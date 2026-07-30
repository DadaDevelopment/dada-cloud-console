// Package box holds the Dada Box control-plane seams and the ready-path
// orchestrator.
//
// A box is an ephemeral root body an agent works in: it boots in seconds, the
// customer's own agent connects to it, managed Postgres and S3 attach mid-flight,
// and a surviving prototype crystallizes into a permanent VM without being
// recreated. See docs/product/box-product-brief.md.
//
// This package exists before the runtime does, on purpose. Everything that can be
// decided and tested without a hypervisor lives here behind four interfaces
// (BoxRuntime, WarmPool, AttachProvider, Crystallizer), so the parts that carry
// the product's central claim — time to ready, what "ready" means, and phase
// accounting — are pinned by hermetic tests instead of discovered in production.
//
// Two invariants this package enforces rather than documents:
//
//   - Every timestamp on the critical path is taken by the orchestrator observing
//     the box. A guest-reported time is never trusted: a freshly booted box's
//     clock is exactly the thing that is wrong, and time to ready is the one
//     number the product is sold on.
//   - "Ready" means a command executed inside the box and returned success, with
//     the warm toolchain present. Not "the API answered", not "TCP accepted".
package box

import (
	"context"
	"errors"
	"time"
)

// ErrPoolExhausted is returned by WarmPool.Claim when no pre-warmed box is
// available. It is a typed error rather than a blocking wait: a caller that
// blocks turns an empty pool into an invisible latency cliff, whereas a caller
// that sees ErrPoolExhausted can fall back to a cold start and account for it.
var ErrPoolExhausted = errors.New("box: warm pool exhausted")

// Clock is the orchestrator's time source. Injected so tests can drive the ready
// path deterministically; it is never satisfied by anything reported by a box.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production Clock.
type SystemClock struct{}

// Now returns the current wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }

// Spec describes the box a caller wants.
type Spec struct {
	Image   string
	Profile string
	Region  string
	// SSHPublicKey is injected into the guest at claim time so the first
	// connection needs no password round trip. It is a public key, so unlike the
	// VM track's private key it never needs scrubbing from an operation payload.
	SSHPublicKey string
	Env          map[string]string
}

// Instance is a claimed box as the control plane sees it. The refs are opaque
// handles owned by the runtime; the control plane relays them and never
// interprets them.
type Instance struct {
	ID          string
	InstanceRef string
	NodeRef     string
	Image       string
	Region      string
	SSHHost     string
	SSHPort     int
	MCPURL      string
}

// CanaryResult is the outcome of running CanaryCommand inside a box.
//
// GuestClaimedAt is deliberately present and deliberately unused by the ready
// path. Runtimes tend to report their own idea of when they became ready, and
// trusting it is the easy mistake: it would make time to ready depend on a clock
// we do not control, inside a machine that just booted. It is carried so it can
// be logged and compared, never so it can be measured with.
type CanaryResult struct {
	ExitCode       int
	Stdout         string
	GuestClaimedAt time.Time
}

// BoxRuntime is the seam over whatever actually runs a box (gVisor sandboxes on a
// pool of VMs today; a micro-VM backend later behind the same interface). The
// control plane holds only this.
type BoxRuntime interface {
	// Bind attaches tenant identity to an already-running, quarantined instance:
	// mounts its volume, writes its identity, injects the caller's public key.
	Bind(ctx context.Context, inst *Instance, spec Spec) error
	// ProgramNetwork moves the instance out of quarantine into tenant egress.
	ProgramNetwork(ctx context.Context, inst *Instance) error
	// Unfreeze thaws the instance and waits for its exec channel to accept.
	Unfreeze(ctx context.Context, inst *Instance) error
	// Exec runs one command inside the box and returns its exit status.
	Exec(ctx context.Context, inst *Instance, cmd string) (CanaryResult, error)
	// Destroy releases the instance and its disk.
	Destroy(ctx context.Context, inst *Instance) error
}

// WarmPool hands out pre-warmed instances. Boxes are not created on demand:
// creation is what costs minutes, so it happens ahead of demand and a spawn is a
// claim. Claim reports whether it was a warm hit, because a miss must be
// accounted for rather than averaged away.
type WarmPool interface {
	Claim(ctx context.Context, image, region string) (inst *Instance, hit bool, err error)
	Available(image, region string) int
	Target(image, region string) int
}

// ParkingPool is a WarmPool a warmer can also fill: Add parks a pre-warmed
// instance, SetTarget records how many free ones the controller aims to keep, so
// "available" reads against an intent rather than against zero.
//
// Claiming and filling are separate interfaces because they have separate callers.
// Request handlers only ever claim, and a handler able to park an instance could
// also park a used one.
type ParkingPool interface {
	WarmPool
	Add(image, region string, inst *Instance)
	SetTarget(image, region string, n int)
}

// Warmer fills a pool ahead of demand. Separate from BoxRuntime because warming is
// startup, not a request: an adapter is asked to warm once, and asked to bind and
// exec for the rest of its life.
type Warmer interface {
	Warm(ctx context.Context, pool ParkingPool, image, region string, n int) error
}

// Door is the box's own endpoint (D6): the customer's agent talks to the BOX, so
// their source, prompts and model credentials never traverse the control plane.
// BrokerConfigured reports whether this adapter can give a box a door at all;
// InstallSessionDigests makes the box's credential file match the live session set
// (a mirror and not a log, which is how a revocation lands); RevokeAllSessionDigests
// empties it; StartBroker starts the endpoint and returns the address it bound.
//
// A seam of its own rather than part of BoxRuntime, because the two answer
// different questions. BoxRuntime answers "does this body exist and run commands";
// Door answers "can the tenant reach it without us in the middle". An adapter can
// satisfy the first and not the second — that is exactly the degraded box the
// control-plane fallback exists for — and one interface would make that
// distinction unrepresentable.
type Door interface {
	BrokerConfigured() bool
	InstallSessionDigests(ctx context.Context, inst *Instance, digests []SessionDigest) error
	RevokeAllSessionDigests(ctx context.Context, inst *Instance) error
	StartBroker(ctx context.Context, inst *Instance, boxName string) (string, error)
}

// Exposer publishes one port of a box on a hostname the PLATFORM assigns. The
// caller never chooses the hostname: custom domains belong to crystallization, and
// an arbitrary name on a throwaway body is a phishing surface.
type Exposer interface {
	Expose(boxName string, port int) (Exposure, error)
	Unexpose(hostname string) error
}

// AttachProvider attaches managed resources to a running box. The resources live
// outside the box — a disposable body must not own the customer's database — so
// attaching is credential injection plus a record of exactly what was injected.
//
// AttachPostgresNamed additionally returns the name of the resource that was
// created, so the attachment row records what exists rather than what was asked
// for; the two differ whenever a name is normalised or made unique.
// ManagedPostgresConfigured lets a handler answer 503 rather than 500 when the
// platform is simply not wired for managed Postgres: an unconfigured subsystem is
// not a failed request, and conflating them makes an outage dashboard lie.
type AttachProvider interface {
	AttachPostgres(ctx context.Context, inst *Instance, name, envPrefix string) (injected []string, err error)
	AttachPostgresNamed(ctx context.Context, inst *Instance, name, envPrefix string) (injected []string, resource string, err error)
	AttachS3(ctx context.Context, inst *Instance, bucket, envPrefix string) (injected []string, err error)
	ManagedPostgresConfigured() bool
}

// Crystallizer promotes an ephemeral box into a permanent VM. It returns a carry
// manifest describing, per kind of state, whether it was preserved, recreated or
// lost. Declared loss is part of the contract: an implementation that quietly
// drops state is worse than one that reports the drop, because the promise is
// that the same object continues living.
type Crystallizer interface {
	Crystallize(ctx context.Context, inst *Instance, domain string) (CarryManifest, error)
}

// CarryDisposition is what happened to one kind of state during crystallization.
type CarryDisposition string

const (
	CarryPreserved CarryDisposition = "preserved"
	CarryRecreated CarryDisposition = "recreated"
	CarryLost      CarryDisposition = "lost"
)

// CarryManifest maps a kind of state (volume|env|attachment|address|port|process)
// to its disposition. Anything reported as lost increments
// dada_box_crystallize_state_loss_total, which is the only critical box alert.
type CarryManifest map[string]CarryDisposition
