package api

import "testing"

func TestConsumptionPricing_Price(t *testing.T) {
	p := consumptionPricing{markup: 1.5}
	got := p.price(1, 1, 1)
	want := 4.5
	if got != want {
		t.Fatalf("price = %v, want %v", got, want)
	}
	if p.price(0, 0, 0) != 0 {
		t.Fatalf("zero costs must price to 0")
	}
}
