//go:build !linux

package box

import "syscall"

// newNamespaceSysProcAttr is the non-Linux stub: only Setpgid, because Linux
// namespace clone flags do not exist on other platforms. Keeping it compilable
// off Linux is what lets the rest of the console build and its tests run on a
// developer's macOS machine; the box runtime itself is only ever executed on
// Linux hosts, where the tagged sibling applies.
func newNamespaceSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
