package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCustomerPathExcludesNonCustomers guards migration 092. The exclusion of
// our own probes used to live in whoever was writing the analysis that day,
// which is why the single app.diagnose row ever recorded -- a synthetic
// actor's -- kept reading as customer demand.
func TestCustomerPathExcludesNonCustomers(t *testing.T) {
	pool := testAuditPool(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	customer := seedPathUser(t, pool, "path-c-"+suffix, "path-c-"+suffix+"@example.test")
	synthetic := seedPathUser(t, pool, "path-s-"+suffix, "path-s-"+suffix+"@keycloak.local")
	internal := seedPathUser(t, pool, "path-i-"+suffix, "path-i-"+suffix+"@dada-tuda.ru")

	action := "PathProbe" + suffix
	for _, actor := range []uuid.UUID{customer, synthetic, internal, systemDeployActorID} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_events (actor_id, action, resource_kind, resource_name, outcome)
			 VALUES ($1, $2, 'Probe', $3, 'success')`,
			actor, action, "probe-"+suffix,
		); err != nil {
			t.Fatalf("seed audit row for %v: %v", actor, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE action = $1`, action)
	})

	rows, err := pool.Query(ctx,
		`SELECT user_id FROM customer_path WHERE action = $1`, action)
	if err != nil {
		t.Fatalf("read customer_path: %v", err)
	}
	defer rows.Close()
	var seen []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen = append(seen, id)
	}
	if rows.Err() != nil {
		t.Fatalf("iterate: %v", rows.Err())
	}
	if len(seen) != 1 || seen[0] != customer {
		t.Fatalf("customer_path returned %v, want exactly the customer %v", seen, customer)
	}

	var total int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_path WHERE action = $1`, action).Scan(&total); err != nil {
		t.Fatalf("read user_path: %v", err)
	}
	if total != 4 {
		t.Fatalf("user_path must stay unfiltered, got %d rows want 4", total)
	}
}

// TestCustomerPathKeepsAnonymousVisitors pins the one exception. A browser with
// no account yet is the top of the funnel, not an excluded actor: dropping it
// would cut off every pre-signup walk, which is the half that answers where
// customers come from.
func TestCustomerPathKeepsAnonymousVisitors(t *testing.T) {
	pool := testAuditPool(t)
	ctx := context.Background()

	anonID := uuid.New()
	eventType := "landing_view_" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx,
		`INSERT INTO ux_events (anon_id, event_type, target, path, occurred_at)
		 VALUES ($1, $2, 'hero', '/', now())`,
		anonID, eventType,
	); err != nil {
		t.Fatalf("seed anonymous ux event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ux_events WHERE anon_id = $1`, anonID)
	})

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_path WHERE anon_id = $1 AND action = $2`, anonID, eventType,
	).Scan(&n); err != nil {
		t.Fatalf("read customer_path: %v", err)
	}
	if n != 1 {
		t.Fatalf("an un-stitched visitor must survive the filter, got %d rows", n)
	}
}

func seedPathUser(t *testing.T, pool *pgxpool.Pool, username, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, email, password_hash, display_name) VALUES ($1, $2, '', $1) RETURNING id`,
		username, email,
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	t.Cleanup(func() {
		dropSeededUser(pool, id)
	})
	return id
}
