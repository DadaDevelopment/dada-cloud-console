//go:build linux

package box

import (
	"syscall"
	"testing"
)

// TestNewNamespaceSysProcAttrLinux pins the isolation the box runtime depends
// on. Each flag here is load-bearing: NEWPID is what makes cmd.Process.Pid the
// namespace's init, NEWNS plus Unshareflags is what stops the box's mounts
// propagating back to the host, and Setpgid is what keeps the tree killable by
// process group.
func TestNewNamespaceSysProcAttrLinux(t *testing.T) {
	attr, err := newNamespaceSysProcAttr()
	if err != nil {
		t.Fatalf("newNamespaceSysProcAttr on linux: %v", err)
	}
	want := uintptr(syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC)
	if attr.Cloneflags != want {
		t.Fatalf("Cloneflags = %#x, want %#x", attr.Cloneflags, want)
	}
	if attr.Unshareflags != uintptr(syscall.CLONE_NEWNS) {
		t.Fatalf("Unshareflags = %#x, want CLONE_NEWNS %#x", attr.Unshareflags, uintptr(syscall.CLONE_NEWNS))
	}
	if !attr.Setpgid {
		t.Fatal("Setpgid must stay true or the box tree is not killable by process group")
	}
}
