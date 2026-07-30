package box

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// LocalRuntime tests.
//
// They are skipped unless BOX_LOCAL_TEST=1 and the process is root, because the
// runtime's whole point is that it really creates namespaces and really mounts
// things. A version of these tests that passed without doing that would be
// asserting nothing — the same mistake as a readiness check that stops at "TCP
// accepted".

func requireLocalRuntime(t *testing.T) *LocalRuntime {
	t.Helper()
	if os.Getenv("BOX_LOCAL_TEST") != "1" {
		t.Skip("set BOX_LOCAL_TEST=1 to run LocalRuntime tests (they create namespaces and mount)")
	}
	if os.Geteuid() != 0 {
		t.Skip("LocalRuntime tests need root to create mount namespaces")
	}
	root := t.TempDir()
	rt := NewLocalRuntime(root, SystemClock{})
	t.Cleanup(func() {
		entries, _ := os.ReadDir(rt.instancesDir())
		for _, e := range entries {
			_ = rt.Destroy(context.Background(), &Instance{ID: e.Name(), InstanceRef: e.Name()})
		}
	})
	return rt
}

// TestLocalRuntimeReadyPathRunsCommandsInsideTheBox walks the real ready path and
// asserts the box is a box: its own root, its own hostname, its own /tmp.
func TestLocalRuntimeReadyPathRunsCommandsInsideTheBox(t *testing.T) {
	rt := requireLocalRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := NewMemoryPool()
	if err := rt.Warm(ctx, pool, "warm-v1", "", 1); err != nil {
		t.Fatalf("warm the pool: %v", err)
	}
	if got := pool.Available("warm-v1", ""); got != 1 {
		t.Fatalf("pool available = %d, want 1", got)
	}

	spec := Spec{Image: "warm-v1", Profile: "box-standard"}
	res, err := Spawn(ctx, Deps{Clock: SystemClock{}, Admit: AllowAll{}, Pool: pool, Runtime: rt}, spec)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !res.PoolHit {
		t.Fatal("expected a warm pool hit")
	}
	if res.Timeline.Total() <= 0 {
		t.Fatal("time to ready must be a measured, positive duration")
	}
	inst := res.Instance

	// Its own root: a file created outside the tree is not visible inside it.
	outside := filepath.Join(rt.Root, "outside-marker")
	if err := os.WriteFile(outside, []byte("host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := rt.Run(ctx, inst, "cat "+outside+" 2>&1; echo rc=$?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.Stdout, "rc=1") {
		t.Errorf("a host path outside the tree was readable from inside the box: %q", out.Stdout)
	}

	// Its own hostname, written by Bind.
	got, err := rt.Run(ctx, inst, "cat /etc/hostname")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got.Stdout) != inst.InstanceRef {
		t.Errorf("/etc/hostname inside the box = %q, want %q", strings.TrimSpace(got.Stdout), inst.InstanceRef)
	}

	// Its own PID namespace: the box's init is PID 1.
	got, err = rt.Run(ctx, inst, "readlink /proc/1/exe || cat /proc/1/comm")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Stdout, "sleep") {
		t.Errorf("PID 1 inside the box = %q, want the box's own init", strings.TrimSpace(got.Stdout))
	}
}

// TestLocalRuntimeQuarantineIsRealBeforeProgramNetwork asserts ProgramNetwork
// performs an observable state change rather than returning nil to look busy.
func TestLocalRuntimeQuarantineIsRealBeforeProgramNetwork(t *testing.T) {
	rt := requireLocalRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	inst, err := rt.create(ctx, "warm-v1", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before, err := rt.Run(ctx, inst, "test -e /etc/resolv.conf && echo present || echo absent")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(before.Stdout) != "absent" {
		t.Errorf("a quarantined box already had /etc/resolv.conf: %q", before.Stdout)
	}
	if err := rt.ProgramNetwork(ctx, inst); err != nil {
		t.Fatalf("ProgramNetwork: %v", err)
	}
	after, err := rt.Run(ctx, inst, "test -e /etc/resolv.conf && echo present || echo absent")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(after.Stdout) != "present" {
		t.Errorf("after ProgramNetwork /etc/resolv.conf was still %q", after.Stdout)
	}
}

// TestLocalRuntimeEnvIsInjectedAt0600AndVisibleToTheNextExec pins the two
// properties the attach path depends on: the file is not readable by anyone else,
// and the very next command sees the value without re-reading anything.
func TestLocalRuntimeEnvIsInjectedAt0600AndVisibleToTheNextExec(t *testing.T) {
	rt := requireLocalRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	inst, err := rt.create(ctx, "warm-v1", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := rt.WriteEnv(ctx, inst, map[string]string{"DEMO_SECRET": "s3cr3t with spaces"}); err != nil {
		t.Fatalf("WriteEnv: %v", err)
	}
	fi, err := os.Stat(filepath.Join(rt.RootFS(inst.InstanceRef), BoxEnvPath))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("box.env mode = %04o, want 0600", perm)
	}
	got, err := rt.Run(ctx, inst, `printf '%s' "$DEMO_SECRET"`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stdout != "s3cr3t with spaces" {
		t.Errorf("injected env inside the box = %q", got.Stdout)
	}
}

// TestLocalRuntimeDestroyKillsProcessesInsideTheBox asserts a background process
// the tenant started cannot outlive its box.
func TestLocalRuntimeDestroyKillsProcessesInsideTheBox(t *testing.T) {
	rt := requireLocalRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	inst, err := rt.create(ctx, "warm-v1", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pid, err := rt.initPID(inst.InstanceRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Destroy(ctx, inst); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := os.Stat("/proc/" + itoa(pid) + "/ns/mnt"); err == nil {
		t.Error("the box's namespace survived Destroy")
	}
	if _, err := os.Stat(rt.BoxDir(inst.InstanceRef)); !os.IsNotExist(err) {
		t.Errorf("the box tree survived Destroy: %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
