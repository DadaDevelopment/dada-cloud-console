package api

import "testing"

// TestIsValidAppServerRegion guards the allowlist against drifting back into a
// wish list. It used to accept ru2/kz1/eu1 alongside ru1 — regions Beget does
// not have — so the API accepted an order the provider refused minutes later
// with `Region 'eu1' does not exist. Available regions: ru1`, leaving a failed
// server row behind. Add a region here only after the provider serves it.
func TestIsValidAppServerRegion(t *testing.T) {
	if !isValidAppServerRegion("ru1") {
		t.Error("expected ru1 to be accepted")
	}

	invalid := []string{"", "ru2", "kz1", "eu1", "ru3", "us1", "RU1"}
	for _, region := range invalid {
		if isValidAppServerRegion(region) {
			t.Errorf("expected %q to be rejected: the provider does not serve it", region)
		}
	}
}
