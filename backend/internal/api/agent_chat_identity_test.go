package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestResolveDeliveredIdentityToken_ReadsDeliveredSecret is the whole claim of the
// console's own cutover: the credential agent chat sends is the token the
// delivery loop wrote, read back out of the cluster rather than pasted into the
// deployment. The console runs in argocd-prod while its Secret is delivered to
// its project namespace, so this read is the only path that does not end in a
// second copy of the token.
func TestResolveDeliveredIdentityToken_ReadsDeliveredSecret(t *testing.T) {
	pool := testPaymentsPool(t)
	ns := "delivery-ns-" + uuid.NewString()[:8]
	appName, _, _ := seedDeliveryApp(t, pool, ns)
	w, cs := newDeliveryWatcher(t, pool)
	ctx := context.Background()

	w.tick(ctx)
	want := deliveredToken(t, cs, ns, appName)

	got, err := w.h.resolveDeliveredIdentityToken(ctx, cs, appName)
	if err != nil {
		t.Fatalf("resolveDeliveredIdentityToken: %v", err)
	}
	if got != want {
		t.Fatalf("resolved %q want the delivered token; chat would authenticate with the wrong credential", got)
	}
}

// TestResolveDeliveredIdentityToken_IgnoresRevokedToken keeps a dead credential from
// being handed to the chat client. A revoked token in the namespace is exactly
// the state re-delivery repairs, and serving it would turn every chat turn into
// a 401 that looks like a gateway outage.
func TestResolveDeliveredIdentityToken_IgnoresRevokedToken(t *testing.T) {
	pool := testPaymentsPool(t)
	ns := "delivery-ns-" + uuid.NewString()[:8]
	appName, _, envID := seedDeliveryApp(t, pool, ns)
	w, cs := newDeliveryWatcher(t, pool)
	ctx := context.Background()

	w.tick(ctx)
	if _, err := pool.Exec(ctx,
		`UPDATE service_identity_tokens SET revoked_at = now()
		  WHERE identity_id IN (SELECT id FROM service_identities WHERE environment_id = $1)`,
		envID,
	); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	got, err := w.h.resolveDeliveredIdentityToken(ctx, cs, appName)
	if err != nil {
		t.Fatalf("resolveDeliveredIdentityToken: %v", err)
	}
	if got != "" {
		t.Fatalf("resolved %q for a revoked token; want empty", got)
	}
}

// TestResolveDeliveredIdentityToken_UnknownAppIsEmpty covers the off-cluster and
// pre-delivery world: no identity, no error, no key -- so the caller falls back
// to its static credential instead of failing to boot.
func TestResolveDeliveredIdentityToken_UnknownAppIsEmpty(t *testing.T) {
	pool := testPaymentsPool(t)
	w, cs := newDeliveryWatcher(t, pool)

	got, err := w.h.resolveDeliveredIdentityToken(context.Background(), cs, "no-such-app-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("resolveDeliveredIdentityToken: %v", err)
	}
	if got != "" {
		t.Fatalf("resolved %q for an app with no identity; want empty", got)
	}
}

// TestRefreshAgentChatIdentityKey_KeepsLastGoodOnFailure guards the degradation
// that matters in prod: a tick that finds nothing must not blank an already
// working credential, or one transient API-server hiccup takes chat down until
// the next successful resolve.
func TestRefreshAgentChatIdentityKey_KeepsLastGoodOnFailure(t *testing.T) {
	pool := testPaymentsPool(t)
	w, cs := newDeliveryWatcher(t, pool)
	h := w.h

	good := "sk-dada-id-known-good"
	h.agentChatIdentityKey.Store(&good)

	h.refreshAgentChatIdentityKey(context.Background(), cs)

	if got := h.currentAgentChatKey(); got != good {
		t.Fatalf("currentAgentChatKey()=%q after a resolve that found nothing; want the last good token", got)
	}
}

// TestAgentChatIdentityAppIsTheConsole pins the app name the console
// authenticates as. Renaming the app without moving this constant leaves chat
// silently falling back to whatever static key is still around -- the exact
// legacy path ADR-021 removes.
func TestAgentChatIdentityAppIsTheConsole(t *testing.T) {
	if agentChatIdentityApp != "cloud-console" {
		t.Fatalf("agentChatIdentityApp=%q; the delivered Secret is named after the app", agentChatIdentityApp)
	}
}
