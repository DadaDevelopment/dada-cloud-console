//go:build !linux

package box

import (
	"strings"
	"testing"
)

// TestNewNamespaceSysProcAttrUnsupported pins the one behaviour that makes the
// build tag safe rather than merely convenient: off Linux the seam must FAIL.
// The tempting stub returns a Setpgid-only SysProcAttr, which compiles, starts a
// box, and produces an unisolated process tree chrooted into the developer's own
// machine -- so this asserts the error, and that it names the platform.
func TestNewNamespaceSysProcAttrUnsupported(t *testing.T) {
	attr, err := newNamespaceSysProcAttr()
	if err == nil {
		t.Fatalf("expected an unsupported-platform error off Linux, got attr=%+v", attr)
	}
	if attr != nil {
		t.Fatalf("expected a nil SysProcAttr alongside the error, got %+v", attr)
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error should say the platform is unsupported, got %q", err)
	}
}
