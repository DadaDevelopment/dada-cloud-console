package api

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testAICredPool connects to the ephemeral integration database, skipping the
// whole test when TEST_DATABASE_URL is unset so `go test` stays green offline.
func testAICredPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping ai-credential DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAICredProject creates a throwaway project, cleaned up when the test ends.
func seedAICredProject(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	name := "aicred-test-" + uuid.NewString()[:8]
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, display_name) VALUES ($1, $1) RETURNING id`,
		name).Scan(&id); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, id)
	})
	return id
}

// TestLoadAIProviderCredential_ProjectKeyBeatsPlatformKey pins the precedence
// migration 079 exists for. A project that brought its own key must keep being
// billed to that key, and a project that brought nothing must still be served
// by the platform's -- the shared free-tier keys behind the fast/medium/smart
// aliases are worthless if a project has to configure something to reach them,
// and actively harmful if they silently override a customer's own key.
func TestLoadAIProviderCredential_ProjectKeyBeatsPlatformKey(t *testing.T) {
	pool := testAICredPool(t)
	ctx := context.Background()
	projectID := seedAICredProject(t, pool)
	provider := "testprov-" + uuid.NewString()[:8]

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM ai_provider_credentials WHERE provider = $1`, provider)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO ai_provider_credentials (project_id, provider, api_base, api_key_encrypted)
		 VALUES (NULL, $1, 'https://platform.example', '\x706c6174666f726d'::bytea)`,
		provider); err != nil {
		t.Fatalf("insert platform credential: %v", err)
	}

	enc, apiBase, err := loadAIProviderCredential(ctx, pool, projectID, provider)
	if err != nil {
		t.Fatalf("project with no key of its own got no credential: %v", err)
	}
	if string(enc) != "platform" {
		t.Fatalf("enc=%q want the platform key", enc)
	}
	if apiBase == nil || *apiBase != "https://platform.example" {
		t.Fatalf("apiBase=%v want the platform api_base", apiBase)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO ai_provider_credentials (project_id, provider, api_base, api_key_encrypted)
		 VALUES ($1, $2, NULL, '\x62796f6b'::bytea)`,
		projectID, provider); err != nil {
		t.Fatalf("insert BYOK credential: %v", err)
	}

	enc, apiBase, err = loadAIProviderCredential(ctx, pool, projectID, provider)
	if err != nil {
		t.Fatalf("load after BYOK insert: %v", err)
	}
	if string(enc) != "byok" {
		t.Fatalf("enc=%q want the project's own key to win over the platform key", enc)
	}
	if apiBase != nil {
		t.Fatalf("apiBase=%v want nil: the BYOK row's NULL must not fall through to the platform row's value", *apiBase)
	}

	other := seedAICredProject(t, pool)
	enc, _, err = loadAIProviderCredential(ctx, pool, other, provider)
	if err != nil {
		t.Fatalf("second project load: %v", err)
	}
	if string(enc) != "platform" {
		t.Fatalf("enc=%q want the platform key: one project's BYOK must not leak to another", enc)
	}

	if _, err := pool.Exec(ctx,
		`DELETE FROM ai_provider_credentials WHERE project_id = $1 AND provider = $2`,
		projectID, provider); err != nil {
		t.Fatalf("delete BYOK credential: %v", err)
	}
	enc, _, err = loadAIProviderCredential(ctx, pool, projectID, provider)
	if err != nil {
		t.Fatalf("load after BYOK delete: %v", err)
	}
	if string(enc) != "platform" {
		t.Fatalf("enc=%q want the platform key back after the project removed its own", enc)
	}
}

// TestLoadAIProviderCredential_NoRowAnywhere keeps the 404 path intact: a
// provider the platform holds no key for must still be reported as missing
// rather than resolving to some other provider's row.
func TestLoadAIProviderCredential_NoRowAnywhere(t *testing.T) {
	pool := testAICredPool(t)
	projectID := seedAICredProject(t, pool)

	_, _, err := loadAIProviderCredential(context.Background(), pool, projectID,
		"testprov-absent-"+uuid.NewString()[:8])
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("err=%v want pgx.ErrNoRows so the handler answers 404", err)
	}
}

// TestPlatformCredentialIsUniquePerProvider proves the partial unique index
// from migration 079 is doing its job. UNIQUE (project_id, provider) does not
// constrain the platform rows at all, because SQL treats NULLs as distinct --
// without the partial index a second platform key for the same provider would
// insert happily and the lookup would start returning an arbitrary one of them.
func TestPlatformCredentialIsUniquePerProvider(t *testing.T) {
	pool := testAICredPool(t)
	ctx := context.Background()
	provider := "testprov-uniq-" + uuid.NewString()[:8]

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM ai_provider_credentials WHERE provider = $1`, provider)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO ai_provider_credentials (project_id, provider, api_key_encrypted)
		 VALUES (NULL, $1, '\x6f6e65'::bytea)`, provider); err != nil {
		t.Fatalf("insert first platform credential: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO ai_provider_credentials (project_id, provider, api_key_encrypted)
		 VALUES (NULL, $1, '\x74776f'::bytea)`, provider); err == nil {
		t.Fatal("a second platform credential for the same provider was accepted")
	}
}
