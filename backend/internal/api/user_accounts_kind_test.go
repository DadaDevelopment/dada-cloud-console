package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSignupCustomerCountIgnoresNonCustomers pins the milestone number the owner
// reads on every signup mail. A raw count(*) over users had it running roughly
// half ahead of reality: only the customer row may move it, and the probes,
// the Keycloak shells and the seeded platform actor may not.
func TestSignupCustomerCountIgnoresNonCustomers(t *testing.T) {
	pool := testAuditPool(t)
	ctx := context.Background()

	before := signupCustomerCount(ctx, pool)
	if before < 0 {
		t.Fatalf("baseline count unavailable")
	}

	suffix := uuid.NewString()[:8]
	seedKindUser(t, pool, "cohort-c-"+suffix, "cohort-c-"+suffix+"@example.test")
	seedKindUser(t, pool, "cohort-s-"+suffix, "cohort-s-"+suffix+"@keycloak.local")
	seedKindUser(t, pool, "service-account-"+suffix, "sa-"+suffix+"@example.test")
	seedKindUser(t, pool, "cohort-i-"+suffix, "cohort-i-"+suffix+"@dada-tuda.ru")

	if got := signupCustomerCount(ctx, pool); got != before+1 {
		t.Fatalf("only the customer row may move the milestone: %d -> %d", before, got)
	}
}

// TestPlatformActorIsItsOwnKind guards migration 078. The seeded system actor
// carries every platform-performed audit row, and reading a customer's path
// means being able to tell that work apart from an e2e probe's -- both of which
// used to answer 'synthetic'.
func TestPlatformActorIsItsOwnKind(t *testing.T) {
	pool := testAuditPool(t)

	var kind string
	if err := pool.QueryRow(context.Background(),
		`SELECT account_kind FROM user_accounts WHERE id = $1`, systemDeployActorID,
	).Scan(&kind); err != nil {
		t.Fatalf("read the system actor's kind: %v", err)
	}
	if kind != "platform" {
		t.Fatalf("the system actor must be its own cohort, got %q", kind)
	}
}

func seedKindUser(t *testing.T, pool *pgxpool.Pool, username, email string) {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, email, password_hash, display_name) VALUES ($1, $2, '', $1) RETURNING id`,
		username, email,
	).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id)
	})
}
