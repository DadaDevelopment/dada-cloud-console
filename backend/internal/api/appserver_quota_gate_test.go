package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

func appServerQuotaTestPlans() []pricing.Plan {
	return []pricing.Plan{
		{
			Key:  "free",
			Name: "Free",
			Quotas: pricing.Quotas{
				Apps:       1,
				Databases:  1,
				Domains:    1,
				AppServers: 0,
			},
		},
		{
			Key:      "startup",
			Name:     "Startup",
			PriceRUB: 990,
			Quotas: pricing.Quotas{
				Apps:       5,
				Databases:  2,
				Domains:    5,
				AppServers: 1,
			},
		},
	}
}

func manualAppServerBody(name string) string {
	return `{"name":"` + name + `","mode":"manual","vm_ip":"203.0.113.10","ssh_private_key":"key-material"}`
}

func TestCreateAppServer_FreePlan_QuotaBlocked(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-vm-quota-free-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	claims := godClaims(seedUser(t, pool))

	h := &Handler{pool: pool, billingPlans: appServerQuotaTestPlans(), cfg: &config.Config{BillingEnabled: true}}

	c, rec := newCreateCtx(t, manualAppServerBody("vm-"+uuid.NewString()[:8]), params(projectID, uuid.Nil), claims)
	h.CreateAppServer(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error    string `json:"error"`
		Resource string `json:"resource"`
		Upgrade  bool   `json:"upgrade"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	if body.Error != "quota_exceeded" {
		t.Fatalf("error = %q, want quota_exceeded", body.Error)
	}
	if body.Resource != "app_servers" {
		t.Fatalf("resource = %q, want app_servers", body.Resource)
	}
	if !body.Upgrade {
		t.Fatalf("upgrade = false, want true so the console can offer a paid plan")
	}

	var n int
	if err := pool.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM app_servers WHERE project_id = $1`, projectID,
	).Scan(&n); err != nil {
		t.Fatalf("count app_servers: %v", err)
	}
	if n != 0 {
		t.Fatalf("app_servers rows = %d, want 0; a quota-blocked request must not create anything", n)
	}

	var auditOutcome string
	if err := pool.QueryRow(c.Request.Context(),
		`SELECT outcome FROM audit_events
		 WHERE project_id = $1 AND action = 'CreateAppServer'
		 ORDER BY created_at DESC LIMIT 1`,
		projectID,
	).Scan(&auditOutcome); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if auditOutcome != auditOutcomeFailure {
		t.Fatalf("audit outcome = %q, want %q; a quota block must be measurable in the audit trail", auditOutcome, auditOutcomeFailure)
	}
}

func TestCreateAppServer_PaidPlanUnderQuota_Allowed(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-vm-quota-paid-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	claims := godClaims(seedUser(t, pool))

	if _, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, updated_at)
		VALUES ($1, 'startup', now(), now())
	`, orgID); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})

	h := &Handler{pool: pool, billingPlans: appServerQuotaTestPlans(), cfg: &config.Config{BillingEnabled: true}}

	name := "vm-" + uuid.NewString()[:8]
	c2, rec := newCreateCtx(t, manualAppServerBody(name), params(projectID, uuid.Nil), claims)
	h.CreateAppServer(c2)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	var n int
	if err := pool.QueryRow(c2.Request.Context(),
		`SELECT COUNT(*) FROM operations WHERE project_id = $1 AND action = 'CreateAppServer' AND resource_name = $2`,
		projectID, name,
	).Scan(&n); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if n != 1 {
		t.Fatalf("CreateAppServer operations = %d, want 1", n)
	}
}
