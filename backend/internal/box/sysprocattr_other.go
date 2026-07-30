//go:build !linux

package box

import (
	"fmt"
	"runtime"
	"syscall"
)

// newNamespaceSysProcAttr is the non-Linux stub, and it fails instead of
// degrading. Keeping it compilable off Linux is what lets the rest of the
// console build and its platform-neutral box tests run on a developer's macOS
// machine, but a box started without CLONE_NEWNS/NEWPID/NEWUTS/NEWIPC is not a
// weaker box -- it is an unisolated process tree chrooted into the host, which
// is the one outcome this runtime exists to prevent. So the seam reports that
// the platform is unsupported and the caller aborts.
func newNamespaceSysProcAttr() (*syscall.SysProcAttr, error) {
	return nil, fmt.Errorf("box: the local box runtime is unsupported on %s/%s: it requires Linux mount, pid, uts and ipc namespaces (CLONE_NEW*), which exist only on Linux -- run the box runtime on a Linux host", runtime.GOOS, runtime.GOARCH)
}
