package api

import (
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
)

// The usage webhook shares fakeVerifier and newWebhookCtx with the status
// webhook test. These cases cover every branch that returns before the ledger
// pool is touched (auth gate + payload validation), so a nil pool is safe.

func TestDadaAgentUsageWebhook_MissingBearer_401(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "", `{}`)
	h.dadaAgentUsageWebhook(c, fakeVerifier{claims: &auth.KeycloakClaims{Azp: "dada-agent"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

func TestDadaAgentUsageWebhook_NilVerifier_503(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok", `{}`)
	h.dadaAgentUsageWebhook(c, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d want 503", rec.Code)
	}
}

func TestDadaAgentUsageWebhook_WrongAzp_403(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok",
		`{"platform_request_id":"ct-1-0-m","model":"claude","intent_id":"i1"}`)
	h.dadaAgentUsageWebhook(c, fakeVerifier{claims: &auth.KeycloakClaims{Azp: "someone-else"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d want 403", rec.Code)
	}
}

func TestDadaAgentUsageWebhook_MissingPlatformRequestID_400(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok", `{"model":"claude","intent_id":"i1"}`)
	h.dadaAgentUsageWebhook(c, fakeVerifier{claims: &auth.KeycloakClaims{Azp: "dada-agent"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}

func TestDadaAgentUsageWebhook_MissingModel_400(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok",
		`{"platform_request_id":"ct-1-0-m","intent_id":"i1"}`)
	h.dadaAgentUsageWebhook(c, fakeVerifier{claims: &auth.KeycloakClaims{Azp: "dada-agent"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}

func TestDadaAgentUsageWebhook_MissingCorrelationKey_400(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok",
		`{"platform_request_id":"ct-1-0-m","model":"claude"}`)
	h.dadaAgentUsageWebhook(c, fakeVerifier{claims: &auth.KeycloakClaims{Azp: "dada-agent"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}
