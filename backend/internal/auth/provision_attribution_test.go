package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestResolveUser_FreshSignupStoresAttribution and its siblings below run
// against a real database (see provisionSignalPool in provision_signal_test.go)
// because the guarantee is a property of the SQL statement written inside
// upsertBySub, not of Go control flow: attribution must be born with the
// users row, and the ON CONFLICT branch must never touch it again.
//
// This one checks that a fresh signup lands its attribution on both the users
// row and the SignUp audit row it is born with -- a channel that only exists
// on one of the two is a channel a funnel query can silently miss.
func TestResolveUser_FreshSignupStoresAttribution(t *testing.T) {
	pool := provisionSignalPool(t)
	kc := throwawayClaims(t, pool)
	ctx := context.Background()

	attr := SignupAttribution{
		Source:   "yandex_direct",
		Medium:   "cpc",
		Campaign: "aug-launch",
		Referrer: "https://yandex.ru/search/?text=cloud+hosting",
	}

	id, created, err := ResolveUser(ctx, pool, kc, true, attr)
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if !created {
		t.Fatalf("created = false for a fresh identity")
	}

	var source, medium, campaign, referrer string
	if err := pool.QueryRow(ctx,
		`SELECT signup_source, signup_medium, signup_campaign, signup_referrer FROM users WHERE id = $1`,
		id,
	).Scan(&source, &medium, &campaign, &referrer); err != nil {
		t.Fatalf("read users attribution: %v", err)
	}
	if source != attr.Source || medium != attr.Medium || campaign != attr.Campaign || referrer != attr.Referrer {
		t.Errorf("users attribution = (%q, %q, %q, %q), want (%q, %q, %q, %q)",
			source, medium, campaign, referrer, attr.Source, attr.Medium, attr.Campaign, attr.Referrer)
	}

	var metaSource, metaMedium, metaCampaign, metaReferrer string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'signup_source', metadata->>'signup_medium',
		        metadata->>'signup_campaign', metadata->>'signup_referrer'
		   FROM audit_events WHERE actor_id = $1 AND action = 'SignUp'`,
		id,
	).Scan(&metaSource, &metaMedium, &metaCampaign, &metaReferrer); err != nil {
		t.Fatalf("read SignUp audit metadata: %v", err)
	}
	if metaSource != attr.Source || metaMedium != attr.Medium || metaCampaign != attr.Campaign || metaReferrer != attr.Referrer {
		t.Errorf("SignUp metadata attribution = (%q, %q, %q, %q), want (%q, %q, %q, %q)",
			metaSource, metaMedium, metaCampaign, metaReferrer, attr.Source, attr.Medium, attr.Campaign, attr.Referrer)
	}
}

// TestResolveUser_SecondCallDoesNotOverwriteAttribution checks first touch
// wins: a second ResolveUser call for the same identity, carrying different
// attribution (as a later visit with a different referrer would), must not
// overwrite what was stored on signup.
func TestResolveUser_SecondCallDoesNotOverwriteAttribution(t *testing.T) {
	pool := provisionSignalPool(t)
	kc := throwawayClaims(t, pool)
	ctx := context.Background()

	first := SignupAttribution{Source: "google", Medium: "organic", Campaign: "", Referrer: "https://google.com/"}
	id, created, err := ResolveUser(ctx, pool, kc, true, first)
	if err != nil || !created {
		t.Fatalf("seed ResolveUser: id=%v created=%v err=%v", id, created, err)
	}

	second := SignupAttribution{Source: "vk", Medium: "social", Campaign: "retarget", Referrer: "https://vk.com/"}
	againID, createdAgain, err := ResolveUser(ctx, pool, kc, true, second)
	if err != nil {
		t.Fatalf("second ResolveUser: %v", err)
	}
	if againID != id {
		t.Fatalf("second call resolved to %v, want the same user %v", againID, id)
	}
	if createdAgain {
		t.Error("created = true on a returning identity")
	}

	var source, medium, referrer string
	var campaign *string
	if err := pool.QueryRow(ctx,
		`SELECT signup_source, signup_medium, signup_campaign, signup_referrer FROM users WHERE id = $1`,
		id,
	).Scan(&source, &medium, &campaign, &referrer); err != nil {
		t.Fatalf("read users attribution: %v", err)
	}
	if source != first.Source || medium != first.Medium || referrer != first.Referrer {
		t.Errorf("attribution changed on second call: (%q, %q, %q), want first-touch (%q, %q, %q)",
			source, medium, referrer, first.Source, first.Medium, first.Referrer)
	}
	if campaign != nil {
		t.Errorf("signup_campaign = %v, want NULL (first touch had no campaign)", *campaign)
	}
}

// TestResolveUser_EmptyAttributionFieldsLandAsNull covers the case every
// visitor with no utm parameters and no document.referrer produces: empty
// medium/campaign/referrer must land as SQL NULL, not as empty strings, so a
// funnel query can distinguish "no data" from "an empty tag was recorded".
func TestResolveUser_EmptyAttributionFieldsLandAsNull(t *testing.T) {
	pool := provisionSignalPool(t)
	kc := throwawayClaims(t, pool)
	ctx := context.Background()

	attr := SignupAttribution{Source: "direct"}

	id, created, err := ResolveUser(ctx, pool, kc, true, attr)
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	if !created {
		t.Fatalf("created = false for a fresh identity")
	}

	var source string
	var medium, campaign, referrer *string
	if err := pool.QueryRow(ctx,
		`SELECT signup_source, signup_medium, signup_campaign, signup_referrer FROM users WHERE id = $1`,
		id,
	).Scan(&source, &medium, &campaign, &referrer); err != nil {
		t.Fatalf("read users attribution: %v", err)
	}
	if source != "direct" {
		t.Errorf("signup_source = %q, want %q", source, "direct")
	}
	if medium != nil {
		t.Errorf("signup_medium = %v, want NULL", *medium)
	}
	if campaign != nil {
		t.Errorf("signup_campaign = %v, want NULL", *campaign)
	}
	if referrer != nil {
		t.Errorf("signup_referrer = %v, want NULL", *referrer)
	}

	var metaMedium *string
	if err := pool.QueryRow(ctx,
		`SELECT metadata->>'signup_medium' FROM audit_events WHERE actor_id = $1 AND action = 'SignUp'`,
		id,
	).Scan(&metaMedium); err != nil {
		t.Fatalf("read SignUp audit metadata: %v", err)
	}
	if metaMedium != nil {
		t.Errorf("SignUp metadata signup_medium = %v, want SQL NULL (jsonb null)", *metaMedium)
	}
}

// TestResolveUser_NoCookiesStillProvisionsRow covers a signup with no
// attribution cookies at all -- the zero-value struct every non-marketing
// caller (and every pre-cookie legacy client) passes -- and checks it still
// succeeds and leaves a users row. Attribution is enrichment, never a
// precondition for provisioning.
func TestResolveUser_NoCookiesStillProvisionsRow(t *testing.T) {
	pool := provisionSignalPool(t)
	kc := throwawayClaims(t, pool)
	ctx := context.Background()

	id, created, err := ResolveUser(ctx, pool, kc, true, SignupAttribution{})
	if err != nil {
		t.Fatalf("ResolveUser with no attribution: %v", err)
	}
	if !created {
		t.Fatalf("created = false for a fresh identity")
	}
	if id == uuid.Nil {
		t.Fatal("id = uuid.Nil, want a provisioned user id")
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users rows for %v = %d, want 1", id, count)
	}
}
