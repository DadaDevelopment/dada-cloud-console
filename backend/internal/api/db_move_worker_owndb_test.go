package api

import (
	"strings"
	"testing"
)

// TestOwnDatabaseIsRecognisedByName covers the one decision that keeps the
// console's own move from deadlocking: while the router holds a database still,
// every client of it waits, and the console is a client of cloud-console. A
// bookkeeping write through the console pool would queue behind the PAUSE the
// worker itself is holding, so the worker has to notice that the database being
// moved is the one under its feet and write to the target shard directly.
func TestOwnDatabaseIsRecognisedByName(t *testing.T) {
	pool := testOptimisticPool(t)
	w := &dbMoveWorker{h: &Handler{pool: pool}}

	own := pool.Config().ConnConfig.Database
	if !w.ownDatabase(own) {
		t.Fatalf("ownDatabase(%q) = false, want true", own)
	}
	if !w.ownDatabase(strings.ToUpper(own)) {
		t.Fatalf("ownDatabase is case sensitive; Postgres database names compared here are not")
	}
	if w.ownDatabase(own + "-copy") {
		t.Fatal("a different database was taken for the console's own")
	}
}

func TestOwnDatabaseWithoutPoolIsFalse(t *testing.T) {
	w := &dbMoveWorker{}
	if w.ownDatabase("cloud-console") {
		t.Fatal("a worker with no pool claimed the database as its own")
	}
}
