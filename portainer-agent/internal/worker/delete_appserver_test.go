package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
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
