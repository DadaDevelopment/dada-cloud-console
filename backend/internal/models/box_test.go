package models

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// TestBoxStatusTransitions pins the state machine. The webhook is the caller that
// matters: box-agent retries, so a stale callback WILL arrive after the box moved
// on, and the rule that a tombstone never reopens is what stops a retry from
// resurrecting a box the customer deleted.
func TestBoxStatusTransitions(t *testing.T) {
	cases := []struct {
		from, to BoxStatus
		want     bool
		why      string
	}{
		{BoxStatusRequested, BoxStatusBooting, true, "the normal claim path"},
		{BoxStatusRequested, BoxStatusReady, true, "a warm-pool hit can skip Booting entirely"},
		{BoxStatusBooting, BoxStatusReady, true, "the canary succeeded"},
		{BoxStatusBooting, BoxStatusFailed, true, "cold start failed"},
		{BoxStatusReady, BoxStatusIdle, true, "below the activity threshold"},
		{BoxStatusIdle, BoxStatusReady, true, "the agent came back"},
		{BoxStatusReady, BoxStatusSleeping, true, "idle timeout or hard TTL"},
		{BoxStatusSleeping, BoxStatusReady, true, "resumed"},
		{BoxStatusSleeping, BoxStatusCrystallizing, true, "a sleeping box can still be promoted"},
		{BoxStatusCrystallizing, BoxStatusReady, true, "promotion finished or was rolled back"},
		{BoxStatusDeleting, BoxStatusDeleted, true, "teardown completed"},
		{BoxStatusReady, BoxStatusReady, true, "a repeated webhook is idempotent, not an error"},

		{BoxStatusDeleted, BoxStatusReady, false, "a tombstone must never reopen"},
		{BoxStatusDeleted, BoxStatusBooting, false, "a tombstone must never reopen"},
		{BoxStatusDeleted, BoxStatusDeleting, false, "a tombstone must never reopen"},
		{BoxStatusFailed, BoxStatusReady, false, "a failed box is re-created, not revived in place"},
		{BoxStatusRequested, BoxStatusIdle, false, "a box cannot be idle before it was ever ready"},
		{BoxStatusRequested, BoxStatusSleeping, false, "a box cannot sleep before it exists"},
		{BoxStatusRequested, BoxStatusCrystallizing, false, "there is nothing yet to crystallize"},
		{BoxStatusDeleting, BoxStatusReady, false, "teardown is not reversible by a stale callback"},
	}
	for _, tc := range cases {
		if got := CanTransitionBoxStatus(tc.from, tc.to); got != tc.want {
			t.Errorf("CanTransitionBoxStatus(%s, %s) = %v, want %v — %s", tc.from, tc.to, got, tc.want, tc.why)
		}
	}
}

func TestBoxStatusValidation(t *testing.T) {
	for _, s := range []BoxStatus{
		BoxStatusRequested, BoxStatusBooting, BoxStatusReady, BoxStatusIdle,
		BoxStatusSleeping, BoxStatusCrystallizing, BoxStatusFailed,
		BoxStatusDeleting, BoxStatusDeleted,
	} {
		if !IsValidBoxStatus(s) {
			t.Errorf("IsValidBoxStatus(%s) = false for a declared status", s)
		}
	}
	for _, s := range []BoxStatus{"", "ready", "Running", "Terminated"} {
		if IsValidBoxStatus(s) {
			t.Errorf("IsValidBoxStatus(%q) = true for an unknown status", s)
		}
	}
	// An unknown status on either side is refused rather than silently allowed:
	// the webhook uses this to reject a status the agent invented.
	if CanTransitionBoxStatus("Running", BoxStatusReady) {
		t.Error("a transition from an unknown status was allowed")
	}
	if CanTransitionBoxStatus(BoxStatusReady, "Running") {
		t.Error("a transition to an unknown status was allowed")
	}
}

func TestBoxIsLive(t *testing.T) {
	live := []BoxStatus{BoxStatusRequested, BoxStatusBooting, BoxStatusReady,
		BoxStatusIdle, BoxStatusSleeping, BoxStatusCrystallizing}
	for _, s := range live {
		if !BoxIsLive(s) {
			t.Errorf("BoxIsLive(%s) = false; it still occupies capacity", s)
		}
	}
	for _, s := range []BoxStatus{BoxStatusDeleting, BoxStatusDeleted, BoxStatusFailed} {
		if BoxIsLive(s) {
			t.Errorf("BoxIsLive(%s) = true; it no longer occupies capacity", s)
		}
	}
}

// TestBoxActionsMatchConstants keeps the exported list and the constants from
// drifting. The list exists so the gitops-agent denylist can be checked against
// one source instead of a hand-copied third place.
func TestBoxActionsMatchConstants(t *testing.T) {
	want := map[string]bool{
		"BoxUp": true, "SuspendBox": true, "ResumeBox": true, "DeleteBox": true,
		"AttachBoxDatabase": true, "AttachBoxS3": true, "DetachBoxAttachment": true,
		"ExposeBox": true, "UnexposeBox": true, "CrystallizeBox": true,
	}
	if len(BoxActions) != len(want) {
		t.Fatalf("BoxActions has %d entries, want %d", len(BoxActions), len(want))
	}
	seen := map[string]bool{}
	for _, a := range BoxActions {
		if !want[a] {
			t.Errorf("unexpected action %q in BoxActions", a)
		}
		if seen[a] {
			t.Errorf("action %q listed twice", a)
		}
		seen[a] = true
	}
}

// TestBoxPayloadJSONTagsRoundTrip pins the wire contract. The tags are a hard
// contract with a worker in a DIFFERENT Go module, so a rename cannot be caught by
// the compiler — only by this test or by a production failure. The literal JSON
// below is deliberately written by hand rather than produced by marshalling the
// struct: comparing a struct to itself would pass under any renaming.
func TestBoxPayloadJSONTagsRoundTrip(t *testing.T) {
	boxID := uuid.MustParse("11111111-2222-3333-4444-555555555555")

	t.Run("BoxUp", func(t *testing.T) {
		raw := `{"box_id":"11111111-2222-3333-4444-555555555555","name":"box-abc","image":"warm-v1",` +
			`"profile":"box-standard","region":"ru1","ttl_seconds":28800,"spend_cap_rub":500,` +
			`"ssh_public_key":"ssh-ed25519 AAAA","session_token_hash":"deadbeef"}`
		var p BoxUpPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if p.BoxID != boxID || p.Name != "box-abc" || p.Image != "warm-v1" ||
			p.Profile != "box-standard" || p.Region != "ru1" || p.TTLSeconds != 28800 ||
			p.SSHPublicKey != "ssh-ed25519 AAAA" || p.SessionTokenHash != "deadbeef" {
			t.Fatalf("field lost or renamed: %+v", p)
		}
		if p.SpendCapRub == nil || *p.SpendCapRub != 500 {
			t.Fatalf("spend_cap_rub lost: %+v", p.SpendCapRub)
		}
		// And back out again, so the worker reading our writes sees the same keys.
		out, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var keys map[string]any
		if err := json.Unmarshal(out, &keys); err != nil {
			t.Fatalf("re-unmarshal: %v", err)
		}
		for _, k := range []string{"box_id", "name", "image", "profile", "region",
			"ttl_seconds", "spend_cap_rub", "ssh_public_key", "session_token_hash"} {
			if _, ok := keys[k]; !ok {
				t.Errorf("marshalled payload lost key %q", k)
			}
		}
	})

	t.Run("SuspendResumeDelete", func(t *testing.T) {
		var s SuspendBoxPayload
		if err := json.Unmarshal([]byte(`{"box_id":"11111111-2222-3333-4444-555555555555","reason":"spend_cap"}`), &s); err != nil {
			t.Fatalf("suspend: %v", err)
		}
		if s.BoxID != boxID || s.Reason != "spend_cap" {
			t.Fatalf("suspend field lost: %+v", s)
		}
		var r ResumeBoxPayload
		if err := json.Unmarshal([]byte(`{"box_id":"11111111-2222-3333-4444-555555555555","ssh_public_key":"k"}`), &r); err != nil {
			t.Fatalf("resume: %v", err)
		}
		if r.BoxID != boxID || r.SSHPublicKey != "k" {
			t.Fatalf("resume field lost: %+v", r)
		}
		var d DeleteBoxPayload
		if err := json.Unmarshal([]byte(`{"box_id":"11111111-2222-3333-4444-555555555555","reason":"ttl"}`), &d); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if d.BoxID != boxID || d.Reason != "ttl" {
			t.Fatalf("delete field lost: %+v", d)
		}
	})

	t.Run("Attach", func(t *testing.T) {
		var db AttachBoxDatabasePayload
		raw := `{"box_id":"11111111-2222-3333-4444-555555555555","name":"maindb","database":"appdb","env_prefix":"PG"}`
		if err := json.Unmarshal([]byte(raw), &db); err != nil {
			t.Fatalf("attach db: %v", err)
		}
		if db.BoxID != boxID || db.Name != "maindb" || db.Database != "appdb" || db.EnvPrefix != "PG" {
			t.Fatalf("attach db field lost: %+v", db)
		}
		var s3 AttachBoxS3Payload
		raw = `{"box_id":"11111111-2222-3333-4444-555555555555","name":"assets","bucket_name":"b-1","region":"ru1","env_prefix":"AWS"}`
		if err := json.Unmarshal([]byte(raw), &s3); err != nil {
			t.Fatalf("attach s3: %v", err)
		}
		if s3.BoxID != boxID || s3.Name != "assets" || s3.BucketName != "b-1" ||
			s3.Region != "ru1" || s3.EnvPrefix != "AWS" {
			t.Fatalf("attach s3 field lost: %+v", s3)
		}
		var det DetachBoxAttachmentPayload
		raw = `{"box_id":"11111111-2222-3333-4444-555555555555","attachment_id":"11111111-2222-3333-4444-555555555555","delete_resource":true}`
		if err := json.Unmarshal([]byte(raw), &det); err != nil {
			t.Fatalf("detach: %v", err)
		}
		if det.BoxID != boxID || det.AttachmentID != boxID || !det.DeleteResource {
			t.Fatalf("detach field lost: %+v", det)
		}
	})

	t.Run("ExposeUnexpose", func(t *testing.T) {
		var e ExposeBoxPayload
		raw := `{"box_id":"11111111-2222-3333-4444-555555555555","port":3000,"hostname":"a.box.dada-tuda.ru"}`
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatalf("expose: %v", err)
		}
		if e.BoxID != boxID || e.Port != 3000 || e.Hostname != "a.box.dada-tuda.ru" {
			t.Fatalf("expose field lost: %+v", e)
		}
		var u UnexposeBoxPayload
		if err := json.Unmarshal([]byte(`{"box_id":"11111111-2222-3333-4444-555555555555","hostname":"a.box.dada-tuda.ru"}`), &u); err != nil {
			t.Fatalf("unexpose: %v", err)
		}
		if u.BoxID != boxID || u.Hostname != "a.box.dada-tuda.ru" {
			t.Fatalf("unexpose field lost: %+v", u)
		}
	})

	t.Run("Crystallize", func(t *testing.T) {
		var cr CrystallizeBoxPayload
		raw := `{"box_id":"11111111-2222-3333-4444-555555555555","app_server_name":"prod-1",` +
			`"flavor":"medium","region":"ru1","domain":"app.example.com","ack_monthly_charge":true}`
		if err := json.Unmarshal([]byte(raw), &cr); err != nil {
			t.Fatalf("crystallize: %v", err)
		}
		if cr.BoxID != boxID || cr.AppServerName != "prod-1" || cr.Flavor != "medium" ||
			cr.Region != "ru1" || cr.Domain != "app.example.com" || !cr.AckMonthlyCharge {
			t.Fatalf("crystallize field lost: %+v", cr)
		}
	})
}

// TestBoxUpPayloadCarriesNoSecret is the assertion behind the comment on
// BoxUpPayload: the box track never puts a live credential in operations.payload,
// so unlike CreateAppServerPayload.SSHPrivateKey there is no scrub step that can
// be forgotten. It is a structural test on purpose — it fails the moment somebody
// adds a private-key or plaintext-token field to the payload.
func TestBoxUpPayloadCarriesNoSecret(t *testing.T) {
	p := BoxUpPayload{
		SSHPublicKey:     "ssh-ed25519 AAAA",
		SessionTokenHash: "0123456789abcdef",
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var keys map[string]any
	if err := json.Unmarshal(out, &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for k := range keys {
		switch k {
		case "ssh_private_key", "private_key", "session_token", "token", "password", "secret":
			t.Errorf("BoxUpPayload gained a secret-bearing field %q; operations.payload is long-lived, "+
				"replicated and readable by every polling agent, so a secret here would need a scrub step "+
				"that somebody will eventually forget", k)
		}
	}
	if _, ok := keys["session_token_hash"]; !ok {
		t.Error("session_token_hash missing: the hash is what the worker compares against")
	}
}
