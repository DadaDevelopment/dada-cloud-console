package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/gin-gonic/gin"
)

func fptr(v float64) *float64 { return &v }

// TestCostRub_Table verifies the money math: cost = (cpu*PerVCPU + ram*PerGBRAM
// + storage*PerGBStorage) * markup, each nil term contributing 0, rounded 2dp.
func TestCostRub_Table(t *testing.T) {
	h := &Handler{
		billingUnit: costengine.UnitCost{
			PerVCPU:      100,
			PerGBRAM:     10,
			PerGBStorage: 2,
		},
		billingMarkup: 2.7,
	}
	cases := []struct {
		name    string
		cpu     *float64
		ram     *float64
		storage *float64
		want    float64
	}{
		{"all nil is zero", nil, nil, nil, 0},
		{"cpu only", fptr(2), nil, nil, round2(2 * 100 * 2.7)},
		{"ram only", nil, fptr(4), nil, round2(4 * 10 * 2.7)},
		{"storage only", nil, nil, fptr(50), round2(50 * 2 * 2.7)},
		{"cpu+ram (app)", fptr(1.5), fptr(3), nil, round2((1.5*100 + 3*10) * 2.7)},
		{"all three", fptr(2), fptr(4), fptr(10), round2((2*100 + 4*10 + 10*2) * 2.7)},
		{"fractional rounds 2dp", fptr(0.333), nil, nil, round2(0.333 * 100 * 2.7)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := h.costRub(tc.cpu, tc.ram, tc.storage)
			if got != tc.want {
				t.Errorf("costRub(%v,%v,%v) = %v, want %v", tc.cpu, tc.ram, tc.storage, got, tc.want)
			}
		})
	}
}

// TestCostRub_ZeroUnitCost proves the informational surface degrades to 0 when
// unit cost failed to load (never a fatal / never a nonzero fabricated bill).
func TestCostRub_ZeroUnitCost(t *testing.T) {
	h := &Handler{billingMarkup: 2.7}
	if got := h.costRub(fptr(5), fptr(5), fptr(5)); got != 0 {
		t.Fatalf("expected 0 with zero unit cost, got %v", got)
	}
}

func TestMonthStart(t *testing.T) {
	now := time.Date(2026, 7, 6, 13, 30, 0, 0, time.UTC)
	got := monthStart(now)
	want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("monthStart = %v, want %v", got, want)
	}
}

func TestConsumptionJSON_Shape(t *testing.T) {
	pc := projectConsumption{
		PeriodStart: "2026-07-01T00:00:00Z",
		PeriodEnd:   "2026-07-06T00:00:00Z",
		TotalRub:    123.45,
		Resources: []consumptionResource{
			{Kind: "app", Name: "web", CPUCores: fptr(1.5), RAMGB: fptr(2), CostRub: 100},
			{Kind: "database", Name: "db", StorageGB: fptr(10), CostRub: 23.45},
		},
	}
	out := consumptionJSON(pc)
	if out["currency"] != "RUB" {
		t.Fatalf("currency = %v, want RUB", out["currency"])
	}
	if out["total_rub"] != 123.45 {
		t.Fatalf("total_rub = %v, want 123.45", out["total_rub"])
	}
	period, ok := out["period"].(gin.H)
	if !ok || period["start"] != "2026-07-01T00:00:00Z" {
		t.Fatalf("period start wrong: %v", out["period"])
	}
	resources, ok := out["resources"].([]gin.H)
	if !ok || len(resources) != 2 {
		t.Fatalf("resources wrong: %v", out["resources"])
	}
	// app row: storage_gb must be nil (serializes to null), cpu/ram present.
	if resources[0]["storage_gb"] != (*float64)(nil) {
		t.Fatalf("app storage_gb should be nil, got %v", resources[0]["storage_gb"])
	}
	if resources[1]["cpu_cores"] != (*float64)(nil) {
		t.Fatalf("db cpu_cores should be nil, got %v", resources[1]["cpu_cores"])
	}
}

func TestGetProjectConsumption_NoClaims_401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Params = gin.Params{{Key: "projectId", Value: "00000000-0000-0000-0000-000000000001"}}
	h.GetProjectConsumption(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestGetAccountSummary_NoClaims_401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	h.GetAccountSummary(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestCallerOrg_EmptyClaims covers the free/empty-org fallback used by the
// account summary plan resolution. The full 200 path needs a DB pool
// (readableProjectIDs), covered by integration tests.
func TestCallerOrg_EmptyClaims(t *testing.T) {
	h := &Handler{}
	if org := h.callerOrg(&auth.Claims{}); org != "" {
		t.Fatalf("callerOrg with empty claims should be empty, got %q", org)
	}
	if org := h.callerOrg(nil); org != "" {
		t.Fatalf("callerOrg(nil) should be empty, got %q", org)
	}
}
