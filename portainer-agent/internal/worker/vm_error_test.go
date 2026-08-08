package worker

import (
	"errors"
	"strings"
	"testing"
)

// TestFriendlyVMError_TerraformRegionDiagnostic pins the failure a user actually
// read in the console: a rejected region arrived as the whole rendered Terraform
// diagnostic, so the server row led with `exit status 1` and file coordinates
// and buried the one sentence that explained the refusal.
func TestFriendlyVMError_TerraformRegionDiagnostic(t *testing.T) {
	raw := errors.New(`terraform apply: tf apply: exit status 1

Error: Region not found

  with beget_compute_instance.app_server,
  on main.tf line 34, in resource "beget_compute_instance" "app_server":
  34:   region = var.region

Region 'eu1' does not exist. Available regions: ru1
`)

	got := friendlyVMError(raw)
	want := "Region not found: Region 'eu1' does not exist. Available regions: ru1"
	if got != want {
		t.Errorf("friendlyVMError:\n got %q\nwant %q", got, want)
	}
	for _, noise := range []string{"exit status", "main.tf line", "with beget_compute_instance"} {
		if strings.Contains(got, noise) {
			t.Errorf("message still carries %q: %q", noise, got)
		}
	}
}

func TestFriendlyVMError_NonTerraformError(t *testing.T) {
	got := friendlyVMError(errors.New("ssh bootstrap: dial tcp 10.0.0.1:22: connect: connection refused"))
	want := "ssh bootstrap: dial tcp 10.0.0.1:22: connect: connection refused"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFriendlyVMError_NilAndTruncation(t *testing.T) {
	if got := friendlyVMError(nil); got != "" {
		t.Errorf("nil error = %q, want empty", got)
	}
	long := strings.Repeat("x", maxAppServerErrorLen*2)
	got := friendlyVMError(errors.New(long))
	if len([]rune(got)) != maxAppServerErrorLen {
		t.Errorf("truncated length = %d, want %d", len([]rune(got)), maxAppServerErrorLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated message must be marked as clipped: %q", got)
	}
}
