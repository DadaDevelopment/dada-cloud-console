package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/dada-tuda/console/backend/internal/auth"
)

type fakeVerifier struct {
	claims *auth.KeycloakClaims
	err    error
}

func (f fakeVerifier) Verify(ctx context.Context, raw string) (*auth.KeycloakClaims, error) {
	return f.claims, f.err
}

func newWebhookCtx(t *testing.T, authHeader, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/dadagent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	c.Request = req
	return c, rec
}

func TestDadaAgentWebhook_MissingBearer_401(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "", `{}`)
	h.dadaAgentWebhook(c, fakeVerifier{claims: &auth.KeycloakClaims{Azp: "dada-agent"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

func TestDadaAgentWebhook_VerifyError_401(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer bad", `{}`)
	h.dadaAgentWebhook(c, fakeVerifier{err: errors.New("nope")})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rec.Code)
	}
}

func TestDadaAgentWebhook_WrongAzp_403(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok", `{"intent_id":"i1","event":"completed"}`)
	h.dadaAgentWebhook(c, fakeVerifier{claims: &auth.KeycloakClaims{Azp: "someone-else"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d want 403", rec.Code)
	}
}

func TestDadaAgentWebhook_NilVerifier_503(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok", `{}`)
	h.dadaAgentWebhook(c, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d want 503", rec.Code)
	}
}

func TestDadaAgentWebhook_MissingCorrelationKey_400(t *testing.T) {
	h := &Handler{}
	c, rec := newWebhookCtx(t, "Bearer ok", `{"event":"completed"}`)
	h.dadaAgentWebhook(c, fakeVerifier{claims: &auth.KeycloakClaims{Azp: "dada-agent"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}

func TestCorrelationKey(t *testing.T) {
	cases := []struct {
		name string
		cb   dadaAgentCallback
		want string
	}{
		{"intent_id wins when both present", dadaAgentCallback{IntentID: "int-1", CloudTaskID: "run-2"}, "int-1"},
		{"falls back to cloud_task_id", dadaAgentCallback{CloudTaskID: "run-2"}, "run-2"},
		{"both empty", dadaAgentCallback{}, ""},
	}
	for _, tc := range cases {
		if got := correlationKey(tc.cb); got != tc.want {
			t.Fatalf("%s: correlationKey=%q want %q", tc.name, got, tc.want)
		}
	}
}

func TestMapAgentStatus(t *testing.T) {
	cases := []struct {
		event, status, want string
	}{
		{"completed", "", "completed"},
		{"", "completed", "completed"},
		{"failed", "", "failed"},
		{"", "failed", "failed"},
		{"canceled", "", "canceled"},
		{"progress", "running", "running"},
		{"", "", "running"},
	}
	for _, tc := range cases {
		if got := mapAgentStatus(tc.event, tc.status); got != tc.want {
			t.Fatalf("mapAgentStatus(%q,%q)=%q want %q", tc.event, tc.status, got, tc.want)
		}
	}
}

func TestHasClient(t *testing.T) {
	cl := &auth.KeycloakClaims{ResourceAccessClients: []string{"account", "dada-agent"}}
	if !hasClient(cl, "dada-agent") {
		t.Fatal("expected dada-agent present")
	}
	if hasClient(cl, "missing") {
		t.Fatal("unexpected client match")
	}
}
