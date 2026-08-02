package api

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// recordingQuerier captures the SQL and arguments a resolution issues without
// touching a database, so query-shape invariants can be pinned in a unit test.
type recordingQuerier struct {
	sql  string
	args []any
}

func (r *recordingQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	r.sql = sql
	r.args = args
	return noRow{}
}

// noRow is a pgx.Row whose Scan always reports "not found", standing in for a
// token that resolves to nothing.
type noRow struct{}

func (noRow) Scan(...any) error { return pgx.ErrNoRows }

func TestGenerateIdentityToken_FormatAndHash(t *testing.T) {
	plaintext, hash, prefix, err := generateIdentityToken()
	if err != nil {
		t.Fatalf("generateIdentityToken: %v", err)
	}
	if !strings.HasPrefix(plaintext, identityTokenPrefix) {
		t.Fatalf("plaintext=%q want %q prefix", plaintext, identityTokenPrefix)
	}
	if len(plaintext) != len(identityTokenPrefix)+48 {
		t.Fatalf("plaintext len=%d want %d", len(plaintext), len(identityTokenPrefix)+48)
	}
	if prefix != plaintext[:identityTokenPrefixLen] {
		t.Fatalf("prefix=%q want first %d chars of %q", prefix, identityTokenPrefixLen, plaintext)
	}
	if hash != hashIdentityToken(plaintext) {
		t.Fatalf("hash=%q does not match hashIdentityToken(plaintext)=%q", hash, hashIdentityToken(plaintext))
	}

	plaintext2, hash2, _, err := generateIdentityToken()
	if err != nil {
		t.Fatalf("generateIdentityToken (2nd): %v", err)
	}
	if plaintext2 == plaintext || hash2 == hash {
		t.Fatal("two generateIdentityToken calls produced the same token")
	}
}

// TestIdentityTokenPrefix_RoutableAndDisjoint pins the routing invariant the
// gateway plugin depends on: a console-minted identity token must be
// recognisable by prefix alone, must not be sent to user-service introspection,
// and must not be confusable with an AI Gateway key -- the two resolve through
// different tables and a collision would silently introspect the wrong one.
func TestIdentityTokenPrefix_RoutableAndDisjoint(t *testing.T) {
	if !strings.HasPrefix(identityTokenPrefix, "sk-dada-") {
		t.Fatalf("identityTokenPrefix=%q must stay inside the sk-dada- family", identityTokenPrefix)
	}
	if identityTokenPrefix == "sk-dada-" {
		t.Fatal("identityTokenPrefix must be strictly longer than sk-dada- or it cannot be told apart")
	}
	if strings.HasPrefix(identityTokenPrefix, aiKeyPrefix) || strings.HasPrefix(aiKeyPrefix, identityTokenPrefix) {
		t.Fatalf("identityTokenPrefix=%q and aiKeyPrefix=%q must not prefix each other", identityTokenPrefix, aiKeyPrefix)
	}
}

// TestIdentityHasScope covers the whole authorization surface every platform
// service shares: a service checks the one scope it needs, and no scope string
// may be satisfied by a prefix or substring of another.
func TestIdentityHasScope(t *testing.T) {
	cases := []struct {
		scopes string
		want   string
		ok     bool
	}{
		{"ai:chat ai:embeddings", "ai:chat", true},
		{"ai:chat ai:embeddings", "ai:embeddings", true},
		{"ai:chat ai:embeddings", "pay:charge", false},
		{"ai:chat", "ai:cha", false},
		{"ai:chat", "ai:chat:write", false},
		{"  ai:chat   pay:charge ", "pay:charge", true},
		{"", "ai:chat", false},
	}
	for _, tc := range cases {
		if got := identityHasScope(tc.scopes, tc.want); got != tc.ok {
			t.Fatalf("identityHasScope(%q, %q)=%v want %v", tc.scopes, tc.want, got, tc.ok)
		}
	}
}

// TestResolveIdentityToken_MissingIdentityIsInvalid is the regression test for
// 2026-08-02, written before the resolution path is trusted: a token whose
// identity row is gone must resolve to "no rows" -- never to a default project.
// The query is asserted rather than executed, because the failure it guards is
// a WHERE clause that silently widens, not a runtime error.
func TestResolveIdentityToken_JoinsOnlyLiveRows(t *testing.T) {
	q := &recordingQuerier{}
	_, _ = resolveIdentityToken(t.Context(), q, identityTokenPrefix+"deadbeef")

	sql := strings.Join(strings.Fields(q.sql), " ")
	for _, must := range []string{
		"FROM service_identity_tokens t",
		"JOIN service_identities i ON i.id = t.identity_id",
		"t.revoked_at IS NULL",
		"i.revoked_at IS NULL",
	} {
		if !strings.Contains(sql, must) {
			t.Fatalf("resolution query lost %q:\n%s", must, sql)
		}
	}
	if strings.Contains(sql, "JOIN projects p ON") && !strings.Contains(sql, "LEFT JOIN projects p ON") {
		t.Fatalf("projects must be LEFT JOINed: an identity outlives its project, and an inner join\n"+
			"would make a project-less identity introspect as invalid:\n%s", sql)
	}
	if q.args[0] != hashIdentityToken(identityTokenPrefix+"deadbeef") {
		t.Fatalf("token looked up by %v, want its sha256 hash", q.args[0])
	}
}
