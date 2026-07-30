package box

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LocalRuntime is a real BoxRuntime for one Linux host.
//
// IT IS NOT THE PRODUCTION RUNTIME AND IS NOT NAMED AS IF IT WERE. Production
// runs a box as a Pod in the cloud we already have, per
// docs/adr/ADR-019-box-container-in-cloud-materialized-onto-a-real-vm.md: no new
// VM fleet, no gVisor, and the ephemeral body lives where we already have
// compute. That adapter is a separate implementation of this same interface and
// is not in this repository yet, because an adapter nobody in this environment
// can run is exactly the kind of scaffolding this vertical exists to stop.
//
// What LocalRuntime actually does, and it does all of it for real:
//
//   - A box is its own filesystem tree under Root/instances/<ref>/rootfs, copied
//     from a warm template. That tree carries the box's own /etc, /root, /srv,
//     /var and its named volume mountpoints.
//   - Commands run genuinely inside it. The box's supervised init is started with
//     CLONE_NEWNS|CLONE_NEWPID|CLONE_NEWUTS|CLONE_NEWIPC, mounts the host's
//     /usr /bin /sbin /lib /lib64 /opt read-only into the tree, mounts its own
//     proc, tmpfs /tmp /run and a minimal /dev, then chroots. Every later Exec
//     enters those namespaces with nsenter, so the box sees its own root, its own
//     PID 1 and its own /tmp. This is measured, not assumed: see
//     localruntime_test.go, which asserts the box reads its own /etc/hostname and
//     that a file written outside the tree is not visible inside it.
//   - Its env lives in /etc/dada/box.env, 0600 root, and every Exec sources it.
//     That is the same file crystallization carries over (ADR-019 step 5), so the
//     env path is exercised end to end rather than described.
//
// What it honestly does NOT do, spelled out because a half-truth here is worse
// than a missing feature:
//
//   - NO NETWORK NAMESPACE. The box shares the host's network stack, so egress
//     isolation — the one control ADR-019 calls the main price of the decision —
//     is absent. Production gets it from a NetworkPolicy with default-deny egress
//     in the cluster, which is a cluster-level object this adapter has no
//     equivalent of. ProgramNetwork still performs a real state change (see its
//     doc comment) rather than returning nil to look busy.
//   - No cgroup limits, so a box cannot be denied CPU or memory here.
//   - No user namespace, so "root inside the box" is host root. On a shared host
//     that is unacceptable and it is why B6 in tasks/box-backlog.md is a gate on
//     public access rather than a preference.
//
// Root must be on a filesystem the process can mount into and is created on
// demand. Every path is derived from an InstanceRef minted by this type, never
// from caller input, so a hostile ref cannot escape Root.
type LocalRuntime struct {
	// Root holds the template, the instances, the volume store and the
	// crystallization targets.
	Root string
	// Clock is used for nothing on the measurement path — the orchestrator owns
	// that (see PhaseTimeline) — only for the metadata it writes.
	Clock Clock
	// ReadyTimeout bounds how long Unfreeze waits for the exec channel.
	ReadyTimeout time.Duration
	// Volumes are the named volumes every box of this runtime gets. They are
	// bind-mounted into the tree, which is what makes them mountpoints the
	// crystallizer must exclude from the userland rsync and restore separately
	// (ADR-019 steps 2 and 4).
	Volumes []Volume

	mu    sync.Mutex
	inits map[string]*boxInit
}

// Volume is one named volume and where it is mounted inside the box.
type Volume struct {
	Name      string
	MountPath string
}

// boxInit is a live box's supervised init: the process that owns the box's mount
// and PID namespaces. Killing it tears the box's namespaces down, which is what
// makes Destroy real rather than a directory removal.
type boxInit struct {
	pid int
	cmd *exec.Cmd
}

var _ BoxRuntime = (*LocalRuntime)(nil)

// DefaultBoxVolumes is the named-volume set a box gets. One volume, because the
// point is to prove the mountpoint is excluded from the userland set and restored
// by path, and a second one proves nothing the first does not.
var DefaultBoxVolumes = []Volume{{Name: "data", MountPath: "/data"}}

// sharedSystemDirs are the host directories bind-mounted read-only into every
// box. They are the toolchain: the warm image's promise is that node, python, go,
// git and psql are already there, and on this adapter they come from the host.
//
// Consequence the crystallizer must know about: inside the box these are
// mountpoints, so from outside the box's namespace they are EMPTY directories.
// They are therefore not part of the box's userland set and are excluded from the
// materialization rsync — declared in the verification report as adapter
// exclusions, separately from ADR-019's fixed list, so the two are never confused.
var sharedSystemDirs = []string{"usr", "bin", "sbin", "lib", "lib64", "opt"}

// machineOwnedDirs are created in every box tree so that ADR-019's exclusion list
// has something to exclude. Each one gets a sentinel file, and the verification
// report asserts no sentinel reached the VM — which turns "we passed --exclude"
// into "the exclusion demonstrably held".
var machineOwnedDirs = []string{"proc", "sys", "dev", "run", "tmp", "boot", "lib/modules"}

// hostEtcSkip is what is NOT copied from the host's /etc into the warm template.
//
// The template's /etc is a real distro /etc because the toolchain needs it:
// /etc/alternatives is where half of /usr/bin points, and without
// /etc/ld.so.cache and /etc/nsswitch.conf the box would have binaries it cannot
// run and a resolver it cannot use. The skips are of two kinds and both matter:
// host credentials (never copy a private key into a tenant body) and the
// machine-owned files ADR-019 excludes, which the box must not carry in the first
// place.
var hostEtcSkip = map[string]bool{
	"shadow": true, "shadow-": true, "gshadow": true, "gshadow-": true,
	"ssh": true, "sudoers": true, "sudoers.d": true, "krb5.keytab": true,
	"machine-id": true, "hostname": true, "hosts": true, "resolv.conf": true,
	"fstab": true, "mtab": true, "dada": true, "systemd": true,
}

// NewLocalRuntime builds a LocalRuntime rooted at root.
func NewLocalRuntime(root string, clock Clock) *LocalRuntime {
	if clock == nil {
		clock = SystemClock{}
	}
	return &LocalRuntime{
		Root:         root,
		Clock:        clock,
		ReadyTimeout: 20 * time.Second,
		Volumes:      DefaultBoxVolumes,
		inits:        map[string]*boxInit{},
	}
}

// --- paths -------------------------------------------------------------------

func (r *LocalRuntime) templateDir() string      { return filepath.Join(r.Root, "template", "rootfs") }
func (r *LocalRuntime) instancesDir() string     { return filepath.Join(r.Root, "instances") }
func (r *LocalRuntime) BoxDir(ref string) string { return filepath.Join(r.instancesDir(), ref) }
func (r *LocalRuntime) RootFS(ref string) string { return filepath.Join(r.BoxDir(ref), "rootfs") }
func (r *LocalRuntime) volumeDir(ref string) string {
	return filepath.Join(r.Root, "volumes", ref)
}

// VMRoot is the separate root that stands in for a crystallized VM's filesystem.
func (r *LocalRuntime) VMRoot(name string) string {
	return filepath.Join(r.Root, "vms", name, "root")
}

// --- template ----------------------------------------------------------------

// EnsureTemplate builds the warm template if it is not there yet. Idempotent.
//
// The template is the warm image's stand-in and it is deliberately a real
// userland: a distro /etc, a working /srv/app, and the machine-owned directories
// with sentinels in them. Building it is the cold cost the warm pool exists to
// pay ahead of demand, which is why Warm copies it rather than rebuilding.
func (r *LocalRuntime) EnsureTemplate(ctx context.Context) error {
	tmpl := r.templateDir()
	// The ready marker lives BESIDE the template, not inside it: a file inside
	// would be copied into every box and would then have to be remembered in the
	// crystallization exclusion list, which is exactly the kind of quiet coupling
	// ADR-019 asks to keep out of that list.
	marker := filepath.Join(r.Root, "template", ".ready")
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	staging := tmpl + ".staging"
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("box: clear template staging: %w", err)
	}
	dirs := []string{
		"etc", "etc/dada", "etc/dada/services", "etc/systemd/system",
		"root", "root/.ssh", "home", "srv/app", "var", "var/lib", "var/log",
	}
	dirs = append(dirs, sharedSystemDirs...)
	dirs = append(dirs, machineOwnedDirs...)
	for _, v := range r.Volumes {
		dirs = append(dirs, strings.TrimPrefix(v.MountPath, "/"))
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(staging, d), 0o755); err != nil {
			return fmt.Errorf("box: template mkdir %s: %w", d, err)
		}
	}

	if err := copyHostEtc(staging); err != nil {
		return err
	}
	// /var/run -> /run, as on any Ubuntu. It is what makes the box's own container
	// daemon reachable at the DEFAULT docker socket path without any DOCKER_HOST in
	// the box's env — and therefore without a machine-scoped variable that
	// crystallization would carry to a VM that deliberately has no Docker at all.
	if err := os.Symlink("/run", filepath.Join(staging, "var/run")); err != nil && !os.IsExist(err) {
		return fmt.Errorf("box: template /var/run symlink: %w", err)
	}

	// Same distribution and version as the VM slug crystallization boots from.
	// ADR-019 makes that a mechanism, not advice: materializing one distro's
	// userland onto another's kernel builds a chimera that assembles and then
	// breaks in production. WarmImageOSSlug is what the crystallizer checks.
	osRelease := "NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\nPRETTY_NAME=\"Ubuntu 24.04 LTS\"\n" +
		"DADA_BOX_OS_SLUG=" + WarmImageOSSlug + "\n"
	files := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"etc/os-release":            {osRelease, 0o644},
		"etc/dada/README":           {"Dada Box control-plane files. box.env is 0600 and never enters git.\n", 0o644},
		"srv/app/README":            {"The box's working directory. cwd of every exec.\n", 0o644},
		"root/.profile":             {"export PS1='box:\\w\\$ '\n", 0o644},
		"etc/machine-id":            {"00000000000000000000000000000000\n", 0o444},
		"etc/fstab":                 {"# box: machine-owned, never crystallized\n", 0o644},
		"boot/vmlinuz-box-sentinel": {"box kernel placeholder — must NEVER reach a crystallized VM\n", 0o644},
		"lib/modules/.box-sentinel": {"box modules placeholder — must NEVER reach a crystallized VM\n", 0o644},
		"proc/.box-sentinel":        {"machine-owned\n", 0o644},
		"sys/.box-sentinel":         {"machine-owned\n", 0o644},
		"dev/.box-sentinel":         {"machine-owned\n", 0o644},
		"run/.box-sentinel":         {"machine-owned\n", 0o644},
		"tmp/.box-sentinel":         {"machine-owned\n", 0o644},
	}
	for name, f := range files {
		p := filepath.Join(staging, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		// Remove first, and it is load-bearing rather than defensive: on Ubuntu
		// /etc/os-release is a SYMLINK into /usr/lib, so writing through it would
		// follow the link out of the template — into a directory that is a
		// read-only bind mount inside the box. The box's os-release has to be a
		// real file, because it is what the crystallizer reads to refuse a
		// distribution mismatch (ADR-019 §3).
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("box: template replace %s: %w", name, err)
		}
		if err := os.WriteFile(p, []byte(f.content), f.mode); err != nil {
			return fmt.Errorf("box: template write %s: %w", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(tmpl), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(tmpl); err != nil {
		return err
	}
	if err := os.Rename(staging, tmpl); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(WarmImageOSSlug+"\n"), 0o644)
}

// WarmImageOSSlug is the OS slug the warm box image and the crystallization VM
// must share (ADR-019 §3 / spike S6). It is one constant so a drift is a
// compile-time-visible edit rather than two strings that quietly diverge.
const WarmImageOSSlug = "ubuntu-24-04"

// copyHostEtc copies the host's /etc into the template minus hostEtcSkip.
func copyHostEtc(staging string) error {
	entries, err := os.ReadDir("/etc")
	if err != nil {
		return fmt.Errorf("box: read host /etc: %w", err)
	}
	for _, e := range entries {
		if hostEtcSkip[e.Name()] {
			continue
		}
		src := filepath.Join("/etc", e.Name())
		dst := filepath.Join(staging, "etc", e.Name())
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		// cp -a preserves symlinks, modes and ownership, which /etc/alternatives
		// and /etc/ssl both need to keep working.
		if out, err := exec.Command("cp", "-a", src, dst).CombinedOutput(); err != nil {
			return fmt.Errorf("box: copy /etc/%s into template: %w: %s", e.Name(), err, out)
		}
	}
	return nil
}

// --- warm pool ---------------------------------------------------------------

// Warm creates n pre-warmed instances and parks them in pool.
//
// This is the cold path, and it is on purpose not on the request path: creation
// is what costs, so it happens ahead of demand and a spawn is a claim (see
// Spawn's doc comment). The instances it parks are already unfrozen with their
// namespaces up, so a claim is a bind plus a thaw rather than a boot.
func (r *LocalRuntime) Warm(ctx context.Context, pool *MemoryPool, image, region string, n int) error {
	if err := r.EnsureTemplate(ctx); err != nil {
		return err
	}
	pool.SetTarget(image, region, n)
	for i := 0; i < n; i++ {
		inst, err := r.create(ctx, image, region)
		if err != nil {
			return err
		}
		pool.Add(image, region, inst)
	}
	return nil
}

// create materializes one instance directory from the template and starts its
// supervised init, leaving it quarantined (no resolv.conf, no tenant identity).
func (r *LocalRuntime) create(ctx context.Context, image, region string) (*Instance, error) {
	if err := r.EnsureTemplate(ctx); err != nil {
		return nil, err
	}
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	ref := "local-" + hex.EncodeToString(buf)
	dir := r.BoxDir(ref)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("box: create instance dir: %w", err)
	}
	if out, err := exec.CommandContext(ctx, "cp", "-a", r.templateDir(), r.RootFS(ref)).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("box: seed instance rootfs from template: %w: %s", err, out)
	}
	for _, v := range r.Volumes {
		if err := os.MkdirAll(filepath.Join(r.volumeDir(ref), v.Name), 0o755); err != nil {
			return nil, fmt.Errorf("box: create volume store: %w", err)
		}
		// A sentinel in the volume: the crystallizer asserts it does NOT arrive on
		// the VM through the userland rsync, because the mountpoint is excluded and
		// the volume is restored separately (ADR-019 steps 2 and 4).
		if err := os.WriteFile(filepath.Join(r.volumeDir(ref), v.Name, ".box-volume-sentinel"),
			[]byte("restored by volume sync, never by the userland rsync\n"), 0o644); err != nil {
			return nil, err
		}
	}
	if err := writeRootMarker(r.RootFS(ref), rootMarkerPath, ref); err != nil {
		return nil, err
	}
	inst := &Instance{
		ID:          ref,
		InstanceRef: ref,
		NodeRef:     localNodeRef(),
		Image:       image,
		Region:      region,
	}
	if err := r.startInit(ctx, inst); err != nil {
		return nil, err
	}
	// The container daemon is started HERE, on the cold path, before the box is
	// parked in the pool. It takes seconds, and seconds spent after a claim would
	// be seconds subtracted from the one number the product is sold on — so the
	// warm image's promise ("docker is already there") is kept by paying for it
	// ahead of demand, which is the entire reason the pool exists.
	if err := r.StartDockerDaemon(ctx, inst); err != nil {
		return nil, err
	}
	return inst, nil
}

// dockerdScript starts the box's OWN container daemon inside the box.
//
// Two properties make this "docker inside the box" rather than "the host's docker
// lent to the box", and the difference is the whole security argument:
//
//   - The socket is the box's, at /run/docker.sock on the box's own tmpfs. The
//     host's /var/run/docker.sock is NEVER bind-mounted in. Handing a tenant the
//     host's docker socket is handing them the host, which is exactly what
//     tasks/box-backlog.md phase 4 forbids ("никакого docker.sock").
//   - data-root and exec-root are on that same tmpfs, so nothing the daemon stores
//     is part of the box's userland. That is also correct by ADR-019: the
//     crystallized VM contains no Docker, no compose and no Portainer agent, so a
//     daemon's image store is machine state and must not be carried.
//
// Honest limits, because this is the one place where a comfortable silence would
// be worst: the box sees the host's cgroup hierarchy through an rbind, runs as
// real root with no user namespace, and shares the host kernel. Spike S3 in the
// backlog — rootless BuildKit in a Pod with no privileged flag — is still open,
// and this does not close it.
const dockerdScript = `mkdir -p /run/docker-data /run/docker-exec /var/log
if docker info --format '{{.ServerVersion}}' >/dev/null 2>&1; then echo already; exit 0; fi
setsid dockerd --data-root=/run/docker-data --exec-root=/run/docker-exec \
  --iptables=false --ip6tables=false --bridge=none \
  >/var/log/dockerd.log 2>&1 </dev/null &
echo started
`

// StartDockerDaemon brings the box's own container daemon up and waits until it
// answers. Idempotent.
func (r *LocalRuntime) StartDockerDaemon(ctx context.Context, inst *Instance) error {
	if _, err := exec.LookPath("dockerd"); err != nil {
		// No daemon binary on this host: leave the box without one rather than
		// pretend. EvaluateReadiness will then refuse the box, which is the honest
		// outcome — the warm toolchain really is incomplete.
		return nil
	}
	launch, err := r.Run(ctx, inst, dockerdScript)
	if err != nil {
		return err
	}
	if launch.ExitCode != 0 {
		return fmt.Errorf("box: launching the container daemon inside %s exited %d: %s %s",
			inst.InstanceRef, launch.ExitCode, strings.TrimSpace(launch.Stdout), strings.TrimSpace(launch.Stderr))
	}
	deadline := time.Now().Add(r.DockerTimeout())
	for {
		res, err := r.Run(ctx, inst, `docker info --format '{{.ServerVersion}}'`)
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != "" {
			return nil
		}
		if time.Now().After(deadline) {
			log, _ := r.Run(ctx, inst, "tail -5 /var/log/dockerd.log 2>&1")
			return fmt.Errorf("box: container daemon inside %s did not answer within %s: launch=%q log=%q",
				inst.InstanceRef, r.DockerTimeout(), strings.TrimSpace(launch.Stdout), strings.TrimSpace(log.Stdout))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// DockerTimeout bounds the cold-path wait for the box's container daemon.
func (r *LocalRuntime) DockerTimeout() time.Duration {
	if r.ReadyTimeout <= 0 {
		return 60 * time.Second
	}
	return 3 * r.ReadyTimeout
}

func localNodeRef() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return "local-host/" + host
}

// initScript is the box's init. It runs in fresh mount/PID/UTS/IPC namespaces,
// assembles the box's view of the filesystem and then chroots into it.
//
// The order matters: the read-only binds come first so the toolchain is present,
// then the machine-owned mounts (proc, tmpfs /tmp and /run, a minimal /dev) shadow
// the sentinel files the template put there, then the named volumes. Everything
// after the chroot sees only the box.
const initScript = `set -e
ROOT="$1"; NAME="$2"; VOLSPEC="$3"
for d in usr bin sbin lib lib64 opt; do
  [ -e "/$d" ] || continue
  mkdir -p "$ROOT/$d"
  mount --bind "/$d" "$ROOT/$d"
  mount -o remount,bind,ro "$ROOT/$d"
done
mount -t proc proc "$ROOT/proc"
mount -t sysfs sysfs "$ROOT/sys" 2>/dev/null || mount -t tmpfs tmpfs "$ROOT/sys"
mkdir -p "$ROOT/sys/fs/cgroup" 2>/dev/null || true
# --rbind, not --bind: on a cgroup-v1 host /sys/fs/cgroup is a tmpfs with one
# mount PER CONTROLLER underneath it, and a plain bind carries none of them. A box
# that cannot see the devices controller cannot start a container daemon, which is
# how "docker inside the box" quietly stops being true.
mount --rbind /sys/fs/cgroup "$ROOT/sys/fs/cgroup" 2>/dev/null || true
mount -t tmpfs tmpfs "$ROOT/tmp"
chmod 1777 "$ROOT/tmp"
mount -t tmpfs tmpfs "$ROOT/run"
mount -t tmpfs tmpfs "$ROOT/dev"
mkdir -p "$ROOT/dev/pts" "$ROOT/dev/shm"
mknod -m 666 "$ROOT/dev/null" c 1 3
mknod -m 666 "$ROOT/dev/zero" c 1 5
mknod -m 666 "$ROOT/dev/random" c 1 8
mknod -m 666 "$ROOT/dev/urandom" c 1 9
mknod -m 666 "$ROOT/dev/tty" c 5 0
ln -sf /proc/self/fd "$ROOT/dev/fd"
ln -sf /proc/self/fd/0 "$ROOT/dev/stdin"
ln -sf /proc/self/fd/1 "$ROOT/dev/stdout"
ln -sf /proc/self/fd/2 "$ROOT/dev/stderr"
mount -t tmpfs tmpfs "$ROOT/dev/shm"
IFS=','
for spec in $VOLSPEC; do
  [ -n "$spec" ] || continue
  src="${spec%%:*}"; dst="${spec#*:}"
  mkdir -p "$src" "$ROOT$dst"
  mount --bind "$src" "$ROOT$dst"
done
unset IFS
hostname "$NAME" 2>/dev/null || true
exec chroot "$ROOT" /bin/sh -c 'cd /srv/app 2>/dev/null || cd /; exec sleep infinity'
`

// startInit launches the box's supervised init and records its host-visible pid.
//
// The pid is taken from Go's own clone, not discovered by scanning: with
// Cloneflags carrying CLONE_NEWPID the started process IS the new namespace's
// init, so cmd.Process.Pid is both the handle to enter the namespaces with and
// the one process whose death tears them down.
func (r *LocalRuntime) startInit(ctx context.Context, inst *Instance) error {
	root := r.RootFS(inst.InstanceRef)
	var specs []string
	for _, v := range r.Volumes {
		specs = append(specs, filepath.Join(r.volumeDir(inst.InstanceRef), v.Name)+":"+v.MountPath)
	}
	name := inst.InstanceRef
	cmd := exec.Command("/bin/sh", "-c", initScript, "box-init", root, name, strings.Join(specs, ","))
	attr, err := newNamespaceSysProcAttr()
	if err != nil {
		return err
	}
	cmd.SysProcAttr = attr
	logPath := filepath.Join(r.BoxDir(inst.InstanceRef), "init.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("box: open init log: %w", err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("box: start init: %w", err)
	}
	go func() { _ = cmd.Wait(); logFile.Close() }()

	r.mu.Lock()
	r.inits[inst.InstanceRef] = &boxInit{pid: cmd.Process.Pid, cmd: cmd}
	r.mu.Unlock()

	if err := os.WriteFile(filepath.Join(r.BoxDir(inst.InstanceRef), "init.pid"),
		[]byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		return err
	}
	// Wait until the exec channel accepts AND lands inside the box.
	//
	// The second half is not belt and braces, it is a bug this code already had:
	// nsenter --root uses the TARGET's root, and until the init reaches its chroot
	// the target's root is still the host's /. A probe of `true` therefore succeeds
	// while commands are still running on the HOST — which is how a box's container
	// daemon ended up started on the host's filesystem, silently, with everything
	// downstream still looking green. So the probe reads a marker that only exists
	// inside this instance's tree, and the box is not considered executable until
	// that marker answers with this instance's own ref.
	deadline := time.Now().Add(r.ReadyTimeout)
	for {
		if err := r.probeInsideBox(ctx, inst); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			body, _ := os.ReadFile(logPath)
			return fmt.Errorf("box: init did not become executable inside its own root within %s: %s",
				r.ReadyTimeout, strings.TrimSpace(string(body)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// rootMarkerPath is the file whose content identifies whose root a command is
// running in. It lives under /etc/dada, which exists in no box's host.
const rootMarkerPath = "etc/dada/root-marker"

// sessionMarkerPath is the root-session equivalent. It is a DIFFERENT file from
// rootMarkerPath on purpose: the box's marker is part of the box's userland and is
// therefore carried by crystallization, so a session opened over the VM root must
// not overwrite a file the manifest comparison just checked.
const sessionMarkerPath = "etc/dada/root-session-marker"

// writeRootMarker stamps a tree with the identity a command inside it must read.
func writeRootMarker(root, relPath, id string) error {
	p := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(id+"\n"), 0o644)
}

// probeInsideBox runs one command in the box and requires the root marker to come
// back with the box's own ref.
func (r *LocalRuntime) probeInsideBox(ctx context.Context, inst *Instance) error {
	res, err := r.Run(ctx, inst, "cat /"+rootMarkerPath)
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(res.Stdout); got != inst.InstanceRef {
		return fmt.Errorf("box: exec landed in root %q, expected the box %q", got, inst.InstanceRef)
	}
	return nil
}

// initPID returns the box's supervised init pid, reading the pidfile when the
// process is not in this process's map (a control-plane restart).
func (r *LocalRuntime) initPID(ref string) (int, error) {
	r.mu.Lock()
	bi, ok := r.inits[ref]
	r.mu.Unlock()
	if ok {
		return bi.pid, nil
	}
	raw, err := os.ReadFile(filepath.Join(r.BoxDir(ref), "init.pid"))
	if err != nil {
		return 0, fmt.Errorf("box: no live init for %s: %w", ref, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("box: malformed init pid for %s: %w", ref, err)
	}
	if _, err := os.Stat("/proc/" + strconv.Itoa(pid) + "/ns/mnt"); err != nil {
		return 0, fmt.Errorf("box: init %d for %s is gone", pid, ref)
	}
	return pid, nil
}

// --- BoxRuntime --------------------------------------------------------------

// Bind attaches tenant identity to an already-running, quarantined instance: it
// writes the box's hostname, the caller's public key, and the identity and env
// files the box's own tooling reads.
//
// It is a write into a body that already exists, which is the whole point of the
// warm pool: nothing here starts a machine.
func (r *LocalRuntime) Bind(ctx context.Context, inst *Instance, spec Spec) error {
	root := r.RootFS(inst.InstanceRef)
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("box: instance %s has no rootfs: %w", inst.InstanceRef, err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/hostname"), []byte(inst.InstanceRef+"\n"), 0o644); err != nil {
		return fmt.Errorf("box: write hostname: %w", err)
	}
	if spec.SSHPublicKey != "" {
		// A PUBLIC key: the caller keeps the private half, so the platform stores
		// no customer credential at all. Nothing here needs scrubbing later.
		ak := filepath.Join(root, "root/.ssh/authorized_keys")
		if err := os.MkdirAll(filepath.Dir(ak), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(ak, []byte(strings.TrimSpace(spec.SSHPublicKey)+"\n"), 0o600); err != nil {
			return fmt.Errorf("box: write authorized_keys: %w", err)
		}
	}
	identity := map[string]string{
		"instance_ref": inst.InstanceRef,
		"image":        spec.Image,
		"profile":      spec.Profile,
		"region":       spec.Region,
		"bound_at":     r.Clock.Now().UTC().Format(time.RFC3339Nano),
	}
	blob, _ := json.MarshalIndent(identity, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "etc/dada/identity.json"), append(blob, '\n'), 0o644); err != nil {
		return fmt.Errorf("box: write identity: %w", err)
	}
	env := map[string]string{
		"BOX_INSTANCE_REF": inst.InstanceRef,
		"BOX_IMAGE":        spec.Image,
		"BOX_PROFILE":      spec.Profile,
		"HOME":             "/root",
		"PATH":             boxPATH(),
	}
	for k, v := range spec.Env {
		env[k] = v
	}
	return r.WriteEnv(ctx, inst, env)
}

// boxPATH builds the box's PATH so the warm toolchain is genuinely reachable.
//
// It starts from the standard directories and then adds every entry of the HOST's
// PATH that lives under one of the read-only bind mounts, because that is where
// this adapter's toolchain actually is: go under /usr/local/go/bin, node often
// under /opt. Entries outside those trees are dropped — they do not exist inside
// the box, so keeping them would be a PATH that lies.
//
// It matters more than tidiness. The readiness canary reports `go=$(go version
// 2>&1)`, and `2>&1` means a MISSING binary still yields a non-empty field: the
// canary would pass with the text "go: not found" as its answer, and the platform
// would ship a box whose promised toolchain is absent while every check is green.
// The honest fix is to make the tool present, not to loosen the check.
func boxPATH() string {
	base := []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"}
	seen := map[string]bool{}
	for _, p := range base {
		seen[p] = true
	}
	shared := map[string]bool{}
	for _, d := range sharedSystemDirs {
		shared[d] = true
	}
	var extra []string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" || seen[entry] {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(entry, "/"), "/")
		if len(parts) == 0 || !shared[parts[0]] {
			continue
		}
		if _, err := os.Stat(entry); err != nil {
			continue
		}
		seen[entry] = true
		extra = append(extra, entry)
	}
	return strings.Join(append(extra, base...), ":")
}

// ProgramNetwork moves the instance out of quarantine into tenant egress.
//
// On this adapter that is a real, observable state change and not a stub: a warm
// instance has no /etc/resolv.conf and no /etc/hosts, so name resolution inside
// it genuinely fails; this method writes both plus the egress allow-list the box
// records for itself. localruntime_test.go asserts the before/after difference,
// so the quarantine is a property of the code rather than a claim in a comment.
//
// What it is NOT: packet-level isolation. See the type's doc comment — the box
// shares the host's network namespace here, and default-deny egress is a
// NetworkPolicy in the production adapter.
func (r *LocalRuntime) ProgramNetwork(ctx context.Context, inst *Instance) error {
	root := r.RootFS(inst.InstanceRef)
	resolv, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		resolv = []byte("nameserver 127.0.0.53\n")
	}
	if err := os.WriteFile(filepath.Join(root, "etc/resolv.conf"), resolv, 0o644); err != nil {
		return fmt.Errorf("box: write resolv.conf: %w", err)
	}
	hosts := "127.0.0.1\tlocalhost " + inst.InstanceRef + "\n::1\tlocalhost ip6-localhost ip6-loopback\n"
	if err := os.WriteFile(filepath.Join(root, "etc/hosts"), []byte(hosts), 0o644); err != nil {
		return fmt.Errorf("box: write hosts: %w", err)
	}
	note := "# egress allow-list recorded by the control plane at claim time.\n" +
		"# LocalRuntime does not enforce it: it shares the host network namespace.\n" +
		"# The production adapter enforces the same list as a NetworkPolicy (ADR-019).\n" +
		"allow dns\nallow https\nallow attached-managed-resources\n"
	return os.WriteFile(filepath.Join(root, "etc/dada/egress.allow"), []byte(note), 0o644)
}

// Unfreeze thaws the instance and waits for its exec channel to accept.
//
// A warm instance's init is already up, so this restarts it only if it died, and
// then blocks on a real command returning success — never on a TCP accept and
// never on a sleep. "SSH accepted TCP" is precisely the check that passes for a
// listener that then hangs in PAM (see readiness.go).
func (r *LocalRuntime) Unfreeze(ctx context.Context, inst *Instance) error {
	if _, err := r.initPID(inst.InstanceRef); err != nil {
		if err := r.startInit(ctx, inst); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(r.ReadyTimeout)
	for {
		err := r.probeInsideBox(ctx, inst)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("box: exec channel did not accept inside the box within %s: %v", r.ReadyTimeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Exec runs one command inside the box and returns its exit status.
//
// GuestClaimedAt is filled in from the box's own clock and is deliberately never
// consulted by the ready path — it exists so the two clocks can be compared in a
// log, not so a measurement can be taken with the wrong one (see CanaryResult).
func (r *LocalRuntime) Exec(ctx context.Context, inst *Instance, cmd string) (CanaryResult, error) {
	res, err := r.Run(ctx, inst, cmd+`; __rc=$?; echo "dada-guest-clock=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)" >&2; exit $__rc`)
	if err != nil {
		return CanaryResult{}, err
	}
	out := CanaryResult{ExitCode: res.ExitCode, Stdout: res.Stdout}
	if ts := parseGuestClock(res.Stderr); !ts.IsZero() {
		out.GuestClaimedAt = ts
	}
	return out, nil
}

func parseGuestClock(stderr string) time.Time {
	for _, line := range strings.Split(stderr, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "dada-guest-clock="); ok {
			if ts, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return ts
			}
		}
	}
	return time.Time{}
}

// Destroy releases the instance and its disk.
//
// It kills the box's init, which is PID 1 of the box's PID namespace, so the
// kernel reaps everything else inside it — a background server started by the
// tenant cannot outlive its box. The tree and the volume store are then removed.
func (r *LocalRuntime) Destroy(ctx context.Context, inst *Instance) error {
	if pid, err := r.initPID(inst.InstanceRef); err == nil {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
		for i := 0; i < 50; i++ {
			if _, err := os.Stat("/proc/" + strconv.Itoa(pid) + "/ns/mnt"); err != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	r.mu.Lock()
	delete(r.inits, inst.InstanceRef)
	r.mu.Unlock()
	// The volume store lives outside the tree, so a Destroy that removed only the
	// tree would silently keep the tenant's data on the host.
	if err := os.RemoveAll(r.volumeDir(inst.InstanceRef)); err != nil {
		return err
	}
	return os.RemoveAll(r.BoxDir(inst.InstanceRef))
}

// --- richer local surface ----------------------------------------------------

// RunResult is one command's full outcome inside a box.
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// execPrelude sources the box's env before the caller's command, so DATABASE_URL
// injected by an attach is present in the very next exec without the caller
// re-reading anything. `set -a` is what exports it to child processes.
const execPrelude = `set -a
[ -r /etc/dada/box.env ] && . /etc/dada/box.env
set +a
cd /srv/app 2>/dev/null || cd /
`

// Run executes a shell command inside the box and captures both streams.
func (r *LocalRuntime) Run(ctx context.Context, inst *Instance, script string) (RunResult, error) {
	pid, err := r.initPID(inst.InstanceRef)
	if err != nil {
		return RunResult{}, err
	}
	cmd := exec.CommandContext(ctx, "nsenter",
		"-t", strconv.Itoa(pid), "-m", "-p", "-u", "-i", "-r", "-w",
		"/bin/sh", "-c", execPrelude+script)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	res := RunResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if runErr == nil {
		return res, nil
	}
	var ee *exec.ExitError
	if ok := asExitError(runErr, &ee); ok {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, fmt.Errorf("box: exec inside %s: %w", inst.InstanceRef, runErr)
}

func asExitError(err error, target **exec.ExitError) bool {
	for err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			*target = ee
			return true
		}
		un, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = un.Unwrap()
	}
	return false
}

// StartService starts a supervised long-running process inside the box and
// records its descriptor in the box's own filesystem.
//
// The descriptor is written INSIDE the box, at /etc/dada/services/<name>.json,
// on purpose: ADR-019 makes the frozen box the source of truth for
// crystallization, so the entrypoint the systemd unit is rendered from has to be
// discoverable by reading the box, not by consulting a control-plane table that
// could disagree with it.
func (r *LocalRuntime) StartService(ctx context.Context, inst *Instance, name, command, workdir string, ports []int) error {
	if workdir == "" {
		workdir = "/srv/app"
	}
	desc := ServiceDescriptor{Name: name, Command: command, WorkingDir: workdir, Ports: ports}
	blob, err := json.MarshalIndent(desc, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(r.RootFS(inst.InstanceRef), "etc/dada/services", name+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o644); err != nil {
		return err
	}
	return r.startDescriptor(ctx, func(script string) (RunResult, error) { return r.Run(ctx, inst, script) }, desc)
}

// ServicePIDFile is where a supervised service records its in-box pid. It lives
// under /run, which is a tmpfs inside the box and one of ADR-019's machine-owned
// exclusions, so a pidfile can never be crystallized and mistaken for a live pid
// on the VM.
func ServicePIDFile(name string) string { return "/run/dada-service-" + name + ".pid" }

// startDescriptor launches one descriptor through an arbitrary exec function, so
// the same rendering starts a service inside a box and inside a crystallized VM
// root. One code path means the process the VM restarts is started the same way
// the box started it, which is the claim ADR-019 makes about carried processes.
func (r *LocalRuntime) startDescriptor(_ context.Context, run func(string) (RunResult, error), desc ServiceDescriptor) error {
	logPath := "/var/log/" + desc.Name + ".log"
	inner := fmt.Sprintf("echo $$ > %s; exec %s", ServicePIDFile(desc.Name), desc.Command)
	start := fmt.Sprintf("mkdir -p /var/log /run && cd %s && setsid /bin/sh -c %s >%s 2>&1 </dev/null & echo started",
		shellQuote(desc.WorkingDir), shellQuote(inner), shellQuote(logPath))
	res, err := run(start)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("box: start service %s: exit %d: %s", desc.Name, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// StopServices stops every supervised service inside the box and reports which
// ones were running.
//
// This is ADR-019's freeze: the box's PROCESSES stop, the box's FILESYSTEM stays
// whole and stays the source of truth until the fixation point. Killing the box's
// namespace instead would also stop the processes, and would also make the tree
// unreadable through the box — so the manifest would have to be taken from the
// host's view of the tree without the box being able to disagree.
func (r *LocalRuntime) StopServices(ctx context.Context, inst *Instance) ([]string, error) {
	descs, err := r.Services(inst.InstanceRef)
	if err != nil {
		return nil, err
	}
	var stopped []string
	for _, d := range descs {
		pf := ServicePIDFile(d.Name)
		script := fmt.Sprintf(`if [ -r %s ]; then kill -TERM "$(cat %s)" 2>/dev/null && echo stopped || echo gone; else echo absent; fi`,
			shellQuote(pf), shellQuote(pf))
		res, err := r.Run(ctx, inst, script)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(res.Stdout) == "stopped" {
			stopped = append(stopped, d.Name)
		}
	}
	return stopped, nil
}

// ServiceDescriptor is a long-running process the box runs, as recorded inside
// the box. Crystallization renders one systemd unit per descriptor.
type ServiceDescriptor struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir"`
	Ports      []int  `json:"ports"`
}

// Services reads the service descriptors out of the box's filesystem.
func (r *LocalRuntime) Services(ref string) ([]ServiceDescriptor, error) {
	dir := filepath.Join(r.RootFS(ref), "etc/dada/services")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ServiceDescriptor
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var d ServiceDescriptor
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("box: malformed service descriptor %s: %w", e.Name(), err)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// WriteEnv merges kv into the box's env file, 0600 and root-owned.
//
// Merge rather than replace because attaches happen one at a time and mid-flight:
// a second attach must not withdraw the first one's credential.
func (r *LocalRuntime) WriteEnv(ctx context.Context, inst *Instance, kv map[string]string) error {
	path := filepath.Join(r.RootFS(inst.InstanceRef), BoxEnvPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	current, err := r.EnvSnapshot(inst.InstanceRef)
	if err != nil {
		return err
	}
	for k, v := range kv {
		current[k] = v
	}
	return WriteEnvFile(path, current)
}

// envFileHeader is the first line of every box env file.
//
// It is a shared constant because crystallization compares the box's env file
// against the VM's by file manifest, on (path, size, mode, sha256). Two renderers
// with two different comment lines would produce a byte difference for identical
// content — a verification failure with no cause, which teaches an operator to
// ignore the check. One renderer, one header.
const envFileHeader = "# Dada Box env. 0600, root. Written out of band, never through git.\n"

// RenderEnvFile renders an env map to the exact bytes a box env file contains.
//
// Values are single-quoted so sourcing the file cannot execute them; keys are
// sorted so the same map always produces the same bytes, which is what makes the
// manifest comparison meaningful.
func RenderEnvFile(kv map[string]string) string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(envFileHeader)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(shellQuote(kv[k]))
		b.WriteString("\n")
	}
	return b.String()
}

// WriteEnvFile writes an env map to path at mode 0600.
//
// The mode is set in the open flags rather than chmod'ed afterwards: a window in
// which the file is world-readable is a window in which an injected credential is
// world-readable, and the chmod at the end is only there for the case where the
// file already existed with a wider mode.
func WriteEnvFile(path string, kv map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(RenderEnvFile(kv)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// BoxEnvPath is where the box's env lives, inside the box. ADR-019 step 5 writes
// the same path on the crystallized VM and points EnvironmentFile at it.
const BoxEnvPath = "etc/dada/box.env"

// EnvSnapshot reads the box's env file into a map. Missing file is not an error:
// a fresh instance legitimately has none.
func (r *LocalRuntime) EnvSnapshot(ref string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(filepath.Join(r.RootFS(ref), BoxEnvPath))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = shellUnquote(strings.TrimSpace(v))
	}
	return out, sc.Err()
}

// shellQuote single-quotes a value so sourcing the env file cannot execute it.
func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func shellUnquote(v string) string {
	if len(v) >= 2 && strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") {
		return strings.ReplaceAll(v[1:len(v)-1], `'\''`, "'")
	}
	return v
}

// --- root sessions (used by crystallization) ---------------------------------

// RootSession is a live mount/PID/UTS/IPC namespace over an arbitrary root
// directory, with the same assembly the box's own init performs.
//
// It exists so the crystallized VM's root can be entered and run in exactly the
// way the box's root is: same bind mounts, same /proc, same /dev. If the VM side
// were exercised through a different mechanism, an equality check between the two
// would be comparing two different things and would prove nothing.
type RootSession struct {
	pid  int
	cmd  *exec.Cmd
	root string
	rt   *LocalRuntime
}

// OpenRoot starts a namespace init over root and waits for it to accept commands.
func (r *LocalRuntime) OpenRoot(ctx context.Context, root, hostname string, mounts []string) (*RootSession, error) {
	for _, d := range append(append([]string{}, sharedSystemDirs...), machineOwnedDirs...) {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, err
		}
	}
	// Same identity marker as a box, for the same reason: until the init reaches
	// its chroot, nsenter --root still lands on the HOST's root, and a probe that
	// only asked "did a command run" would report a VM that is really the host.
	sessionID := "rootsession-" + filepath.Base(filepath.Dir(root)) + "-" + hostname
	if err := writeRootMarker(root, sessionMarkerPath, sessionID); err != nil {
		return nil, err
	}
	cmd := exec.Command("/bin/sh", "-c", initScript, "root-session", root, hostname, strings.Join(mounts, ","))
	attr, err := newNamespaceSysProcAttr()
	if err != nil {
		return nil, err
	}
	cmd.SysProcAttr = attr
	var log strings.Builder
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("box: start root session over %s: %w", root, err)
	}
	go func() { _ = cmd.Wait() }()
	s := &RootSession{pid: cmd.Process.Pid, cmd: cmd, root: root, rt: r}
	deadline := time.Now().Add(r.ReadyTimeout)
	for {
		if res, err := s.Run(ctx, "cat /"+sessionMarkerPath); err == nil &&
			res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == sessionID {
			return s, nil
		}
		if time.Now().After(deadline) {
			_ = s.Close()
			return nil, fmt.Errorf("box: root session over %s did not accept within %s: %s",
				root, r.ReadyTimeout, strings.TrimSpace(log.String()))
		}
		select {
		case <-ctx.Done():
			_ = s.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Run executes a shell command inside the session's root.
func (s *RootSession) Run(ctx context.Context, script string) (RunResult, error) {
	cmd := exec.CommandContext(ctx, "nsenter",
		"-t", strconv.Itoa(s.pid), "-m", "-p", "-u", "-i", "-r", "-w",
		"/bin/sh", "-c", execPrelude+script)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	res := RunResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if runErr == nil {
		return res, nil
	}
	var ee *exec.ExitError
	if asExitError(runErr, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, fmt.Errorf("box: exec inside root session %s: %w", s.root, runErr)
}

// StartService starts one descriptor inside the session — the restart-once step
// of ADR-019, run through the same renderer the box used.
func (s *RootSession) StartService(ctx context.Context, desc ServiceDescriptor) error {
	return s.rt.startDescriptor(ctx, func(script string) (RunResult, error) { return s.Run(ctx, script) }, desc)
}

// ListeningPorts reports which of ports accept TCP from inside the session.
func (s *RootSession) ListeningPorts(ctx context.Context, ports []int) ([]int, error) {
	return probePorts(ports, func(script string) (RunResult, error) { return s.Run(ctx, script) })
}

// Close tears the session's namespaces down.
func (s *RootSession) Close() error {
	if s == nil || s.pid == 0 {
		return nil
	}
	_ = syscall.Kill(-s.pid, syscall.SIGKILL)
	_ = syscall.Kill(s.pid, syscall.SIGKILL)
	for i := 0; i < 50; i++ {
		if _, err := os.Stat("/proc/" + strconv.Itoa(s.pid) + "/ns/mnt"); err != nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

// ListeningPorts reports which of ports are accepting TCP inside the box, by
// connecting to each one from inside the box.
//
// Dialing rather than parsing /proc/net/tcp, and deliberately: this adapter
// shares the host's network namespace, so /proc/net/tcp inside the box lists the
// whole host and a set built from it would be a set about the host, not the box.
// A connect that completes is evidence about the port; a line in a table shared
// with the host is not.
func (r *LocalRuntime) ListeningPorts(ctx context.Context, inst *Instance, ports []int) ([]int, error) {
	return probePorts(ports, func(script string) (RunResult, error) { return r.Run(ctx, inst, script) })
}

func probePorts(ports []int, run func(string) (RunResult, error)) ([]int, error) {
	listening := []int{}
	for _, p := range ports {
		script := fmt.Sprintf(
			`python3 -c 'import socket,sys; s=socket.socket(); s.settimeout(1.0); sys.exit(s.connect_ex(("127.0.0.1",%d)))'`, p)
		res, err := run(script)
		if err != nil {
			return nil, err
		}
		if res.ExitCode == 0 {
			listening = append(listening, p)
		}
	}
	sort.Ints(listening)
	return listening, nil
}
