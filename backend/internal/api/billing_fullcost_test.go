package api

import (
	"math"
	"testing"
)

func TestOverheadFactor(t *testing.T) {
	cases := []struct {
		name      string
		user, tot float64
		want      float64
	}{
		{"users occupy whole cluster -> 1", 10, 10, 1},
		{"users at half -> 2", 5, 10, 2},
		{"share above floor -> exact ratio", 4, 10, 2.5},
		{"share at floor -> capped", 3, 10, 1 / 0.30},
		{"tiny share -> capped at floor", 0.1, 10, 1 / 0.30},
		{"zero total -> 1", 0, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := overheadFactor(c.user, c.tot, 0.30)
			if math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("overheadFactor(%v,%v) = %v, want %v", c.user, c.tot, got, c.want)
			}
		})
	}
}

func TestConsumptionPricing_Price(t *testing.T) {
	p := consumptionPricing{fCPU: 2, fRAM: 1, fPV: 3, margin: 1.4}
	got := p.price(1, 1, 1)
	want := round2((1*2 + 1*1 + 1*3) * 1.4)
	if got != want {
		t.Fatalf("price = %v, want %v", got, want)
	}
	if p.price(0, 0, 0) != 0 {
		t.Fatalf("zero costs must price to 0")
	}
}
