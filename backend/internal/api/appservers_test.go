package api

import "testing"

func TestIsValidAppServerRegion(t *testing.T) {
	valid := []string{"ru1", "ru2", "kz1", "eu1"}
	for _, region := range valid {
		if !isValidAppServerRegion(region) {
			t.Errorf("expected %q to be accepted", region)
		}
	}

	invalid := []string{"", "ru3", "us1", "RU1"}
	for _, region := range invalid {
		if isValidAppServerRegion(region) {
			t.Errorf("expected %q to be rejected", region)
		}
	}
}
