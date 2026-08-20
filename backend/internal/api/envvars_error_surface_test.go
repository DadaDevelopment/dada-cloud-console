package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// newRevealCtx builds a gin context for RevealEnvVar: a GET request carrying
// the reveal=true query param RevealEnvVar requires, plus the path params and
// claims.
func newRevealCtx(t *testing.T, params gin.Params, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/?reveal=true", nil)
	c.Params = params
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	return c, rec
}

// TestRevealEnvVar_DecryptFailureAuditCarriesRawError is the regression gate
// for the 2026-08-18/19 outage: a broken GITOPS_ENCRYPTION_KEY put the
// env-var subsystem down for 14 hours, and everything the operator had to go
// on was a bare "decrypt_failed" reason -- the actual crypto error never made
// it into the audit row, so diagnosis only happened by chance, from a
// different code path's metadata.
//
// This pins that the audit_events row for a failed reveal now carries the
// underlying error text under metadata["error"], and that the HTTP response
// never leaks the encrypted value or key material.
func TestRevealEnvVar_DecryptFailureAuditCarriesRawError(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedOptimisticFixture(t, pool)
	userID := seedUser(t, pool)
	claims := godClaims(userID)

	appName := "envcrypto-" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, appName)

	hGood := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	key := "GITOPS_ENCRYPTION_KEY_TEST"
	secretValue := "super-secret-value-do-not-leak"

	setCtx, setRec := newCreateCtx(t, `{"value":"`+secretValue+`","is_secret":true,"scope":"runtime"}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: key},
		}, claims)
	hGood.SetEnvVar(setCtx)
	if setRec.Code != http.StatusOK {
		t.Fatalf("SetEnvVar status = %d, want 200; body=%s", setRec.Code, setRec.Body.String())
	}

	brokenKey := "zz" + installTestKey[2:]
	hBroken := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: brokenKey}}

	revealCtx, revealRec := newRevealCtx(t, gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
		{Key: "key", Value: key},
	}, claims)
	hBroken.RevealEnvVar(revealCtx)

	if revealRec.Code != http.StatusInternalServerError {
		t.Fatalf("RevealEnvVar status = %d, want 500; body=%s", revealRec.Code, revealRec.Body.String())
	}
	if strings.Contains(revealRec.Body.String(), secretValue) {
		t.Fatalf("RevealEnvVar response leaked the secret value: %s", revealRec.Body.String())
	}
	if strings.Contains(revealRec.Body.String(), installTestKey) || strings.Contains(revealRec.Body.String(), brokenKey) {
		t.Fatalf("RevealEnvVar response leaked key material: %s", revealRec.Body.String())
	}

	var reason string
	var errText *string
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata->>'reason', metadata->>'error' FROM audit_events
		  WHERE project_id = $1 AND environment_id = $2 AND action = $3 AND resource_name = $4
		  ORDER BY created_at DESC LIMIT 1`,
		projectID, envID, auditActionRevealEnvVar, key,
	).Scan(&reason, &errText); err != nil {
		t.Fatalf("expected a RevealEnvVar audit row, got error: %v", err)
	}
	if reason != "decrypt_failed" {
		t.Fatalf("audit reason = %q, want decrypt_failed", reason)
	}
	if errText == nil || *errText == "" {
		t.Fatalf("audit metadata carries no \"error\" key -- this is the incident: the operator has no way to see WHY decryption failed")
	}
	if !strings.Contains(*errText, "decoding encryption key") {
		t.Fatalf("audit metadata error = %q, want it to contain the underlying hex-decode failure (\"decoding encryption key\")", *errText)
	}
}

// TestRevealEnvVar_DecryptFailureResponseCarriesCodeNotRawError is the sibling
// gate for the frontend side of the same 2026-08-18/19 outage: with no
// machine-readable "code" in the HTTP body, the console could only branch on
// error prose (a /403|forbidden|permission/i regex), so a broken
// GITOPS_ENCRYPTION_KEY surfaced to kkartov@yandex.ru as a bare "не удалось",
// and he pressed DeleteApp seven times chasing it.
//
// This pins that a failed reveal's JSON body carries a stable "code" field
// the frontend can switch on, and -- the other half of the same incident --
// that the body never carries the raw decrypt error text, which can contain
// encryption-key material.
func TestRevealEnvVar_DecryptFailureResponseCarriesCodeNotRawError(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedOptimisticFixture(t, pool)
	userID := seedUser(t, pool)
	claims := godClaims(userID)

	appName := "envcrypto-" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, appName)

	hGood := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
	key := "GITOPS_ENCRYPTION_KEY_TEST"
	secretValue := "super-secret-value-do-not-leak"

	setCtx, setRec := newCreateCtx(t, `{"value":"`+secretValue+`","is_secret":true,"scope":"runtime"}`,
		gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: key},
		}, claims)
	hGood.SetEnvVar(setCtx)
	if setRec.Code != http.StatusOK {
		t.Fatalf("SetEnvVar status = %d, want 200; body=%s", setRec.Code, setRec.Body.String())
	}

	brokenKey := "zz" + installTestKey[2:]
	hBroken := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: brokenKey}}

	revealCtx, revealRec := newRevealCtx(t, gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
		{Key: "key", Value: key},
	}, claims)
	hBroken.RevealEnvVar(revealCtx)

	if revealRec.Code != http.StatusInternalServerError {
		t.Fatalf("RevealEnvVar status = %d, want 500; body=%s", revealRec.Code, revealRec.Body.String())
	}

	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(revealRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("RevealEnvVar response is not valid JSON: %v; body=%s", err, revealRec.Body.String())
	}
	if body.Code != "decrypt_failed" {
		t.Fatalf("RevealEnvVar response code = %q, want %q; body=%s", body.Code, "decrypt_failed", revealRec.Body.String())
	}

	if strings.Contains(revealRec.Body.String(), "decoding encryption key") {
		t.Fatalf("RevealEnvVar response leaked the raw decrypt error text: %s", revealRec.Body.String())
	}
	if strings.Contains(revealRec.Body.String(), secretValue) {
		t.Fatalf("RevealEnvVar response leaked the secret value: %s", revealRec.Body.String())
	}
	if strings.Contains(revealRec.Body.String(), installTestKey) || strings.Contains(revealRec.Body.String(), brokenKey) {
		t.Fatalf("RevealEnvVar response leaked key material: %s", revealRec.Body.String())
	}
}
