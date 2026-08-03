package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/dada-tuda/console/backend/internal/config"
)

// usageTestFixture creates a project and a ServiceIdentity living in it, and
// returns both plus a handler wired to the test pool. The Handler is built as
// a literal rather than through NewHandler: the constructor dials redis and
// starts background clients, none of which this path touches.
func usageTestFixture(t *testing.T, appName string) (*Handler, uuid.UUID, uuid.UUID) {
	t.Helper()
	pool := testAdvisoryPool(t)
	ctx := context.Background()
	h := &Handler{pool: pool, cfg: &config.Config{}}

	suffix := uuid.NewString()[:8]
	var projectID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"usage-test-"+suffix,
	).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}

	var identityID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO service_identities (app_name, project_id, display_name)
		 VALUES ($1, $2, $1) RETURNING id`,
		appName+"-"+suffix, projectID,
	).Scan(&identityID); err != nil {
		t.Fatalf("create identity: %v", err)
	}

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM agent_token_usage WHERE project_id = $1`, projectID)
		_, _ = pool.Exec(bg, `DELETE FROM service_identities WHERE id = $1`, identityID)
		_, _ = pool.Exec(bg, `DELETE FROM projects WHERE id = $1`, projectID)
	})

	return h, projectID, identityID
}

// postUsage drives AIRecordUsage through gin exactly as the gateway callback
// does, so the binding of identity_id is exercised and not bypassed.
func postUsage(t *testing.T, h *Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal usage body: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/internal/ai/usage/record", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	h.AIRecordUsage(c)
	return w
}

// TestRecordUsageAttributesTheIdentityThatPaid is the point of ADR-021 phase
// 4: the ledger row has to name the app, not just the project it lives in.
func TestRecordUsageAttributesTheIdentityThatPaid(t *testing.T) {
	h, projectID, identityID := usageTestFixture(t, "attributed")
	prid := "preq_" + uuid.NewString()[:12]

	w := postUsage(t, h, map[string]any{
		"project_id":          projectID,
		"provider":            "openrouter",
		"model":               "or-gpt-41-mini",
		"prompt_tokens":       10,
		"completion_tokens":   5,
		"total_tokens":        15,
		"cost_usd":            0.01,
		"platform_request_id": prid,
		"identity_id":         identityID.String(),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("record usage: status %d body %s", w.Code, w.Body.String())
	}

	var stored *uuid.UUID
	if err := h.pool.QueryRow(context.Background(),
		`SELECT identity_id FROM agent_token_usage WHERE platform_request_id = $1`, prid,
	).Scan(&stored); err != nil {
		t.Fatalf("read back usage row: %v", err)
	}
	if stored == nil || *stored != identityID {
		t.Fatalf("expected identity_id %s on the ledger row, got %v", identityID, stored)
	}
}

// TestRecordUsageKeepsTheRowWhenTheIdentityIsGone pins the reason identity_id
// goes in through a subselect. The gateway's callback fires after the response
// has already left, so an app deleted in between would raise a foreign-key
// violation on a plain parameter -- and the whole ledger row, cost included,
// would be lost to save an attribution that no longer exists.
func TestRecordUsageKeepsTheRowWhenTheIdentityIsGone(t *testing.T) {
	h, projectID, _ := usageTestFixture(t, "vanished")
	prid := "preq_" + uuid.NewString()[:12]

	w := postUsage(t, h, map[string]any{
		"project_id":          projectID,
		"provider":            "openrouter",
		"model":               "or-gpt-41-mini",
		"total_tokens":        7,
		"cost_usd":            0.02,
		"platform_request_id": prid,
		"identity_id":         uuid.NewString(),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("record usage with a dead identity must still succeed: status %d body %s", w.Code, w.Body.String())
	}

	var stored *uuid.UUID
	var cost float64
	if err := h.pool.QueryRow(context.Background(),
		`SELECT identity_id, cost_usd FROM agent_token_usage WHERE platform_request_id = $1`, prid,
	).Scan(&stored, &cost); err != nil {
		t.Fatalf("read back usage row: %v", err)
	}
	if stored != nil {
		t.Fatalf("an unknown identity must degrade to NULL, got %v", *stored)
	}
	if cost != 0.02 {
		t.Fatalf("cost must survive the lost attribution, got %v", cost)
	}
}

// TestRecordUsageWithoutIdentityStaysNull covers the console-chat and
// project-key callers: no identity is sent, and none may be invented.
func TestRecordUsageWithoutIdentityStaysNull(t *testing.T) {
	h, projectID, _ := usageTestFixture(t, "anonymous")
	prid := "preq_" + uuid.NewString()[:12]

	w := postUsage(t, h, map[string]any{
		"project_id":          projectID,
		"provider":            "openai",
		"model":               "gpt-4o",
		"total_tokens":        3,
		"platform_request_id": prid,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("record usage: status %d body %s", w.Code, w.Body.String())
	}

	var stored *uuid.UUID
	if err := h.pool.QueryRow(context.Background(),
		`SELECT identity_id FROM agent_token_usage WHERE platform_request_id = $1`, prid,
	).Scan(&stored); err != nil {
		t.Fatalf("read back usage row: %v", err)
	}
	if stored != nil {
		t.Fatalf("expected NULL identity_id for a caller that sent none, got %v", *stored)
	}
}

// TestUsageByAppSplitsProjectSpendPerApp is the question phase 4 exists to
// answer: two apps sharing one project's credential must show up as two
// numbers, and the spend nobody can be charged for must not be folded into
// either of them.
func TestUsageByAppSplitsProjectSpendPerApp(t *testing.T) {
	h, projectID, identityA := usageTestFixture(t, "app-a")
	ctx := context.Background()

	var identityB uuid.UUID
	if err := h.pool.QueryRow(ctx,
		`INSERT INTO service_identities (app_name, project_id, display_name)
		 VALUES ($1, $2, $1) RETURNING id`,
		"app-b-"+uuid.NewString()[:8], projectID,
	).Scan(&identityB); err != nil {
		t.Fatalf("create second identity: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = h.pool.Exec(bg, `DELETE FROM agent_token_usage WHERE identity_id = $1`, identityB)
		_, _ = h.pool.Exec(bg, `DELETE FROM service_identities WHERE id = $1`, identityB)
	})

	for _, row := range []struct {
		identity any
		cost     float64
	}{
		{identityA, 0.10},
		{identityA, 0.05},
		{identityB, 0.02},
		{nil, 1.00},
	} {
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO agent_token_usage
				(source, project_id, model, provider, prompt_tokens, completion_tokens,
				 total_tokens, cost_usd, platform_request_id, identity_id)
			VALUES ('gateway', $1, 'or-gpt-41-mini', 'openrouter', 1, 1, 2, $2, $3, $4)`,
			projectID, row.cost, "preq_"+uuid.NewString()[:12], row.identity,
		); err != nil {
			t.Fatalf("seed usage row: %v", err)
		}
	}

	now := time.Now().UTC()
	apps, err := h.aiUsageByApp(ctx, projectID, now.AddDate(0, 0, -1), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("aiUsageByApp: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("expected exactly the two identity-backed apps, got %d: %+v", len(apps), apps)
	}
	if apps[0].IdentityID != identityA.String() || apps[0].CostUSD != 0.15 {
		t.Fatalf("expected app-a first with 0.15, got %+v", apps[0])
	}
	if apps[1].IdentityID != identityB.String() || apps[1].CostUSD != 0.02 {
		t.Fatalf("expected app-b second with 0.02, got %+v", apps[1])
	}
	if apps[0].AppName == "" || apps[1].AppName == "" {
		t.Fatalf("every app row must carry a name, got %+v", apps)
	}
}
