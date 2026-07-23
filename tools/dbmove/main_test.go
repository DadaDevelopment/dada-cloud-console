package main

import "testing"

func TestValidateFlagsRejectsOnlyAndFromTogether(t *testing.T) {
	if err := validateFlags("verify", "safety-dump"); err == nil {
		t.Fatal("expected error when --only and --from are both set")
	}
}

func TestValidateFlagsAllowsOnlyAlone(t *testing.T) {
	if err := validateFlags("verify", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateFlagsAllowsFromAlone(t *testing.T) {
	if err := validateFlags("", "safety-dump"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateFlagsAllowsNeither(t *testing.T) {
	if err := validateFlags("", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
