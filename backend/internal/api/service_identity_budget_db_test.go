package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// budgetFixture puts a live token on the identity created by usageTestFixture,
// so introspection can be driven the way the gateway drives it -- by token,
// not by id.
func budgetFixture(t *testing.T, appName string) (*Handler, uuid.UUID, uuid.UUID, string) {
	t.Helper()
	h, projectID, identityID := usageTestFixture(t, appName)

	plaintext, hash, prefix, err := generateIdentityToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if _, err := h.pool.Exec(context.Background(),
		`INSERT INTO service_identity_tokens (identity_id, token_hash, token_prefix)
		 VALUES ($1, $2, $3)`,
		identityID, hash, prefix,
	); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(),
			`DELETE FROM service_identity_tokens WHERE identity_id = $1`, identityID)
	})

	return h, projectID, identityID, plaintext
}

// setIdentityBudget writes the monthly ceiling straight onto the identity.
func setIdentityBudget(t *testing.T, h *Handler, identityID uuid.UUID, usd float64) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE service_identities SET ai_monthly_limit_usd = $1 WHERE id = $2`,
		usd, identityID,
	); err != nil {
		t.Fatalf("set budget: %v", err)
	}
}

// seedSpend inserts a ledger row for the identity, offset from now by a SQL
// interval so a test can put spend in a previous month.
func seedSpend(t *testing.T, h *Handler, projectID, identityID uuid.UUID, cost float64, ago string) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(), `
		INSERT INTO agent_token_usage
			(source, project_id, model, provider, prompt_tokens, completion_tokens,
			 total_tokens, cost_usd, platform_request_id, identity_id, created_at)
		VALUES ('gateway', $1, 'or-gpt-41-mini', 'openrouter', 1, 1, 2, $2, $3, $4,
		        now() - $5::interval)`,
		projectID, cost, "preq_"+uuid.NewString()[:12], identityID, ago,
	); err != nil {
		t.Fatalf("seed spend: %v", err)
	}
}

// introspectToken drives the gateway-facing introspection and decodes it.
func introspectToken(t *testing.T, h *Handler, token string) aiKeyIntrospectResponse {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/ai/key/introspect", nil)
	h.introspectIdentityAsAIKey(c, token)
	if w.Code != http.StatusOK {
		t.Fatalf("introspect: status %d body %s", w.Code, w.Body.String())
	}
	var out aiKeyIntrospectResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode introspect response: %v (body %s)", err, w.Body.String())
	}
	return out
}

// TestIntrospectRefusesAnIdentityOverItsMonthlyBudget is the ceiling doing its
// job: the credential is perfectly valid, and the call is still refused.
func TestIntrospectRefusesAnIdentityOverItsMonthlyBudget(t *testing.T) {
	h, projectID, identityID, token := budgetFixture(t, "overbudget")
	setIdentityBudget(t, h, identityID, 0.10)
	seedSpend(t, h, projectID, identityID, 0.09, "1 hour")
	seedSpend(t, h, projectID, identityID, 0.06, "30 minutes")

	resp := introspectToken(t, h, token)
	if resp.Valid {
		t.Fatal("an identity past its monthly ceiling must not resolve")
	}
	if resp.Reason != aiIntrospectReasonBudget {
		t.Fatalf("reason=%q, want %q -- without it the gateway reports a good token as invalid",
			resp.Reason, aiIntrospectReasonBudget)
	}
}

// TestIntrospectAllowsAnIdentityUnderItsBudget guards the other half: a ceiling
// that rejects while there is still room would take every budgeted app offline.
func TestIntrospectAllowsAnIdentityUnderItsBudget(t *testing.T) {
	h, projectID, identityID, token := budgetFixture(t, "underbudget")
	setIdentityBudget(t, h, identityID, 1.00)
	seedSpend(t, h, projectID, identityID, 0.15, "2 hours")

	resp := introspectToken(t, h, token)
	if !resp.Valid {
		t.Fatalf("identity under its ceiling must resolve, got reason=%q", resp.Reason)
	}
	if resp.IdentityID != identityID.String() {
		t.Fatalf("identity_id=%q, want %q", resp.IdentityID, identityID)
	}
}

// TestBudgetIgnoresPreviousMonths pins the window. A ceiling that counted
// forever would let one expensive month mute an app permanently, and the
// operator would have no way to tell that from a broken credential.
func TestBudgetIgnoresPreviousMonths(t *testing.T) {
	h, projectID, identityID, token := budgetFixture(t, "lastmonth")
	setIdentityBudget(t, h, identityID, 0.10)
	seedSpend(t, h, projectID, identityID, 5.00, "40 days")

	resp := introspectToken(t, h, token)
	if !resp.Valid {
		t.Fatalf("spend from a previous month must not count, got reason=%q", resp.Reason)
	}
}

// TestNoBudgetMeansNoCeiling covers every identity that predates this feature:
// a migration must not start refusing live traffic.
func TestNoBudgetMeansNoCeiling(t *testing.T) {
	h, projectID, identityID, token := budgetFixture(t, "unbudgeted")
	seedSpend(t, h, projectID, identityID, 99.00, "1 hour")

	resp := introspectToken(t, h, token)
	if !resp.Valid {
		t.Fatalf("an identity without a ceiling must resolve at any spend, got reason=%q", resp.Reason)
	}
}
