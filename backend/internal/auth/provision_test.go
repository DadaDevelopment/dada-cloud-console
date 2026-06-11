package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// fakeRow implements pgx.Row by copying a fixed id (or returning a fixed error).
type fakeRow struct {
	id  uuid.UUID
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) > 0 {
		if p, ok := dest[0].(*uuid.UUID); ok {
			*p = r.id
		}
	}
	return nil
}

// fakeQuerier records each QueryRow call and replies from a scripted queue.
type fakeQuerier struct {
	calls   []string // SQL of each call, in order
	replies []pgx.Row
	i       int
}

func (q *fakeQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	q.calls = append(q.calls, sql)
	r := q.replies[q.i]
	q.i++
	return r
}

// pgUniqueErr is a stand-in implementing SQLState() == 23505, matching how pgx
// surfaces a unique-constraint violation.
type pgUniqueErr struct{}

func (pgUniqueErr) Error() string    { return "ERROR: duplicate key value (SQLSTATE 23505)" }
func (pgUniqueErr) SQLState() string { return "23505" }

func TestResolveUser_UpsertBySub(t *testing.T) {
	want := uuid.New()
	q := &fakeQuerier{replies: []pgx.Row{fakeRow{id: want}}}

	got, err := ResolveUser(context.Background(), q, &KeycloakClaims{
		Subject: "sub-1", PreferredUsername: "alice", Email: "alice@x.io", Name: "Alice",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != want {
		t.Errorf("id = %v want %v", got, want)
	}
	if len(q.calls) != 1 {
		t.Fatalf("expected 1 query (upsert-by-sub), got %d", len(q.calls))
	}
}

func TestResolveUser_UsernameEmailCollisionLinks(t *testing.T) {
	want := uuid.New()
	q := &fakeQuerier{replies: []pgx.Row{
		fakeRow{err: pgUniqueErr{}}, // upsert-by-sub hits username/email collision
		fakeRow{id: want},           // link-existing succeeds
	}}

	got, err := ResolveUser(context.Background(), q, &KeycloakClaims{
		Subject: "sub-2", PreferredUsername: "legacy", Email: "legacy@x.io", Name: "Legacy",
	})
	if err != nil {
		t.Fatalf("resolve with collision: %v", err)
	}
	if got != want {
		t.Errorf("id = %v want %v", got, want)
	}
	if len(q.calls) != 2 {
		t.Fatalf("expected upsert then link (2 queries), got %d", len(q.calls))
	}
}

func TestResolveUser_NonUniqueErrorPropagates(t *testing.T) {
	q := &fakeQuerier{replies: []pgx.Row{fakeRow{err: errors.New("connection refused")}}}
	if _, err := ResolveUser(context.Background(), q, &KeycloakClaims{Subject: "sub-3"}); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestResolveUser_MissingSubject(t *testing.T) {
	q := &fakeQuerier{}
	if _, err := ResolveUser(context.Background(), q, &KeycloakClaims{}); err == nil {
		t.Fatal("expected error for empty subject")
	}
}

func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(pgUniqueErr{}) {
		t.Error("SQLState 23505 should be detected via interface")
	}
	if !isUniqueViolation(errors.New("oops (SQLSTATE 23505)")) {
		t.Error("23505 in message should be detected")
	}
	if isUniqueViolation(errors.New("other")) {
		t.Error("non-23505 must be false")
	}
}
