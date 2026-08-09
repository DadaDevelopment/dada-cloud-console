package worker

import (
	"context"
	"errors"
	"testing"

	"os"
	"path/filepath"
	"strings"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
	tf "github.com/dada-tuda/console/portainer-agent/internal/terraform"
)

type fakeRemover struct {
	called []string
	err    error
}

func (f *fakeRemover) RemoveVPS(_ context.Context, id string) error {
	f.called = append(f.called, id)
	return f.err
}

func strptr(s string) *string { return &s }

// TestRemoveVMViaProvider proves the provider-level fallback deletes the exact
// machine Terraform created. It exists because the console once marked an
// AppServer deleted while its VM kept running and billing (le-probe, 08-07):
// the agent pod restarted, its ephemeral Terraform workspace went with it, and
// `terraform destroy` never ran.
func TestRemoveVMViaProvider(t *testing.T) {
	f := &fakeRemover{}
	w := &VMWatcher{beget: f}

	if err := w.removeVMViaProvider(context.Background(), strptr("cea6860c")); err != nil {
		t.Fatalf("removeVMViaProvider: %v", err)
	}
	if len(f.called) != 1 || f.called[0] != "cea6860c" {
		t.Errorf("removed = %v, want [cea6860c]", f.called)
	}
}

func TestRemoveVMViaProvider_RefusesWithoutProviderID(t *testing.T) {
	f := &fakeRemover{}
	w := &VMWatcher{beget: f}

	for _, id := range []*string{nil, strptr("")} {
		if err := w.removeVMViaProvider(context.Background(), id); err == nil {
			t.Error("expected an error when no vm_provider_id is recorded")
		}
	}
	if len(f.called) != 0 {
		t.Errorf("provider must not be called without an id, got %v", f.called)
	}
}

// TestMayHaveMachine separates the two rows that both fail every destroy path:
// one that never got a machine (safe to remove) and one that has a handle on a
// billed VM (must keep failing loudly). Treating them alike is what left a
// failed provisioning parked in Deleting with no way out.
func TestMayHaveMachine(t *testing.T) {
	cases := []struct {
		name   string
		server *db.AppServerRow
		want   bool
	}{
		{"never provisioned", &db.AppServerRow{}, false},
		{"empty handles", &db.AppServerRow{VMIP: strptr(""), VMProviderID: strptr("")}, false},
		{"has provider id", &db.AppServerRow{VMProviderID: strptr("cea6860c")}, true},
		{"has ip only", &db.AppServerRow{VMIP: strptr("5.101.0.7")}, true},
	}
	for _, tc := range cases {
		if got := mayHaveMachine(tc.server); got != tc.want {
			t.Errorf("%s: mayHaveMachine = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRemoveVMViaProvider_PropagatesProviderError(t *testing.T) {
	f := &fakeRemover{err: errors.New("beget said no")}
	w := &VMWatcher{beget: f}

	if err := w.removeVMViaProvider(context.Background(), strptr("vm-1")); err == nil {
		t.Fatal("expected the provider error to surface so the operation fails instead of orphaning a billed VM")
	}
}

// TestVMResourceAddressMatchesTemplate pins the destroy scope to the Terraform
// template. An unscoped destroy is impossible here: beget_ssh_key.deploy carries
// lifecycle.prevent_destroy, and its presence in the plan makes Terraform reject
// the whole destroy — on 2026-08-09 that left a farm-account VM running and
// billed after DeleteAppServer reported failure. Renaming the instance resource
// in the template without updating vmResourceAddress would silently restore that
// failure, so both halves of the contract are asserted here.
func TestVMResourceAddressMatchesTemplate(t *testing.T) {
	dir := t.TempDir()
	if err := tf.PrepareWorkspace(dir); err != nil {
		t.Fatalf("prepare workspace: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatalf("read main.tf: %v", err)
	}
	main := string(body)

	kind, name, ok := strings.Cut(vmResourceAddress, ".")
	if !ok {
		t.Fatalf("vmResourceAddress %q is not <type>.<name>", vmResourceAddress)
	}
	decl := "resource \"" + kind + "\" \"" + name + "\""
	if !strings.Contains(main, decl) {
		t.Fatalf("template declares no %s — destroy would target nothing", vmResourceAddress)
	}
	if !strings.Contains(main, "prevent_destroy") {
		t.Fatal("template no longer sets prevent_destroy — re-check whether scoping the destroy is still required")
	}
	keyDecl := "resource \"beget_ssh_key\" \"deploy\""
	if !strings.Contains(main, keyDecl) {
		t.Fatal("shared deploy key resource is gone — update the destroy scope")
	}
	if strings.HasPrefix(vmResourceAddress, "beget_ssh_key.") {
		t.Fatalf("vmResourceAddress %q points at the protected shared key", vmResourceAddress)
	}
}
