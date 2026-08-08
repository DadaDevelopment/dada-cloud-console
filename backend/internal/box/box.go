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

// ErrColdStart reports that the pool was empty AND building a body on the spot
// did not work either.
//
// It is a separate error from ErrPoolExhausted because the two say opposite
// things to the person who hit them. "Exhausted" means the product is full and
// nothing the caller does will help; a failed cold start means the cluster had
// room and one particular build did not finish in the time the caller allowed,
// which a retry — or a longer wait_seconds — can fix. Folding the second into
// the first kept one metric label at the cost of telling first-time users the
// product was out of capacity when it was not. The label is kept distinct
// instead, so the alert that watches genuine exhaustion is not diluted by the
// slow path it was never meant to cover.
var ErrColdStart = errors.New("box: cold start did not produce a ready body")

// ErrBodyGone reports that the box's running body no longer exists — the pod was
// deleted by a node drain, an evicted preemption, or a teardown that already ran.
//
// It exists so teardown paths can tell "I could not reach the box" apart from
// "there is nothing left to reach". The difference is not cosmetic: a suspend
// that treats a missing pod as a failure retries until it gives up, the reaper
// enqueues the same suspend on its next pass, and the box stays Ready forever
// while pointing at an address that answers nothing.
var ErrBodyGone = errors.New("box: the box has no running body")

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

// Sleeper is the optional half of a runtime: a box that can be put down and
// picked back up without losing what the agent did in it.
//
// It is deliberately not part of BoxRuntime. Sleep is only meaningful where the
// disk outlives the body, and a runtime that cannot promise that must fail the
// request out loud rather than inherit a method it would have to implement as a
// destroy. Callers type-assert, and the failure of that assertion is the honest
// answer "this runtime does not do that".
type Sleeper interface {
	// Suspend releases the running body and keeps the workspace disk.
	Suspend(ctx context.Context, inst *Instance) error
	// Resume rebuilds a body around the surviving disk and waits for its door.
	Resume(ctx context.Context, inst *Instance, spec Spec) error
}

// ServiceRestarter is a runtime that can bring a box's declared services back up
// after its body was replaced.
//
// Separate from Sleeper because it is a step of the RESUME OPERATION rather than
// of the resume itself: the services have to start after the tenant's environment
// is rebound into the new body, and a runtime whose bodies never lose their
// processes has nothing to implement here.
type ServiceRestarter interface {
	RestartServices(ctx context.Context, inst *Instance) error
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

// ReconcilingPool is the extra a pool whose state lives outside this process can
// offer the warmer, and it is optional because a pool whose state IS this process
// does not need it: MemoryPool's Available already counts everything it has.
//
// Inventory is not Available. Available answers a claimer — how many bodies can be
// handed over right now — and must exclude a pod that is still coming up.
// Inventory answers the warmer — how many bodies exist — and a warmer that asks
// the claimer's question builds a second box during the ninety seconds the first
// one takes to become Ready. With two console replicas reconciling on the same
// interval that is not a corner case, it is what happens.
//
// Trim exists because a pool that only grows is a leak. Every over-fill is
// permanent otherwise, and the surplus holds fleet quota a customer's box needs.
type ReconcilingPool interface {
	Inventory(ctx context.Context, image, region string) (int, error)
	Trim(ctx context.Context, image, region string, keep int) (int, error)
}

// poolInventory asks a pool how many bodies it has, falling back to the claimable
// count for a pool that keeps its state in memory and cannot tell the difference.
func poolInventory(ctx context.Context, pool ParkingPool, image, region string) (int, error) {
	if rp, ok := pool.(ReconcilingPool); ok {
		return rp.Inventory(ctx, image, region)
	}
	return pool.Available(image, region), nil
}

// poolTrim removes surplus bodies from a pool that can, and reports zero for one
// that cannot: an in-memory pool's surplus disappears with the process anyway.
func poolTrim(ctx context.Context, pool ParkingPool, image, region string, keep int) (int, error) {
	if rp, ok := pool.(ReconcilingPool); ok {
		return rp.Trim(ctx, image, region, keep)
	}
	return 0, nil
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
