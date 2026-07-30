package box

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A runtime with no broker must SAY so rather than behave as if it had one. The
// failure this guards against is not a crash: it is a box that comes up, publishes
// a control-plane URL as its own endpoint, and quietly routes the customer's work
// through us (D6).
func TestARuntimeWithoutABrokerBinaryReportsIt(t *testing.T) {
	r := NewLocalRuntime(t.TempDir(), SystemClock{})

	if r.BrokerConfigured() {
		t.Fatal("BrokerConfigured is true with no BrokerDir set")
	}
	if _, err := r.StartBroker(t.Context(), &Instance{InstanceRef: "box-x"}, "box-x"); !errors.Is(err, ErrNoBroker) {
		t.Fatalf("StartBroker error = %v, want ErrNoBroker", err)
	}

	// A directory that exists but holds no binary is the same answer. This is the
	// realistic misconfiguration — an operator sets BOX_BROKER_DIR to a path the
	// build did not write to — and treating it as configured would produce boxes
	// whose door fails to start one at a time, in production.
	dir := t.TempDir()
	r.BrokerDir = dir
	if r.BrokerConfigured() {
		t.Fatalf("BrokerConfigured is true for %s, which contains no %s", dir, BrokerBinaryName)
	}

	if err := os.WriteFile(filepath.Join(dir, BrokerBinaryName), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !r.BrokerConfigured() {
		t.Fatal("BrokerConfigured is false with the binary in place")
	}
}

// What may be written into the box's digest file, and what may not. The file is the
// box's whole notion of who may open it, so anything that is not a live credential
// reaching it is either a hole or a lockout.
func TestInstallSessionDigestsRefusesAnythingThatIsNotALiveCredential(t *testing.T) {
	r := NewLocalRuntime(t.TempDir(), SystemClock{})
	// No BrokerDir, so nothing is executed: these cases must all fail on validation
	// before any attempt to enter a box, which is what makes them cheap to assert.
	inst := &Instance{InstanceRef: "box-x"}

	cases := []struct {
		name    string
		digests []SessionDigest
		wantErr string
	}{
		{
			// The one that matters most: a plaintext token in the digest column would
			// be a credential written in the clear into a file inside the box.
			name:    "a plaintext token is not a digest",
			digests: []SessionDigest{{Hash: "dadabox_deadbeef", ExpiresAt: time.Now().Add(time.Hour)}},
			wantErr: "not a sha256 hex digest",
		},
		{
			name:    "a truncated digest",
			digests: []SessionDigest{{Hash: strings.Repeat("a", 63), ExpiresAt: time.Now().Add(time.Hour)}},
			wantErr: "not a sha256 hex digest",
		},
		{
			name:    "64 characters that are not hex",
			digests: []SessionDigest{{Hash: strings.Repeat("z", 64), ExpiresAt: time.Now().Add(time.Hour)}},
			wantErr: "not a sha256 hex digest",
		},
		{
			// A digest with no deadline is a standing credential inside a body that is
			// supposed to be disposable.
			name:    "a digest with no expiry",
			digests: []SessionDigest{{Hash: strings.Repeat("a", 64)}},
			wantErr: "no expiry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := r.InstallSessionDigests(t.Context(), inst, tc.digests)
			if err == nil {
				t.Fatalf("InstallSessionDigests accepted %+v", tc.digests)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// The published URL names the address the broker actually bound. It is asserted
// because the alternative — assembling a URL from a port we assumed — is exactly
// the shape that produces a box reported ready with nothing listening behind it.
func TestBrokerMCPURLNamesTheBoundAddress(t *testing.T) {
	if got, want := BrokerMCPURL("127.0.0.1:34567"), "http://127.0.0.1:34567/mcp"; got != want {
		t.Fatalf("BrokerMCPURL = %q, want %q", got, want)
	}
}

// The broker's whole footprint inside a box is under /run, which is on ADR-019's
// machine-owned exclusion list. That is what keeps a crystallized VM free of both
// the broker binary and a live box credential. A path that drifted out of /run
// would carry a token onto a permanent machine, silently.
func TestTheBrokerLivesOnlyInMachineOwnedSpace(t *testing.T) {
	for _, p := range []string{brokerDirInBox, brokerBinInBox, brokerTokensPath, brokerAddrPath, brokerLogPath} {
		if !strings.HasPrefix(p, "/run/") {
			t.Fatalf("%s is outside /run, so crystallization would carry it into the VM", p)
		}
	}
	// And /run really is on the exclusion list rather than merely believed to be.
	found := false
	for _, d := range machineOwnedDirs {
		if d == "run" {
			found = true
		}
	}
	if !found {
		t.Fatalf("run is not in machineOwnedDirs %v, so the broker's files would be crystallized", machineOwnedDirs)
	}
}
