package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// wireCodeBody is the shape every env-var 403/404 response is expected to
// carry: prose for a human plus a stable "code" a client can switch on
// instead of parsing the "error" string (backlog 0419, follow-up of
// af4c27ff -- the frontend dictionary in frontend/lib/env-error.ts falls
// back to raw prose whenever a response has no code).
type wireCodeBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func decodeWireCode(t *testing.T, body []byte) wireCodeBody {
	t.Helper()
	var b wireCodeBody
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatalf("response is not valid JSON: %v; body=%s", err, body)
	}
	return b
}

// notAMemberClaims is a real user with no membership on any project: the
// fixture project in these tests always carries a NULL org_id, so neither the
// org-role cascade nor the personal-org cascade can ever resolve a role for
// this identity.
func notAMemberClaims(userID uuid.UUID) *auth.Claims {
	return &auth.Claims{UserID: userID, Username: "outsider-" + uuid.NewString()[:8]}
}

// readOnlyClaims grants ReadOnly on exactly one project via an explicit
// project-role group -- the org segment of the group path is not checked
// against the project's actual org (see resolveRole), only the project id is,
// so this resolves to ReadOnly regardless of the fixture project's org_id.
func readOnlyClaims(userID, projectID uuid.UUID) *auth.Claims {
	return &auth.Claims{UserID: userID, Groups: []string{"/orgs/wire-code-test/projects/" + projectID.String() + "/ReadOnly"}}
}

func wireCodeHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
}

// TestSetEnvVar_ErrorSitesCarryStableWireCodes pins the wire "code" for every
// 403/404 exit in SetEnvVar. Each subtest breaks the handler exactly one way
// and checks status + code together, so a swapped mapping (e.g.
// not_a_member answered where read_only_role belongs) fails loudly instead
// of both cases quietly returning "not found".
func TestSetEnvVar_ErrorSitesCarryStableWireCodes(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedOptimisticFixture(t, pool)
	memberID := seedUser(t, pool)
	outsiderID := seedUser(t, pool)
	h := wireCodeHandler(pool)
	appName := "wire-set-" + uuid.NewString()[:8]

	t.Run("malformed projectId is project_not_found", func(t *testing.T) {
		c, rec := newCreateCtx(t, `{"value":"x"}`, gin.Params{
			{Key: "projectId", Value: "not-a-uuid"},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, godClaims(memberID))
		h.SetEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if body := decodeWireCode(t, rec.Body.Bytes()); body.Code != "project_not_found" {
			t.Fatalf("code = %q, want project_not_found; body=%s", body.Code, rec.Body.String())
		}
	})

	t.Run("malformed envId is env_not_in_project", func(t *testing.T) {
		c, rec := newCreateCtx(t, `{"value":"x"}`, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: "not-a-uuid"},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, godClaims(memberID))
		h.SetEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if body := decodeWireCode(t, rec.Body.Bytes()); body.Code != "env_not_in_project" {
			t.Fatalf("code = %q, want env_not_in_project; body=%s", body.Code, rec.Body.String())
		}
	})

	t.Run("caller with no role is not_a_member", func(t *testing.T) {
		c, rec := newCreateCtx(t, `{"value":"x"}`, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, notAMemberClaims(outsiderID))
		h.SetEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if body := decodeWireCode(t, rec.Body.Bytes()); body.Code != "not_a_member" {
			t.Fatalf("code = %q, want not_a_member; body=%s", body.Code, rec.Body.String())
		}
	})

	t.Run("ReadOnly role is read_only_role", func(t *testing.T) {
		c, rec := newCreateCtx(t, `{"value":"x"}`, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, readOnlyClaims(outsiderID, projectID))
		h.SetEnvVar(c)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if body := decodeWireCode(t, rec.Body.Bytes()); body.Code != "read_only_role" {
			t.Fatalf("code = %q, want read_only_role; body=%s", body.Code, rec.Body.String())
		}
	})

	t.Run("env belonging to a different project is env_not_in_project", func(t *testing.T) {
		otherProjectID, _ := seedOptimisticFixture(t, pool)
		c, rec := newCreateCtx(t, `{"value":"x"}`, gin.Params{
			{Key: "projectId", Value: otherProjectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, godClaims(memberID))
		h.SetEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if body := decodeWireCode(t, rec.Body.Bytes()); body.Code != "env_not_in_project" {
			t.Fatalf("code = %q, want env_not_in_project; body=%s", body.Code, rec.Body.String())
		}
	})
}

// TestBulkSetEnvVars_ErrorSitesCarryStableWireCodes mirrors the SetEnvVar
// coverage above for BulkSetEnvVars' own copy of the same three guards.
func TestBulkSetEnvVars_ErrorSitesCarryStableWireCodes(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedOptimisticFixture(t, pool)
	memberID := seedUser(t, pool)
	outsiderID := seedUser(t, pool)
	h := wireCodeHandler(pool)
	appName := "wire-bulk-" + uuid.NewString()[:8]
	body := `{"vars":[{"key":"K","value":"x"}]}`

	t.Run("caller with no role is not_a_member", func(t *testing.T) {
		c, rec := newCreateCtx(t, body, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, notAMemberClaims(outsiderID))
		h.BulkSetEnvVars(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "not_a_member" {
			t.Fatalf("code = %q, want not_a_member; body=%s", got.Code, rec.Body.String())
		}
	})

	t.Run("ReadOnly role is read_only_role", func(t *testing.T) {
		c, rec := newCreateCtx(t, body, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, readOnlyClaims(outsiderID, projectID))
		h.BulkSetEnvVars(c)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "read_only_role" {
			t.Fatalf("code = %q, want read_only_role; body=%s", got.Code, rec.Body.String())
		}
	})

	t.Run("env belonging to a different project is env_not_in_project", func(t *testing.T) {
		otherProjectID, _ := seedOptimisticFixture(t, pool)
		c, rec := newCreateCtx(t, body, gin.Params{
			{Key: "projectId", Value: otherProjectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
		}, godClaims(memberID))
		h.BulkSetEnvVars(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "env_not_in_project" {
			t.Fatalf("code = %q, want env_not_in_project; body=%s", got.Code, rec.Body.String())
		}
	})
}

// TestRevealEnvVar_ErrorSitesCarryStableWireCodes covers RevealEnvVar's four
// 403/404 exits, including var_not_found -- the one site in this class that
// is not a membership/role/env guard but a missing row.
func TestRevealEnvVar_ErrorSitesCarryStableWireCodes(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedOptimisticFixture(t, pool)
	memberID := seedUser(t, pool)
	outsiderID := seedUser(t, pool)
	h := wireCodeHandler(pool)
	appName := "wire-reveal-" + uuid.NewString()[:8]

	t.Run("caller with no role is not_a_member", func(t *testing.T) {
		c, rec := newRevealCtx(t, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, notAMemberClaims(outsiderID))
		h.RevealEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "not_a_member" {
			t.Fatalf("code = %q, want not_a_member; body=%s", got.Code, rec.Body.String())
		}
	})

	t.Run("ReadOnly role is read_only_role", func(t *testing.T) {
		c, rec := newRevealCtx(t, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, readOnlyClaims(outsiderID, projectID))
		h.RevealEnvVar(c)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "read_only_role" {
			t.Fatalf("code = %q, want read_only_role; body=%s", got.Code, rec.Body.String())
		}
	})

	t.Run("env belonging to a different project is env_not_in_project", func(t *testing.T) {
		otherProjectID, _ := seedOptimisticFixture(t, pool)
		c, rec := newRevealCtx(t, gin.Params{
			{Key: "projectId", Value: otherProjectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, godClaims(memberID))
		h.RevealEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "env_not_in_project" {
			t.Fatalf("code = %q, want env_not_in_project; body=%s", got.Code, rec.Body.String())
		}
	})

	t.Run("key never set is var_not_found", func(t *testing.T) {
		c, rec := newRevealCtx(t, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "NEVER_SET_" + uuid.NewString()[:8]},
		}, godClaims(memberID))
		h.RevealEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "var_not_found" {
			t.Fatalf("code = %q, want var_not_found; body=%s", got.Code, rec.Body.String())
		}
	})
}

// TestDeleteEnvVar_ErrorSitesCarryStableWireCodes covers DeleteEnvVar's four
// 403/404 exits. The RowsAffected==0 site keeps its own pre-existing audit
// reason string "not_found" rather than being folded into var_not_found, per
// the "reuse the exact string, do not rename it" rule.
func TestDeleteEnvVar_ErrorSitesCarryStableWireCodes(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedOptimisticFixture(t, pool)
	memberID := seedUser(t, pool)
	outsiderID := seedUser(t, pool)
	h := wireCodeHandler(pool)
	appName := "wire-del-" + uuid.NewString()[:8]

	t.Run("caller with no role is not_a_member", func(t *testing.T) {
		c, rec := newCreateCtx(t, `{}`, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, notAMemberClaims(outsiderID))
		h.DeleteEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "not_a_member" {
			t.Fatalf("code = %q, want not_a_member; body=%s", got.Code, rec.Body.String())
		}
	})

	t.Run("ReadOnly role is read_only_role", func(t *testing.T) {
		c, rec := newCreateCtx(t, `{}`, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, readOnlyClaims(outsiderID, projectID))
		h.DeleteEnvVar(c)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "read_only_role" {
			t.Fatalf("code = %q, want read_only_role; body=%s", got.Code, rec.Body.String())
		}
	})

	t.Run("env belonging to a different project is env_not_in_project", func(t *testing.T) {
		otherProjectID, _ := seedOptimisticFixture(t, pool)
		c, rec := newCreateCtx(t, `{}`, gin.Params{
			{Key: "projectId", Value: otherProjectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "K"},
		}, godClaims(memberID))
		h.DeleteEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "env_not_in_project" {
			t.Fatalf("code = %q, want env_not_in_project; body=%s", got.Code, rec.Body.String())
		}
	})

	t.Run("key never set keeps the pre-existing not_found reason", func(t *testing.T) {
		c, rec := newCreateCtx(t, `{}`, gin.Params{
			{Key: "projectId", Value: projectID.String()},
			{Key: "envId", Value: envID.String()},
			{Key: "appName", Value: appName},
			{Key: "key", Value: "NEVER_SET_" + uuid.NewString()[:8]},
		}, godClaims(memberID))
		h.DeleteEnvVar(c)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if got := decodeWireCode(t, rec.Body.Bytes()); got.Code != "not_found" {
			t.Fatalf("code = %q, want not_found; body=%s", got.Code, rec.Body.String())
		}
	})
}

// TestSetEnvVar_AndDeleteEnvVar_WireCodeMatchesAuditReason is the contract
// the coordinator asked for directly: wherever a site already writes a
// "reason" into audit_events.metadata, the HTTP "code" must be the exact
// same string, so the two vocabularies never drift apart. RevealEnvVar
// already audits every one of its rejects; this pins wire==audit for its
// var_not_found site end-to-end against the real audit_events row.
func TestRevealEnvVar_WireCodeMatchesAuditReason(t *testing.T) {
	pool := testOptimisticPool(t)
	projectID, envID := seedOptimisticFixture(t, pool)
	memberID := seedUser(t, pool)
	h := wireCodeHandler(pool)
	appName := "wire-audit-" + uuid.NewString()[:8]
	key := "NEVER_SET_" + uuid.NewString()[:8]
	t.Cleanup(func() { dropSeededAudit(pool, "EnvVar", key) })

	c, rec := newRevealCtx(t, gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
		{Key: "appName", Value: appName},
		{Key: "key", Value: key},
	}, godClaims(memberID))
	h.RevealEnvVar(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	wireCode := decodeWireCode(t, rec.Body.Bytes()).Code

	var auditReason string
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata->>'reason' FROM audit_events
		  WHERE project_id = $1 AND environment_id = $2 AND action = $3 AND resource_name = $4
		  ORDER BY created_at DESC LIMIT 1`,
		projectID, envID, auditActionRevealEnvVar, key,
	).Scan(&auditReason); err != nil {
		t.Fatalf("expected a RevealEnvVar audit row, got error: %v", err)
	}

	if wireCode != auditReason {
		t.Fatalf("wire code %q != audit reason %q -- the two vocabularies drifted apart", wireCode, auditReason)
	}
}
