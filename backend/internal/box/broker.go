package box

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The box's own door, from the runtime's side.
//
// cmd/box-broker is the process; this file is what puts it inside a box, hands it
// the box's credential digests, and reads back the address it actually bound. The
// division is deliberate: the broker knows nothing about the control plane, and the
// control plane learns the broker's address by reading it out of the box rather
// than by assuming a port.
//
// WHERE IT LIVES INSIDE THE BOX, and why that is the whole design:
//
//	/run/dada-broker/bin/box-broker  the binary, bind-mounted read-only from the host
//	/run/dada-broker/tokens          sha256 digests of live sessions, 0600
//	/run/dada-broker/addr            the bound address, written by the broker itself
//	/run/dada-broker/broker.log      its output
//
// All of it under /run, which is tmpfs inside the box and is on ADR-019's
// machine-owned exclusion list. That is not tidiness: it means a crystallized VM
// contains no broker binary and no box credential, which is the correct outcome. A
// permanent VM is not an ephemeral body an agent claims, and carrying a live token
// onto one would quietly extend a box credential past the life of the box it was
// minted for.
//
// The binary is bind-mounted rather than copied because copying an ~8MB executable
// into every box's tmpfs would charge the warm pool real memory per box for a file
// that is byte-identical across all of them. In production the broker is part of the
// warm image and this bind is what stands in for that.

// brokerDirInBox is the broker's directory as seen from inside the box.
const brokerDirInBox = "/run/dada-broker"

// BrokerBinaryName is the file the runtime expects to find in LocalRuntime.BrokerDir.
const BrokerBinaryName = "box-broker"

const (
	// brokerBinInBox is the read-only bind of the host's BrokerDir. Only bin/ is
	// read-only; the directory above it is the box's tmpfs, because the broker has
	// to write the digests, the address and the log somewhere.
	brokerBinInBox   = brokerDirInBox + "/bin"
	brokerTokensPath = brokerDirInBox + "/tokens"
	brokerAddrPath   = brokerDirInBox + "/addr"
	brokerLogPath    = brokerDirInBox + "/broker.log"
)

// ErrNoBroker reports that this runtime was not given a broker binary, so the box
// has no door of its own and the control-plane fallback is the only way in.
//
// It is a typed error rather than a silent no-op because "the box came up without
// its own endpoint" is exactly the condition a caller must be able to say out loud
// in its response. A box that quietly published a control-plane URL as if it were
// the box's own would be the fake-door failure in a new place.
var ErrNoBroker = fmt.Errorf("box: no broker binary configured (set BOX_BROKER_DIR); the box has no endpoint of its own")

// BrokerConfigured reports whether this runtime can give a box its own door.
func (r *LocalRuntime) BrokerConfigured() bool {
	if r.BrokerDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(r.BrokerDir, BrokerBinaryName))
	return err == nil
}

// SessionDigest is one credential the box's door will accept, and until when.
//
// The expiry travels WITH the digest rather than being enforced by the control
// plane pushing an update when a session lapses. That would make the box's door
// depend on a control-plane heartbeat to stop honouring a dead credential — and a
// missed heartbeat would leave a token working on the one path we deliberately are
// not on. Carrying the deadline makes the box able to refuse on its own.
type SessionDigest struct {
	// Hash is the hex sha256 of the token plaintext.
	Hash string
	// ExpiresAt is when the box must stop accepting it, regardless of anything the
	// control plane does or fails to do afterwards.
	ExpiresAt time.Time
}

// InstallSessionDigests writes the box's live session credentials into the box,
// replacing whatever was there.
//
// Replace rather than append, and it is load-bearing: this function is also how a
// revocation lands. The control plane holds the set of live sessions, so the file
// is that set, and a digest that is no longer in the set is gone from the box on
// the next call rather than lingering until something remembers to remove it.
func (r *LocalRuntime) InstallSessionDigests(ctx context.Context, inst *Instance, digests []SessionDigest) error {
	var b strings.Builder
	b.WriteString("# <sha256-of-token> <unix-expiry>. Written by the control plane; the box enforces both.\n")
	for _, d := range digests {
		hash := strings.TrimSpace(d.Hash)
		if hash == "" {
			continue
		}
		// A digest is 64 hex characters and nothing else. Validating here rather
		// than trusting the caller keeps a stray newline or a plaintext token from
		// ever reaching a file the broker reads as a list of digests.
		if !isSHA256Hex(hash) {
			return fmt.Errorf("box: refusing to install %q as a session digest: not a sha256 hex digest", hash)
		}
		if d.ExpiresAt.IsZero() {
			return fmt.Errorf("box: refusing to install session digest %s… with no expiry: a credential the box cannot time out on its own is a standing credential", hash[:8])
		}
		fmt.Fprintf(&b, "%s %d\n", hash, d.ExpiresAt.Unix())
	}
	script := fmt.Sprintf(`mkdir -p %s && chmod 0700 %s && umask 077 && cat > %s <<'DADA_BROKER_TOKENS_EOF'
%sDADA_BROKER_TOKENS_EOF
chmod 0600 %s`, brokerDirInBox, brokerDirInBox, brokerTokensPath, b.String(), brokerTokensPath)

	res, err := r.Run(ctx, inst, script)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("box: installing session digests into %s exited %d: %s",
			inst.InstanceRef, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// RevokeAllSessionDigests empties the box's digest file, which shuts its door
// immediately: the broker re-reads the file per request, so there is no restart and
// no cache between the truncate and the next 401.
func (r *LocalRuntime) RevokeAllSessionDigests(ctx context.Context, inst *Instance) error {
	return r.InstallSessionDigests(ctx, inst, nil)
}

// isSHA256Hex is the only thing this package knows about a token's shape. Hashing
// itself deliberately lives in ONE place — the control plane's hashBoxToken, beside
// where tokens are minted and stored — so there is no second implementation that
// could drift from the digest already in box_sessions.
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// brokerScript starts the broker inside the box. Idempotent: a broker that is
// already listening leaves its address file alone and reports `already`.
const brokerScript = `mkdir -p ` + brokerDirInBox + `
if [ -s ` + brokerAddrPath + ` ] && kill -0 "$(cat ` + brokerDirInBox + `/broker.pid 2>/dev/null)" 2>/dev/null; then
  echo already; exit 0
fi
rm -f ` + brokerAddrPath + `
setsid ` + brokerBinInBox + `/` + BrokerBinaryName + ` >` + brokerLogPath + ` 2>&1 </dev/null &
echo $! > ` + brokerDirInBox + `/broker.pid
echo started
`

// StartBroker launches the box's own endpoint and returns the address it bound.
//
// The address is READ BACK from the box rather than chosen by this process. The
// alternative — pick a free port on the host, pass it in, publish it — has a race
// with every other box being claimed at the same moment, and its failure mode is a
// box that is reported ready with a URL nothing is listening on. Letting the kernel
// pick inside the box and reading the answer out makes "the broker is listening"
// and "the URL we published" the same fact.
func (r *LocalRuntime) StartBroker(ctx context.Context, inst *Instance, boxName string) (string, error) {
	if !r.BrokerConfigured() {
		return "", ErrNoBroker
	}
	env := map[string]string{
		"BOX_BROKER_ADDR":      "127.0.0.1:0",
		"BOX_BROKER_ADDR_FILE": brokerAddrPath,
		"BOX_BROKER_TOKENS":    brokerTokensPath,
		"BOX_NAME":             boxName,
	}
	var prefix strings.Builder
	for k, v := range env {
		prefix.WriteString(fmt.Sprintf("export %s=%q\n", k, v))
	}
	launch, err := r.Run(ctx, inst, prefix.String()+brokerScript)
	if err != nil {
		return "", err
	}
	if launch.ExitCode != 0 {
		return "", fmt.Errorf("box: launching the broker inside %s exited %d: %s %s",
			inst.InstanceRef, launch.ExitCode, strings.TrimSpace(launch.Stdout), strings.TrimSpace(launch.Stderr))
	}

	deadline := time.Now().Add(r.brokerTimeout())
	for {
		res, err := r.Run(ctx, inst, "cat "+brokerAddrPath+" 2>/dev/null")
		if err == nil && res.ExitCode == 0 {
			if addr := strings.TrimSpace(res.Stdout); addr != "" {
				// The address file proves the socket was bound. Proving it ANSWERS is
				// a separate step and is not skipped: the same mistake as a readiness
				// check that stops at "the process was spawned".
				if err := r.waitBrokerHealthy(ctx, addr, deadline); err != nil {
					return "", err
				}
				return addr, nil
			}
		}
		if time.Now().After(deadline) {
			logs, _ := r.Run(ctx, inst, "tail -20 "+brokerLogPath+" 2>&1")
			return "", fmt.Errorf("box: the broker inside %s did not publish a listen address within %s: launch=%q log=%q",
				inst.InstanceRef, r.brokerTimeout(), strings.TrimSpace(launch.Stdout), strings.TrimSpace(logs.Stdout))
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// waitBrokerHealthy dials the broker's socket until it accepts.
//
// A TCP connect and no more, on purpose. This is the runtime confirming its own
// process came up; it holds no session token and must not hold one, so it cannot
// call an authenticated verb. The verb-level proof belongs to the caller that does
// have the credential — which is what scripts/box-walk.sh asserts.
func (r *LocalRuntime) waitBrokerHealthy(ctx context.Context, addr string, deadline time.Time) error {
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("box: the broker published %s but nothing accepts there: %w", addr, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *LocalRuntime) brokerTimeout() time.Duration {
	if r.ReadyTimeout <= 0 {
		return 20 * time.Second
	}
	return r.ReadyTimeout
}

// BrokerMCPURL renders the endpoint a client points its MCP transport at.
//
// On LocalRuntime this is a loopback address, and every caller that publishes it
// says so rather than dressing it as the box's hostname. Production is
// https://<box>.<domain>/mcp against a Pod with its own network (ADR-019); the
// difference is a deployment topology, not a different broker.
func BrokerMCPURL(addr string) string { return "http://" + addr + "/mcp" }
