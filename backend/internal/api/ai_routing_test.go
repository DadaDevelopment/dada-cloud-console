package api

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/config"
)

// Billing attribution for AI routing, against a real database.
//
// This is money: what these tests pin is that nobody is charged by accident.
// Migration 079 gave every project a free fallback onto the platform's provider
// key, and that must stay free forever unless the project explicitly asked to
// be billed for routing. The three conditions in aiBilledUSD are the whole
// safety property, so each one is exercised on its own.
func testAIRoutingPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping AI routing DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedAIRoutingProject(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var projectID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		"ai-routing-test-"+uuid.NewString()[:8],
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		dropSeededProject(pool, projectID)
	})
	return projectID
}

func TestAIKeyOwnerFor(t *testing.T) {
	pool := testAIRoutingPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{AIRoutingMarkup: 1.3}}
	ctx := context.Background()
	projectID := seedAIRoutingProject(t, pool)

	if got := h.aiKeyOwnerFor(ctx, projectID, "openai", ""); got != aiKeyOwnerPlatform {
		t.Fatalf("no credential row: key owner = %q, want %q", got, aiKeyOwnerPlatform)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO ai_provider_credentials (project_id, provider, api_key_encrypted)
		 VALUES ($1, 'openai', $2)`,
		projectID, []byte("enc"),
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	if got := h.aiKeyOwnerFor(ctx, projectID, "openai", ""); got != aiKeyOwnerBYOK {
		t.Fatalf("with credential row: key owner = %q, want %q", got, aiKeyOwnerBYOK)
	}
	if got := h.aiKeyOwnerFor(ctx, projectID, "anthropic", ""); got != aiKeyOwnerPlatform {
		t.Fatalf("credential is per provider: anthropic owner = %q, want %q", got, aiKeyOwnerPlatform)
	}
	if got := h.aiKeyOwnerFor(ctx, projectID, "openai", aiKeyOwnerPlatform); got != aiKeyOwnerPlatform {
		t.Fatalf("gateway-declared owner ignored: got %q", got)
	}
	if got := h.aiKeyOwnerFor(ctx, projectID, "", ""); got != aiKeyOwnerUnknown {
		t.Fatalf("no provider: key owner = %q, want %q", got, aiKeyOwnerUnknown)
	}
}

func TestAIBilledUSD_OnlyBillsOptedInPlatformRouting(t *testing.T) {
	pool := testAIRoutingPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{AIRoutingMarkup: 1.3}}
	ctx := context.Background()
	projectID := seedAIRoutingProject(t, pool)

	if got := h.aiRoutingMode(ctx, projectID); got != aiRoutingModeBYOK {
		t.Fatalf("a project with no settings row: mode = %q, want %q", got, aiRoutingModeBYOK)
	}
	if got := h.aiBilledUSD(ctx, projectID, aiKeyOwnerPlatform, 1.0); got != 0 {
		t.Fatalf("free platform fallback billed %v, want 0", got)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO ai_routing_settings (project_id, mode) VALUES ($1, 'platform')`, projectID,
	); err != nil {
		t.Fatalf("opt into platform routing: %v", err)
	}

	if got := h.aiBilledUSD(ctx, projectID, aiKeyOwnerPlatform, 1.0); got != 1.3 {
		t.Fatalf("opted-in platform call billed %v, want 1.3", got)
	}
	if got := h.aiBilledUSD(ctx, projectID, aiKeyOwnerBYOK, 1.0); got != 0 {
		t.Fatalf("call on the customer's own key billed %v, want 0", got)
	}
	if got := h.aiBilledUSD(ctx, projectID, aiKeyOwnerUnknown, 1.0); got != 0 {
		t.Fatalf("unattributed call billed %v, want 0", got)
	}
	if got := h.aiBilledUSD(ctx, projectID, aiKeyOwnerPlatform, 0); got != 0 {
		t.Fatalf("zero-cost call billed %v, want 0", got)
	}
}

func TestAIUsageLedgerRecordsAttributionIdempotently(t *testing.T) {
	pool := testAIRoutingPool(t)
	ctx := context.Background()
	projectID := seedAIRoutingProject(t, pool)
	requestID := "test-" + uuid.NewString()

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM agent_token_usage WHERE platform_request_id = $1`, requestID)
	})

	insert := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_token_usage
				(source, project_id, model, provider, prompt_tokens, completion_tokens,
				 total_tokens, cost_usd, platform_request_id, key_owner, billed_usd)
			VALUES ('gateway', $1, 'gpt-4o-mini', 'openai', 10, 20, 30, 0.5, $2, 'platform', 0.65)
			ON CONFLICT (platform_request_id) WHERE platform_request_id IS NOT NULL DO NOTHING
		`, projectID, requestID); err != nil {
			t.Fatalf("insert usage row: %v", err)
		}
	}
	insert()
	insert()

	var rows int
	var keyOwner string
	var billed float64
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*), MIN(key_owner), COALESCE(SUM(billed_usd), 0)::float8
		  FROM agent_token_usage WHERE platform_request_id = $1`, requestID,
	).Scan(&rows, &keyOwner, &billed); err != nil {
		t.Fatalf("read back usage row: %v", err)
	}
	if rows != 1 {
		t.Fatalf("retried callback wrote %d rows, want 1", rows)
	}
	if keyOwner != aiKeyOwnerPlatform || billed != 0.65 {
		t.Fatalf("stored attribution = (%q, %v), want (%q, 0.65)", keyOwner, billed, aiKeyOwnerPlatform)
	}
}
