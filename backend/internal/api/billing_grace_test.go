package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDueGraceStage(t *testing.T) {
	graceUntil := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return graceUntil.Add(-d) }
	ptr := func(tm time.Time) *time.Time { return &tm }

	cases := []struct {
		name       string
		notifiedAt *time.Time
		now        time.Time
		wantDays   int
		wantDue    bool
	}{
		{"far out, silent", nil, at(60 * 24 * time.Hour), 0, false},
		{"30d window opens", nil, at(29 * 24 * time.Hour), 30, true},
		{"30d already sent", ptr(at(29 * 24 * time.Hour)), at(20 * 24 * time.Hour), 0, false},
		{"7d after 30d sent", ptr(at(29 * 24 * time.Hour)), at(6 * 24 * time.Hour), 7, true},
		{"7d already sent", ptr(at(6 * 24 * time.Hour)), at(5 * 24 * time.Hour), 0, false},
		{"final day", ptr(at(6 * 24 * time.Hour)), at(12 * time.Hour), 1, true},
		{"final already sent", ptr(at(12 * time.Hour)), at(2 * time.Hour), 0, false},
		{"late backfill gets one notice", nil, at(2 * 24 * time.Hour), 7, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := graceAccount{OrgID: "org", GraceUntil: graceUntil, NotifiedAt: tc.notifiedAt}
			stage, ok := dueGraceStage(a, tc.now)
			if ok != tc.wantDue {
				t.Fatalf("due=%v, want %v", ok, tc.wantDue)
			}
			if !ok {
				return
			}
			if days := int(stage / (24 * time.Hour)); days != tc.wantDays {
				t.Fatalf("stage=%dd, want %dd", days, tc.wantDays)
			}
		})
	}
}

func TestGraceDaysLeft(t *testing.T) {
	graceUntil := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		remaining time.Duration
		want      int
	}{
		{30 * 24 * time.Hour, 30},
		{6*24*time.Hour + 5*time.Hour, 7},
		{25 * time.Hour, 2},
		{time.Hour, 1},
		{0, 1},
	}
	for _, tc := range cases {
		if got := graceDaysLeft(graceUntil, graceUntil.Add(-tc.remaining)); got != tc.want {
			t.Fatalf("remaining=%s: days=%d, want %d", tc.remaining, got, tc.want)
		}
	}
}

type bodyMailer struct {
	sends []struct{ to, subject, body string }
}

func (m *bodyMailer) Send(to, subject, body string) error {
	m.sends = append(m.sends, struct{ to, subject, body string }{to, subject, body})
	return nil
}

// seedGraceOrg creates a free org inside its grandfathering window holding two
// apps, which is over the test free plan's single-app quota.
func seedGraceOrg(t *testing.T, pool *pgxpool.Pool, graceUntil time.Time) string {
	t.Helper()
	ctx := context.Background()
	orgID := "org-grace-" + uuid.NewString()[:8]

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"grace-"+uuid.NewString()[:8], orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	var envID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type) VALUES ($1, 'prod', $2, 'prod') RETURNING id`,
		projectID, "grace-ns-"+uuid.NewString()[:8],
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase)
			 VALUES ($1, $2, 'App', $3, 'Ready')`,
			projectID, envID, "app-"+uuid.NewString()[:6],
		); err != nil {
			t.Fatalf("seed app snapshot: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, quota_grace_until, updated_at)
		VALUES ($1, 'free', now(), $2, now())
	`, orgID, graceUntil); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})
	return orgID
}

func TestSweepQuotaGrace_NotifiesOncePerStageWithRealNumbers(t *testing.T) {
	pool := quotaGatePool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	graceUntil := now.Add(20 * 24 * time.Hour)
	orgID := seedGraceOrg(t, pool, graceUntil)

	mailer := &bodyMailer{}
	SweepQuotaGrace(ctx, pool, mailer, "ops@example.com", testPlans(), now)

	var mine []struct{ to, subject, body string }
	for _, s := range mailer.sends {
		if strings.Contains(s.subject, orgID) || strings.Contains(s.body, orgID) {
			mine = append(mine, s)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("sends for %s = %d, want 1 operator copy", orgID, len(mine))
	}
	if !strings.Contains(mine[0].body, "приложения: 2, бесплатно 1") {
		t.Fatalf("body carries no per-org numbers:\n%s", mine[0].body)
	}
	if !strings.Contains(mine[0].subject, "20 дн.") {
		t.Fatalf("subject=%q want the real remaining days", mine[0].subject)
	}

	var notifiedAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT grace_notified_at FROM billing_accounts WHERE org_id = $1`, orgID,
	).Scan(&notifiedAt); err != nil {
		t.Fatalf("read grace_notified_at: %v", err)
	}
	if notifiedAt == nil {
		t.Fatal("grace_notified_at not recorded; every tick would re-send")
	}

	before := len(mailer.sends)
	SweepQuotaGrace(ctx, pool, mailer, "ops@example.com", testPlans(), now.Add(time.Hour))
	if len(mailer.sends) != before {
		t.Fatalf("same stage re-fired on the next tick: sends %d -> %d", before, len(mailer.sends))
	}
}

func TestSweepQuotaGrace_ExpiredWindow_Silent(t *testing.T) {
	pool := quotaGatePool(t)
	now := time.Now().UTC()
	orgID := seedGraceOrg(t, pool, now.Add(-24*time.Hour))

	mailer := &bodyMailer{}
	SweepQuotaGrace(context.Background(), pool, mailer, "ops@example.com", testPlans(), now)
	for _, s := range mailer.sends {
		if strings.Contains(s.subject, orgID) || strings.Contains(s.body, orgID) {
			t.Fatal("notified an org whose grace already ended; the mail would be pointless")
		}
	}
}

func TestFreePlanOf(t *testing.T) {
	plans := []pricing.Plan{{Key: "startup"}, {Key: "free"}, {Key: "business"}}
	p, ok := freePlanOf(plans)
	if !ok || p.Key != "free" {
		t.Fatalf("got %q ok=%v, want free", p.Key, ok)
	}
	if _, ok := freePlanOf([]pricing.Plan{{Key: "startup"}}); ok {
		t.Fatal("catalog without a free plan must not resolve one")
	}
}
