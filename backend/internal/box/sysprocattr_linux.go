//go:build linux

package box

import "syscall"

// newNamespaceSysProcAttr describes how a box init is cloned: its own mount,
// pid, uts and ipc namespaces, with the mount namespace additionally unshared
// so nothing it mounts propagates back to the host. Setpgid keeps the whole
// tree killable by process group.
//
// CLONE_NEW* live in package syscall only on Linux, which is why this sits
// behind a build tag -- see sysprocattr_other.go for the non-Linux stub.
func newNamespaceSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC,
		Unshareflags: syscall.CLONE_NEWNS,
		Setpgid:      true,
	}
}
