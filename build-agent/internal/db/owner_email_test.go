package db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedUserWithEmail inserts one account and returns its id. The password hash and
// display name are required by the schema and irrelevant here: the only field
// under test is the address the ladder is supposed to find.
func seedUserWithEmail(t *testing.T, pool *pgxpool.Pool, username, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	exec(t, pool,
		`INSERT INTO users (id, username, email, password_hash, display_name)
		 VALUES ($1, $2, $3, 'x', $2)`,
		id, username, email)
	return id
}

// TestOwnerEmail_OwnerRung is the case that already worked: projects.owner_id
// points at an account with a real address. It is pinned so the ladder cannot
// be reordered by accident -- rung one must keep winning when it can answer.
func TestOwnerEmail_OwnerRung(t *testing.T) {
	pool := testPool(t)
	projectID, _ := seedProjectEnv(t, pool, "small")
	userID := seedUserWithEmail(t, pool, "owner-"+projectID.String()[:8], "owner@example.com")
	exec(t, pool, `UPDATE projects SET owner_id = $1 WHERE id = $2`, userID, projectID)

	email, source, err := OwnerEmail(context.Background(), pool, projectID)
	if err != nil {
		t.Fatalf("OwnerEmail: %v", err)
	}
	if email != "owner@example.com" || source != RecipientSourceOwner {
		t.Fatalf("got (%q, %q), want (owner@example.com, %q)", email, source, RecipientSourceOwner)
	}
}

// TestOwnerEmail_FallsThroughToMember is the live defect: 11 of 11 failed
// build notifications in the last 30 days were recorded as "no_recipient", and
// the projects behind them (reels-tracker, dada-development-site,
// agent-orchestrator-ui, it-tools) have no owner_id. With rung one alone the
// person who was watching that deploy is told nothing and the journal claims
// they had no address; the Owner/Admin member row here is that address.
func TestOwnerEmail_FallsThroughToMember(t *testing.T) {
	pool := testPool(t)
	projectID, _ := seedProjectEnv(t, pool, "small")
	userID := seedUserWithEmail(t, pool, "member-"+projectID.String()[:8], "member@example.com")
	exec(t, pool,
		`INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, 'Owner')`,
		projectID, userID)

	email, source, err := OwnerEmail(context.Background(), pool, projectID)
	if err != nil {
		t.Fatalf("OwnerEmail: %v", err)
	}
	if email != "member@example.com" || source != RecipientSourceMember {
		t.Fatalf("got (%q, %q), want (member@example.com, %q)", email, source, RecipientSourceMember)
	}
}

// TestOwnerEmail_FallsThroughToPersonalOrg covers the personal-org convention:
// the project carries no owner_id and no member row, but its org_id is the
// user's own username. This is how single-user projects are shaped, so it is
// the rung that matters most for the people deploying alone.
func TestOwnerEmail_FallsThroughToPersonalOrg(t *testing.T) {
	pool := testPool(t)
	projectID, _ := seedProjectEnv(t, pool, "small")
	username := "solo-" + projectID.String()[:8]
	seedUserWithEmail(t, pool, username, "solo@example.com")
	exec(t, pool, `UPDATE projects SET org_id = $1 WHERE id = $2`, username, projectID)

	email, source, err := OwnerEmail(context.Background(), pool, projectID)
	if err != nil {
		t.Fatalf("OwnerEmail: %v", err)
	}
	if email != "solo@example.com" || source != RecipientSourcePersonalOrg {
		t.Fatalf("got (%q, %q), want (solo@example.com, %q)", email, source, RecipientSourcePersonalOrg)
	}
}

// TestOwnerEmail_RejectsKeycloakLocalAndKeepsDescending feeds the synthetic
// address a Keycloak identity gets when it carries no email claim. It is
// non-empty, so a naive resolver "succeeds" into the void and writes a success
// row that lies. Every rung must treat it as no answer and keep descending --
// here the personal-org rung holds the real mailbox.
func TestOwnerEmail_RejectsKeycloakLocalAndKeepsDescending(t *testing.T) {
	pool := testPool(t)
	projectID, _ := seedProjectEnv(t, pool, "small")
	ghost := seedUserWithEmail(t, pool, "ghost-"+projectID.String()[:8], uuid.New().String()+"@keycloak.local")
	exec(t, pool, `UPDATE projects SET owner_id = $1 WHERE id = $2`, ghost, projectID)
	username := "real-" + projectID.String()[:8]
	seedUserWithEmail(t, pool, username, "real@example.com")
	exec(t, pool, `UPDATE projects SET org_id = $1 WHERE id = $2`, username, projectID)

	email, source, err := OwnerEmail(context.Background(), pool, projectID)
	if err != nil {
		t.Fatalf("OwnerEmail: %v", err)
	}
	if email != "real@example.com" || source != RecipientSourcePersonalOrg {
		t.Fatalf("got (%q, %q), want (real@example.com, %q)", email, source, RecipientSourcePersonalOrg)
	}
}

// TestOwnerEmail_NoRungAnswers keeps "nobody was reachable" a distinguishable
// outcome rather than an error or a guess, because that is the state the
// no_recipient audit row is meant to record honestly.
func TestOwnerEmail_NoRungAnswers(t *testing.T) {
	pool := testPool(t)
	projectID, _ := seedProjectEnv(t, pool, "small")

	email, source, err := OwnerEmail(context.Background(), pool, projectID)
	if err != nil {
		t.Fatalf("OwnerEmail: %v", err)
	}
	if email != "" || source != "" {
		t.Fatalf("got (%q, %q), want empty", email, source)
	}
}

// TestRecordBuildNotify_StoresRecipientSource pins the field that was missing
// while the backend alert path already had it: every build-notification row
// must say which rung produced the address, so "who did we pick" is answerable
// from the journal alone -- and it must still carry no address (152-FZ).
func TestRecordBuildNotify_StoresRecipientSource(t *testing.T) {
	pool := testPool(t)
	projectID, envID := seedProjectEnv(t, pool, "small")

	RecordBuildNotify(context.Background(), pool, projectID, envID, uuid.New(), "shop", "success", RecipientSourcePersonalOrg, "", nil)

	_, meta, _ := readLastBuildNotify(t, pool, projectID)
	if meta["recipient_source"] != RecipientSourcePersonalOrg {
		t.Fatalf("recipient_source = %v, want %q", meta["recipient_source"], RecipientSourcePersonalOrg)
	}
	for _, k := range []string{"to", "email", "recipient"} {
		if _, ok := meta[k]; ok {
			t.Fatalf("metadata must not carry an address, found %q", k)
		}
	}
}
