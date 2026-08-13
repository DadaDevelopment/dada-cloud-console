package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestGetBillingPlans_ShapeIsReadableByConsole walks the exact selection the
// quota upsell performs -- "cheapest paid plan whose limit for the refused
// resource is above the one I just hit" -- over the real handler response.
//
// The upsell reads price_rub and quotas[resource] off this payload. When those
// keys were absent (the structs carried yaml tags only) the filter matched
// nothing, silently, and every refusal fell through to the generic pricing
// link. Asserting a 200 cannot see that; reading the payload the way the
// console reads it can.
func TestGetBillingPlans_ShapeIsReadableByConsole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{billingPlans: testPlans()}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	h.GetBillingPlans(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Plans []struct {
			Key      string         `json:"key"`
			Name     string         `json:"name"`
			PriceRUB *float64       `json:"price_rub"`
			Quotas   map[string]int `json:"quotas"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode plans: %v", err)
	}
	if len(body.Plans) == 0 {
		t.Fatalf("no plans in response: %s", rec.Body.String())
	}
	for _, p := range body.Plans {
		if p.Key == "" || p.Name == "" {
			t.Errorf("plan is missing key/name: %s", rec.Body.String())
		}
		if p.PriceRUB == nil {
			t.Errorf("plan %q has no price_rub: %s", p.Key, rec.Body.String())
		}
		if len(p.Quotas) == 0 {
			t.Errorf("plan %q has no readable quotas: %s", p.Key, rec.Body.String())
		}
	}

	// The upsell's own query: refused at the free tier's 1-app limit, which
	// paid plan raises it?
	var target string
	var targetPrice float64
	for _, p := range body.Plans {
		if p.PriceRUB == nil || *p.PriceRUB <= 0 {
			continue
		}
		limit, ok := p.Quotas["apps"]
		if !ok || limit <= 1 {
			continue
		}
		if target == "" || *p.PriceRUB < targetPrice {
			target, targetPrice = p.Key, *p.PriceRUB
		}
	}
	if target != "startup" {
		t.Errorf("upsell picked %q for a 1-app refusal, want startup", target)
	}
}

// TestRecommendPlan_BindsConsoleBody posts the body the pricing page actually
// sends. A struct without json tags binds none of it and answers "free" to a
// need no free plan covers.
func TestRecommendPlan_BindsConsoleBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{billingPlans: testPlans()}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := `{"apps":4,"databases":2,"domains":3,"members":3,"storage_gb":1}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	h.RecommendPlan(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Recommended string `json:"recommended"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode recommendation: %v", err)
	}
	if out.Recommended != "startup" {
		t.Errorf("recommended %q for a 4-app need, want startup: %s", out.Recommended, rec.Body.String())
	}
}
