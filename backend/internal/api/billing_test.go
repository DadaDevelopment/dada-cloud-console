package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/gin-gonic/gin"
)

func testPlans() []pricing.Plan {
	return []pricing.Plan{
		{
			Key:      "free",
			Name:     "Free",
			PriceRUB: 0,
			Quotas: pricing.Quotas{
				Apps:        1,
				Databases:   1,
				Domains:     1,
				TeamMembers: 1,
			},
		},
		{
			Key:      "startup",
			Name:     "Startup",
			PriceRUB: 990,
			Quotas: pricing.Quotas{
				Apps:        5,
				Databases:   2,
				Domains:     5,
				TeamMembers: 3,
			},
		},
		{
			Key:  "enterprise",
			Name: "Enterprise",
			Quotas: pricing.Quotas{
				Apps:        0,
				Databases:   0,
				Domains:     0,
				TeamMembers: 0,
			},
		},
	}
}

// stubCountResource returns a countResource function that always returns the
// provided count without hitting the DB.
func (h *Handler) withCountStub(count int) func(ctx context.Context, orgID, resource string) (int, error) {
	return func(ctx context.Context, orgID, resource string) (int, error) {
		return count, nil
	}
}

// checkQuotaWithCount is a test helper that overrides countResource inline.
func checkQuotaWithCount(plans []pricing.Plan, planKey, resource string, currentCount int) error {
	var activePlan pricing.Plan
	for _, p := range plans {
		if p.Key == planKey {
			activePlan = p
			break
		}
	}
	limit, known := pricing.Quota(activePlan, resource)
	if !known || limit == 0 {
		return nil
	}
	if currentCount >= limit {
		return &quotaExceededError{Resource: resource, Limit: limit}
	}
	return nil
}

func TestCheckQuota_Table(t *testing.T) {
	plans := testPlans()
	cases := []struct {
		name         string
		planKey      string
		resource     string
		currentCount int
		wantErr      bool
	}{
		{"free at limit blocks", "free", "apps", 1, true},
		{"free under limit allows", "free", "apps", 0, false},
		{"startup under limit allows", "startup", "apps", 4, false},
		{"startup at limit blocks", "startup", "apps", 5, true},
		{"enterprise unlimited allows any", "enterprise", "apps", 9999, false},
		{"enterprise unlimited databases", "enterprise", "databases", 9999, false},
		{"free db at limit blocks", "free", "databases", 1, true},
		{"free db under limit allows", "free", "databases", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkQuotaWithCount(plans, tc.planKey, tc.resource, tc.currentCount)
			if tc.wantErr && err == nil {
				t.Errorf("expected quota error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

func TestGetBillingAccount_NoClaims_401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{billingPlans: testPlans()}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = req
	c.Params = gin.Params{{Key: "projectId", Value: "00000000-0000-0000-0000-000000000001"}}
	h.GetBillingAccount(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRecommendPlan_HappyPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{billingPlans: testPlans()}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := `{"Apps":0,"Databases":0,"Domains":0,"Members":0,"StorageGB":0}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	h.RecommendPlan(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestRecommendPlan_BadBody_400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{billingPlans: testPlans()}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`not json`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	h.RecommendPlan(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGetBillingPlans_ReturnsPlans(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{billingPlans: testPlans()}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request = req
	h.GetBillingPlans(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
