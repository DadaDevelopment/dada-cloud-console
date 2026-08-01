package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/google/uuid"
)

func decodeQuotaEnforced(t *testing.T, body []byte) bool {
	t.Helper()
	var resp struct {
		QuotaEnforced bool `json:"quota_enforced"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, body)
	}
	return resp.QuotaEnforced
}

func TestGetBillingAccount_QuotaEnforced_BillingDisabled_False(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-qe-disabled-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans(), cfg: &config.Config{BillingEnabled: false}}
	c, rec := newBillingCtx(http.MethodGet, "/", "", godClaims(uuid.New()), projectID)
	h.GetBillingAccount(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := decodeQuotaEnforced(t, rec.Body.Bytes()); got {
		t.Fatalf("quota_enforced=true with BillingEnabled=false; billing off must never claim enforcement")
	}
}

func TestGetBillingAccount_QuotaEnforced_ExemptOrg_False(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-qe-exempt-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)

	h := &Handler{
		pool:         pool,
		billingPlans: testPlans(),
		cfg:          &config.Config{BillingEnabled: true, BillingExemptOrgs: []string{orgID}},
	}
	c, rec := newBillingCtx(http.MethodGet, "/", "", godClaims(uuid.New()), projectID)
	h.GetBillingAccount(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := decodeQuotaEnforced(t, rec.Body.Bytes()); got {
		t.Fatalf("quota_enforced=true for a BILLING_EXEMPT_ORGS member")
	}
}

func TestGetBillingAccount_QuotaEnforced_ActiveGrace_False(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-qe-grace-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)
	future := time.Now().UTC().Add(30 * 24 * time.Hour)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO billing_accounts (org_id, plan, plan_assigned_at, quota_grace_until, updated_at)
		VALUES ($1, 'free', now(), $2, now())
	`, orgID, future); err != nil {
		t.Fatalf("seed billing account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM billing_accounts WHERE org_id = $1`, orgID)
	})

	h := &Handler{pool: pool, billingPlans: testPlans(), cfg: &config.Config{BillingEnabled: true}}
	c, rec := newBillingCtx(http.MethodGet, "/", "", godClaims(uuid.New()), projectID)
	h.GetBillingAccount(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := decodeQuotaEnforced(t, rec.Body.Bytes()); got {
		t.Fatalf("quota_enforced=true during an active grace window")
	}
}

func TestGetBillingAccount_QuotaEnforced_NoBypass_True(t *testing.T) {
	pool := testPaymentsPool(t)
	orgID := "org-qe-live-" + uuid.NewString()[:8]
	projectID := seedPaymentsProject(t, pool, orgID)

	h := &Handler{pool: pool, billingPlans: testPlans(), cfg: &config.Config{BillingEnabled: true}}
	c, rec := newBillingCtx(http.MethodGet, "/", "", godClaims(uuid.New()), projectID)
	h.GetBillingAccount(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := decodeQuotaEnforced(t, rec.Body.Bytes()); !got {
		t.Fatalf("quota_enforced=false with billing on, org not exempt, no grace; quotas ARE enforced here")
	}
}
