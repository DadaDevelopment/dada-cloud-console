package costengine

import (
	"math"
	"testing"
)

// TestPerMinuteCostTimesMinutesPerMonthIsTheMonthlyCost is the product claim, as a
// test.
//
// The brief promises a box minute that "matches a VPS". That claim is not kept by a
// price list somebody keeps in sync; it is kept by this identity — 43200 minutes of
// a footprint cost exactly what a month of the same footprint costs, because the
// per-minute figure is the monthly figure divided by 43200 and by nothing else. If
// a future change introduces a rounding step, a per-minute floor, or a separate
// per-minute table, this test is what fails.
func TestPerMinuteCostTimesMinutesPerMonthIsTheMonthlyCost(t *testing.T) {
	unit := UnitCost{PerVCPU: 50, PerGBRAM: 142.857142857, PerGBStorage: 4.444444444}
	footprints := []Footprint{
		{VCPU: 2, RAMGB: 4, StorageGB: 20},     // box-standard
		{VCPU: 4, RAMGB: 8, StorageGB: 40},     // box-large
		{VCPU: 8, RAMGB: 16, StorageGB: 80},    // box-xl
		{StorageGB: 20},                        // a sleeping box: disk only
		{VCPU: 0.1, RAMGB: 0.25, StorageGB: 1}, // the free plan's own footprint
	}
	for _, fp := range footprints {
		monthly := PlanCost(fp, unit)
		reassembled := PerMinuteCost(fp, unit) * MinutesPerMonth
		if math.Abs(reassembled-monthly) > 1e-9 {
			t.Errorf("footprint %+v: %d minutes cost %.12f but a month costs %.12f — "+
				"the \"a box minute matches a VPS\" claim is exactly this identity",
				fp, MinutesPerMonth, reassembled, monthly)
		}
	}
}

// TestMinutesPerMonthIsFixedNotCalendarBased pins the divisor. A calendar-aware
// divisor would make a February minute cost 7% more than a July minute for the same
// box, and nobody could quote a price.
func TestMinutesPerMonthIsFixedNotCalendarBased(t *testing.T) {
	if MinutesPerMonth != 30*24*60 {
		t.Fatalf("MinutesPerMonth = %d, want %d (30d x 24h x 60m)", MinutesPerMonth, 30*24*60)
	}
}

// TestPerMinuteCostIsZeroForAnEmptyFootprint: a footprint of nothing costs nothing,
// which matters because it is the boundary the ledger relies on — an idle minute
// writes no row at all, and the reason it does not need one is that there would be
// nothing to charge for.
func TestPerMinuteCostIsZeroForAnEmptyFootprint(t *testing.T) {
	if got := PerMinuteCost(Footprint{}, UnitCost{PerVCPU: 50, PerGBRAM: 100, PerGBStorage: 5}); got != 0 {
		t.Fatalf("PerMinuteCost(empty) = %v, want 0", got)
	}
}
