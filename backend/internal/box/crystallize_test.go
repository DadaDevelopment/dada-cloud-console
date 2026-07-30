package box

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Crystallization tests.
//
// Same gate as the LocalRuntime tests: they really rsync a real tree onto a real
// second root and really re-bind a real port, because the failure mode ADR-019
// warns about is a verification that passes without having verified anything. A
// version of these tests that ran without those things would be the same bug in
// test form.

// TestCrystallizeMaterializesTheUserlandAndVerifiesIt is the whole ADR-019
// mechanism in one test: freeze, provision, apply the userland, restore the
// volume, write the env, render the unit, restart once, then verify by manifest,
// socket set and probe.
func TestCrystallizeMaterializesTheUserlandAndVerifiesIt(t *testing.T) {
	rt := requireLocalRuntime(t)
	if _, err := os.Stat("/usr/bin/rsync"); err != nil {
		t.Skip("rsync is required: the materialization is an rsync, and faking it would test nothing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	inst := readyBoxFixture(t, rt, ctx)
	port := freeLocalPort(t)

	// Content written INSIDE the box, so the manifest comparison is over something
	// the box produced rather than over the template both sides started from.
	marker := "crystallize-test-" + inst.InstanceRef
	if _, err := rt.Run(ctx, inst, "mkdir -p /srv/app && printf '%s\\n' '"+marker+"' > /srv/app/marker.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(ctx, inst, "printf '%s\\n' 'volume-"+marker+"' > /data/notes.txt"); err != nil {
		t.Fatal(err)
	}
	app := `import http.server, socketserver, pathlib
M = pathlib.Path("/srv/app/marker.txt").read_text().strip()
V = pathlib.Path("/data/notes.txt").read_text().strip()
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(s):
        b = ("%s|%s" % (M, V)).encode()
        s.send_response(200); s.send_header("Content-Length", str(len(b))); s.end_headers(); s.wfile.write(b)
    def log_message(s, *a): pass
socketserver.TCPServer.allow_reuse_address = True
socketserver.TCPServer(("127.0.0.1", PORT), H).serve_forever()
`
	app = strings.Replace(app, "PORT", itoa(port), 1)
	if err := os.WriteFile(filepath.Join(rt.RootFS(inst.InstanceRef), "srv/app/server.py"), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rt.StartService(ctx, inst, "demo", "python3 /srv/app/server.py", "/srv/app", []int{port}); err != nil {
		t.Fatalf("StartService: %v", err)
	}
	if got, err := rt.ListeningPorts(ctx, inst, []int{port}); err != nil || len(got) != 1 {
		t.Fatalf("the demo service did not come up inside the box: %v %v", got, err)
	}

	cz := &LocalCrystallizer{Runtime: rt, Clock: SystemClock{}}
	rep, err := cz.CrystallizeWithReport(ctx, inst, CrystallizeOptions{
		VMName: "test-vm", Domain: "test-vm.example.invalid", OSSlug: WarmImageOSSlug,
	})
	if rep != nil {
		t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(rep.VMRoot)) })
	}
	if err != nil {
		if rep != nil {
			t.Log("\n" + rep.Text())
		}
		t.Fatalf("CrystallizeWithReport: %v", err)
	}

	if !rep.Manifest.Equal {
		t.Errorf("manifest not equal: missing=%v mismatched=%v", rep.Manifest.MissingOnVM, rep.Manifest.Mismatched)
	}
	if rep.Manifest.BoxFiles == 0 {
		t.Error("an empty manifest compares equal trivially; the box userland set must not be empty")
	}
	if !rep.Sockets.Equal {
		t.Errorf("socket set not equal: before=%v after=%v", rep.Sockets.ListeningBeforeFreeze, rep.Sockets.ListeningAfterCutover)
	}
	if !rep.Probe.OK {
		t.Errorf("the end-to-end probe did not return 200: %+v", rep.Probe)
	}
	// The probe body proves the VM served the BOX's content, not something else
	// that happened to hold the port.
	if !strings.Contains(rep.Probe.Body, marker) || !strings.Contains(rep.Probe.Body, "volume-"+marker) {
		t.Errorf("the crystallized VM's response does not carry the box's marker and volume: %q", rep.Probe.Body)
	}
	if !rep.Env.Equal || rep.Env.Mode != "0600" {
		t.Errorf("env not carried correctly: equal=%t mode=%s mismatched=%v", rep.Env.Equal, rep.Env.Mode, rep.Env.Mismatched)
	}
	for kind, disp := range rep.Carry {
		if disp == CarryLost {
			t.Errorf("carry manifest reports %q lost", kind)
		}
	}
	for path, intact := range rep.VMOwnArtifacts {
		if !intact {
			t.Errorf("the VM lost its own %s; only the userland may be materialized (ADR-019)", path)
		}
	}
	for path, leaked := range rep.BoxSentinels {
		if leaked {
			t.Errorf("box machine-owned file %s reached the VM; the exclusion list did not hold", path)
		}
	}
	if len(rep.Units) != 1 || !strings.Contains(rep.Units[0].Content, "EnvironmentFile=/etc/dada/box.env") {
		t.Errorf("the rendered unit does not reference the out-of-band env file: %+v", rep.Units)
	}
	if strings.Contains(rep.Units[0].Content, "docker") && !strings.Contains(rep.Units[0].Content, "No Docker") {
		t.Error("the rendered unit mentions docker as a dependency; a crystallized VM is not a container host")
	}
}

// TestCrystallizeRefusesADistributionMismatch pins ADR-019 §3 as a mechanism.
// Materializing one distribution's userland onto another's kernel produces a
// chimera that assembles and then breaks in production, so this is a hard refusal
// rather than a warning.
func TestCrystallizeRefusesADistributionMismatch(t *testing.T) {
	rt := requireLocalRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	inst := readyBoxFixture(t, rt, ctx)

	cz := &LocalCrystallizer{Runtime: rt, Clock: SystemClock{}}
	_, err := cz.CrystallizeWithReport(ctx, inst, CrystallizeOptions{
		VMName: "mismatch-vm", OSSlug: "debian-12",
	})
	if err == nil {
		t.Fatal("crystallizing an ubuntu-24-04 box onto a debian-12 slug must be refused")
	}
	if !strings.Contains(err.Error(), "does not match the VM slug") {
		t.Errorf("the refusal must name the mismatch, got: %v", err)
	}
}

// TestUserlandExclusionsAreExactlyTheADRList pins the fixed list against the ADR.
// It is a variable the report prints rather than a constant nobody sees, and this
// test is what stops it drifting quietly.
func TestUserlandExclusionsAreExactlyTheADRList(t *testing.T) {
	want := []string{
		"/proc", "/sys", "/dev", "/run", "/tmp", "/boot", "/lib/modules",
		"/etc/fstab", "/etc/machine-id", "/etc/hostname", "/etc/resolv.conf", "/etc/hosts",
	}
	if len(ADRUserlandExclusions) != len(want) {
		t.Fatalf("the exclusion list has %d entries, ADR-019 names %d: %v",
			len(ADRUserlandExclusions), len(want), ADRUserlandExclusions)
	}
	for i := range want {
		if ADRUserlandExclusions[i] != want[i] {
			t.Errorf("exclusion %d = %q, ADR-019 names %q", i, ADRUserlandExclusions[i], want[i])
		}
	}
}

// TestExclusionSetMatchesOnDirectoryBoundaries pins the matcher against the
// mistake that would silently drop real userland: a prefix match that is not
// anchored on a path separator excludes /etc/hostsomething along with /etc/hosts.
func TestExclusionSetMatchesOnDirectoryBoundaries(t *testing.T) {
	excl := newExclusionSet([]string{"/etc/hosts", "/boot"})
	for _, p := range []string{"/etc/hosts", "/boot", "/boot/grub/grub.cfg"} {
		if !excl.excluded(p) {
			t.Errorf("%q must be excluded", p)
		}
	}
	for _, p := range []string{"/etc/hostsomething", "/etc/hostname", "/bootstrap.sh", "/srv/boot"} {
		if excl.excluded(p) {
			t.Errorf("%q must NOT be excluded: an unanchored prefix match would drop real userland", p)
		}
	}
}

// readyBoxFixture claims one warm box through the real ready path.
func readyBoxFixture(t *testing.T, rt *LocalRuntime, ctx context.Context) *Instance {
	t.Helper()
	pool := NewMemoryPool()
	if err := rt.Warm(ctx, pool, "warm-v1", "", 1); err != nil {
		t.Fatalf("warm the pool: %v", err)
	}
	res, err := Spawn(ctx, Deps{Clock: SystemClock{}, Admit: AllowAll{}, Pool: pool, Runtime: rt},
		Spec{Image: "warm-v1", Profile: "box-standard"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	return res.Instance
}

// freeLocalPort reserves a port by binding it, then releases it. Asking the kernel
// rather than picking a constant: a hardcoded port that something else already
// holds turns the socket comparison into a check on that other process.
func freeLocalPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}
