package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Dada Box: the object model and the operation contract.
//
// A box is an ephemeral root body an agent works in. It owns exactly one
// environments row with runtime='box', type='dev' (D1 in
// docs/plans/2026-07-29-box-backend-slice.md): environments.id is the identity
// carrier every attached resource, injected env var and published hostname is
// keyed by, so crystallization promotes that same row instead of minting a new
// one, and nothing has to be migrated for the customer's state to survive.
//
// Two conventions this file follows deliberately:
//
//   - Action names are string literals on the wire. The agents are separate Go
//     modules and cannot import this package (see the comment on the action
//     constants), so the constants below are a backend-side convenience only and
//     must stay byte-identical to the literals the workers switch on.
//   - JSON tags are a hard contract with the worker — do NOT rename them. Same
//     discipline as MoveAppPayload in operation.go. A renamed tag is a silent
//     runtime failure in a different process, discovered in production.

// BoxStatus is a box's lifecycle phase.
//
// The value set is intentionally identical to the phase label of the
// dada_boxes{phase} gauge (internal/metrics/box.go) and to the CHECK constraint
// in migration 061, so the gauge is a plain GROUP BY on the column and the three
// records cannot drift apart.
type BoxStatus string

const (
	// BoxStatusRequested — the row exists and BoxUp is enqueued; no body yet.
	BoxStatusRequested BoxStatus = "Requested"
	// BoxStatusBooting — a warm instance is being claimed/bound, or (pool miss)
	// a cold sandbox is starting.
	BoxStatusBooting BoxStatus = "Booting"
	// BoxStatusReady — the canary executed inside the box and returned success.
	// Not "the API answered" and not "TCP accepted" (see internal/box).
	BoxStatusReady BoxStatus = "Ready"
	// BoxStatusIdle — ready, connected, but below the activity threshold. Still
	// running, still resident; the distinction from Ready exists so "idle is not
	// billed" is observable rather than asserted.
	BoxStatusIdle BoxStatus = "Idle"
	// BoxStatusSleeping — frozen after the idle timeout or the hard TTL. Wakes on
	// reconnect; billing for compute stops here.
	BoxStatusSleeping BoxStatus = "Sleeping"
	// BoxStatusCrystallizing — a CrystallizeBox saga is in flight.
	BoxStatusCrystallizing BoxStatus = "Crystallizing"
	// BoxStatusFailed — a terminal failure the customer must see.
	BoxStatusFailed BoxStatus = "Failed"
	// BoxStatusDeleting — teardown enqueued; the body may still exist.
	BoxStatusDeleting BoxStatus = "Deleting"
	// BoxStatusDeleted — tombstone. Kept rather than deleted so the box's
	// environment_id stays claimed forever (one identity, one box) while the
	// box's NAME becomes reusable via the partial unique index.
	BoxStatusDeleted BoxStatus = "Deleted"
)

// boxStatusTransitions is the allowed state machine, as an adjacency list.
//
// It exists as data rather than as a chain of ifs so an illegal transition is a
// one-line diff to review instead of a control-flow change. The webhook is the
// main caller: a box-agent that reports a stale status (a retried callback
// arriving after the box was deleted) must not be able to resurrect the box.
var boxStatusTransitions = map[BoxStatus][]BoxStatus{
	BoxStatusRequested:     {BoxStatusBooting, BoxStatusReady, BoxStatusFailed, BoxStatusDeleting, BoxStatusDeleted},
	BoxStatusBooting:       {BoxStatusReady, BoxStatusFailed, BoxStatusDeleting, BoxStatusDeleted},
	BoxStatusReady:         {BoxStatusIdle, BoxStatusSleeping, BoxStatusCrystallizing, BoxStatusFailed, BoxStatusDeleting},
	BoxStatusIdle:          {BoxStatusReady, BoxStatusSleeping, BoxStatusCrystallizing, BoxStatusFailed, BoxStatusDeleting},
	BoxStatusSleeping:      {BoxStatusBooting, BoxStatusReady, BoxStatusCrystallizing, BoxStatusFailed, BoxStatusDeleting},
	BoxStatusCrystallizing: {BoxStatusReady, BoxStatusIdle, BoxStatusFailed, BoxStatusDeleting},
	BoxStatusFailed:        {BoxStatusDeleting, BoxStatusDeleted},
	BoxStatusDeleting:      {BoxStatusDeleted, BoxStatusFailed},
	BoxStatusDeleted:       nil, // terminal on purpose: a tombstone never reopens
}

// IsValidBoxStatus reports whether s is a known status.
func IsValidBoxStatus(s BoxStatus) bool {
	_, ok := boxStatusTransitions[s]
	return ok
}

// CanTransitionBoxStatus reports whether from -> to is allowed. A no-op
// transition (from == to) is allowed so a repeated webhook is idempotent rather
// than an error; an unknown status on either side is refused.
func CanTransitionBoxStatus(from, to BoxStatus) bool {
	if !IsValidBoxStatus(from) || !IsValidBoxStatus(to) {
		return false
	}
	if from == to {
		return true
	}
	for _, allowed := range boxStatusTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// BoxIsLive reports whether the box still occupies capacity and can be acted on.
func BoxIsLive(s BoxStatus) bool {
	return s != BoxStatusDeleted && s != BoxStatusDeleting && s != BoxStatusFailed
}

// Box mirrors the boxes table (migration 061) column for column.
//
// The runtime coordinate fields (InstanceRef, NodeRef, SSHHost, SSHPort, MCPURL)
// are opaque handles owned by the runtime: the control plane relays them and
// never interprets them. They are written by the box-agent webhook and never by
// the tenant.
type Box struct {
	ID            uuid.UUID `json:"id"             db:"id"`
	ProjectID     uuid.UUID `json:"project_id"     db:"project_id"`
	EnvironmentID uuid.UUID `json:"environment_id" db:"environment_id"`

	Name    string `json:"name"    db:"name"`
	Image   string `json:"image"   db:"image"`
	Profile string `json:"profile" db:"profile"`
	Region  string `json:"region"  db:"region"`

	Status       BoxStatus `json:"status"                  db:"status"`
	ErrorMessage string    `json:"error_message,omitempty" db:"error_message"`

	InstanceRef string `json:"instance_ref,omitempty" db:"instance_ref"`
	NodeRef     string `json:"node_ref,omitempty"     db:"node_ref"`
	SSHHost     string `json:"ssh_host,omitempty"     db:"ssh_host"`
	SSHPort     *int   `json:"ssh_port,omitempty"     db:"ssh_port"`
	MCPURL      string `json:"mcp_url,omitempty"      db:"mcp_url"`

	TTLSeconds         int        `json:"ttl_seconds"              db:"ttl_seconds"`
	IdleTimeoutSeconds int        `json:"idle_timeout_seconds"     db:"idle_timeout_seconds"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"     db:"expires_at"`
	LastActiveAt       *time.Time `json:"last_active_at,omitempty" db:"last_active_at"`
	SleptAt            *time.Time `json:"slept_at,omitempty"       db:"slept_at"`

	// SpendCapRub is nil for "plan default". Reaching the cap suspends the box;
	// it never deletes it, so the customer's data survives their own runaway.
	SpendCapRub *float64 `json:"spend_cap_rub,omitempty" db:"spend_cap_rub"`

	// LastSampleJSON is the newest box-agent sample, taken OUTSIDE the guest —
	// the authoritative activity signal. A heartbeat from inside the guest may
	// only ask for MORE billing, never less, which is what makes trusting it safe.
	LastSampleJSON json.RawMessage `json:"last_sample,omitempty"    db:"last_sample_json"`
	LastSampleAt   *time.Time      `json:"last_sample_at,omitempty" db:"last_sample_at"`

	// AppServerID is set when crystallization succeeds. Its presence is how a
	// caller tells a promoted box from an ephemeral one.
	AppServerID *uuid.UUID `json:"app_server_id,omitempty" db:"app_server_id"`

	CreatedBy *uuid.UUID `json:"created_by,omitempty" db:"created_by"`
	CreatedAt time.Time  `json:"created_at"           db:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"           db:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
}

// The ten box operation actions.
//
// Honest note about the pattern: this repository has no action constants today.
// appservers.go writes 'CreateAppServer' as a SQL literal and gitops-agent
// switches on the same literal, because the agents are separate Go modules and
// cannot import backend/internal/models. The real convention is "payload struct
// with a doc comment, plus a literal". These constants are additive, live only on
// the backend side, and MUST stay byte-identical to the literals the workers
// switch on and to the names in gitops-agent's claim denylist.
//
// THE DENYLIST IS LOAD-BEARING: gitops-agent/internal/db/operations.go claims by
// `action NOT IN (...)`. Every name below has to appear in that exclusion list or
// gitops-agent claims the operation and fails it immediately with
// "unknown action". portainer-agent uses an allowlist and needs no change.
const (
	ActionBoxUp               = "BoxUp"
	ActionSuspendBox          = "SuspendBox"
	ActionResumeBox           = "ResumeBox"
	ActionDeleteBox           = "DeleteBox"
	ActionAttachBoxDatabase   = "AttachBoxDatabase"
	ActionAttachBoxS3         = "AttachBoxS3"
	ActionDetachBoxAttachment = "DetachBoxAttachment"
	ActionExposeBox           = "ExposeBox"
	ActionUnexposeBox         = "UnexposeBox"
	ActionCrystallizeBox      = "CrystallizeBox"
)

// ResourceKindBox is the operations.resource_kind value for every box action.
const ResourceKindBox = "Box"

// BoxActions is every box action name, in lifecycle order. Exported so a test
// can assert the gitops-agent denylist covers all of them without the list being
// duplicated by hand in a third place.
var BoxActions = []string{
	ActionBoxUp,
	ActionSuspendBox,
	ActionResumeBox,
	ActionDeleteBox,
	ActionAttachBoxDatabase,
	ActionAttachBoxS3,
	ActionDetachBoxAttachment,
	ActionExposeBox,
	ActionUnexposeBox,
	ActionCrystallizeBox,
}

// BoxUpPayload is the typed payload for BoxUp operations: claim a warm box (or
// cold-start one on a pool miss), bind tenant identity to it, and report the
// connection coordinates back through the box-agent webhook. JSON tags are a
// hard contract with the box-agent worker — do NOT rename them.
//
// SessionTokenHash carries ONLY a sha256 hash, never a secret. Contrast
// CreateAppServerPayload.SSHPrivateKey, which has to be scrubbed from
// operations.payload after the operation goes terminal precisely because it
// carries a live credential: an operations row is long-lived, replicated and
// visible to every agent that polls the table, so a secret in it is a secret at
// rest in the widest blast radius the platform has. Box sessions were designed
// the other way round from the start — the plaintext token is shown to the
// caller exactly once and only its hash ever reaches the database — so there is
// nothing here to scrub, and no scrub step that can be forgotten. This is a
// deliberate improvement on the VM track, not an accident of scope.
//
// SSHPublicKey is likewise public by construction: the caller keeps the private
// half, so the platform holds no customer credential at all.
type BoxUpPayload struct {
	BoxID            uuid.UUID `json:"box_id"`
	Name             string    `json:"name"`
	Image            string    `json:"image"`
	Profile          string    `json:"profile"`
	Region           string    `json:"region,omitempty"`
	TTLSeconds       int       `json:"ttl_seconds,omitempty"`
	SpendCapRub      *float64  `json:"spend_cap_rub,omitempty"`
	SSHPublicKey     string    `json:"ssh_public_key,omitempty"`
	SessionTokenHash string    `json:"session_token_hash,omitempty"` // sha256 hex, never a secret
}

// SuspendBoxPayload is the typed payload for SuspendBox operations: freeze the
// sandbox so compute billing stops while the disk survives. JSON tags are a hard
// contract with the box-agent worker — do NOT rename them.
//
// Reason is one of user|idle|ttl|spend_cap so the destroy/suspend cause is
// attributable in dada_box_spend_cap_hits_total and in the customer-facing mail
// without the worker guessing.
type SuspendBoxPayload struct {
	BoxID  uuid.UUID `json:"box_id"`
	Reason string    `json:"reason,omitempty"`
}

// ResumeBoxPayload is the typed payload for ResumeBox operations: thaw a
// sleeping box and wait for its exec channel to accept again. JSON tags are a
// hard contract with the box-agent worker — do NOT rename them.
type ResumeBoxPayload struct {
	BoxID uuid.UUID `json:"box_id"`
	// SSHPublicKey lets a resume rebind a fresh key without a new box, so the
	// caller never has to keep a key alive across a sleep.
	SSHPublicKey string `json:"ssh_public_key,omitempty"`
}

// DeleteBoxPayload is the typed payload for DeleteBox operations: destroy the
// sandbox and release its disk. JSON tags are a hard contract with the box-agent
// worker — do NOT rename them.
//
// Destructive and irreversible for the box's own filesystem. Attached resources
// (managed Postgres, buckets) live OUTSIDE the box and are not touched.
type DeleteBoxPayload struct {
	BoxID  uuid.UUID `json:"box_id"`
	Reason string    `json:"reason,omitempty"` // user|ttl|abuse|reaper
}

// AttachBoxDatabasePayload is the typed payload for AttachBoxDatabase
// operations. JSON tags are a hard contract with the box-agent worker — do NOT
// rename them.
//
// TRAP, worth stating in the type: this action does NOT provision the database
// itself. The worker enqueues a CHILD CreateServiceDatabase operation against
// the box's environment_id, waits for it to reach Committed, resolves the
// credential and only then injects env. Same parent/child idiom as
// doImportComposeStack -> DeployStack. Provisioning inline would duplicate the
// Crossplane path and diverge from it on the first change.
type AttachBoxDatabasePayload struct {
	BoxID     uuid.UUID `json:"box_id"`
	Name      string    `json:"name"`
	Database  string    `json:"database"`
	EnvPrefix string    `json:"env_prefix,omitempty"`
}

// AttachBoxS3Payload is the typed payload for AttachBoxS3 operations: create (or
// reuse) a bucket outside the box and inject AWS_*/S3_ENDPOINT into it. JSON
// tags are a hard contract with the box-agent worker — do NOT rename them.
type AttachBoxS3Payload struct {
	BoxID      uuid.UUID `json:"box_id"`
	Name       string    `json:"name"`
	BucketName string    `json:"bucket_name"`
	Region     string    `json:"region,omitempty"`
	EnvPrefix  string    `json:"env_prefix,omitempty"`
}

// DetachBoxAttachmentPayload is the typed payload for DetachBoxAttachment
// operations: withdraw injected credentials and the egress allow-rule. JSON tags
// are a hard contract with the box-agent worker — do NOT rename them.
//
// DeleteResource=false by default on purpose: detaching a database from a
// disposable body must not destroy the customer's data by default.
type DetachBoxAttachmentPayload struct {
	BoxID          uuid.UUID `json:"box_id"`
	AttachmentID   uuid.UUID `json:"attachment_id"`
	DeleteResource bool      `json:"delete_resource,omitempty"`
}

// ExposeBoxPayload is the typed payload for ExposeBox operations: publish one
// port of the box through the broker ingress. JSON tags are a hard contract with
// the box-agent worker — do NOT rename them.
//
// Hostname is always under the platform wildcard (*.box.dada-tuda.ru) and is
// assigned by the platform, never chosen by the caller: custom domains are a
// crystallization feature, which also removes most of the phishing incentive on
// throwaway bodies.
type ExposeBoxPayload struct {
	BoxID    uuid.UUID `json:"box_id"`
	Port     int       `json:"port"`
	Hostname string    `json:"hostname,omitempty"`
}

// UnexposeBoxPayload is the typed payload for UnexposeBox operations: withdraw a
// published hostname. JSON tags are a hard contract with the box-agent worker —
// do NOT rename them.
type UnexposeBoxPayload struct {
	BoxID    uuid.UUID `json:"box_id"`
	Hostname string    `json:"hostname"`
}

// CrystallizeBoxPayload is the typed payload for CrystallizeBox operations:
// promote the ephemeral box into a permanent VM, in place, on the box's own
// environment row.
//
// JSON tags are a hard contract with the box-agent worker — do NOT rename them.
//
// TWO CONTRACT FACTS, both cheap now and expensive to find late:
//
//  1. CrystallizeBox is TERMINAL ON Committed, NOT ON Ready. It ends by writing
//     the app_servers row and the git manifests; the downstream Ready belongs to
//     Argo/Portainer. A caller polling for Ready gets a false timeout. This exact
//     bug is already recorded in the review section of tasks/todo.md.
//  2. AckMonthlyCharge is a consent gate, not a flag. Promotion converts a
//     per-minute body into a monthly VM bill, so the API answers 409 until the
//     caller acknowledges it.
type CrystallizeBoxPayload struct {
	BoxID            uuid.UUID `json:"box_id"`
	AppServerName    string    `json:"app_server_name"`
	Flavor           string    `json:"flavor,omitempty"`
	Region           string    `json:"region,omitempty"`
	Domain           string    `json:"domain,omitempty"`
	AckMonthlyCharge bool      `json:"ack_monthly_charge"`
}
