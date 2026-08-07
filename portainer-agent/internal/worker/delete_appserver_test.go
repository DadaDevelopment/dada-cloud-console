package worker

import (
	"context"
	"errors"
	"testing"
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

func TestRemoveVMViaProvider_PropagatesProviderError(t *testing.T) {
	f := &fakeRemover{err: errors.New("beget said no")}
	w := &VMWatcher{beget: f}

	if err := w.removeVMViaProvider(context.Background(), strptr("vm-1")); err == nil {
		t.Fatal("expected the provider error to surface so the operation fails instead of orphaning a billed VM")
	}
}
