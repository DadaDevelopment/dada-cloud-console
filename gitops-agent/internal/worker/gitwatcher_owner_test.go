package worker

import (
	"testing"

	"github.com/google/uuid"
)

// TestOwnerUnresolvedWarning_ClientProjectNoOwner is the RED-proof case for the
// silent-drop bug: a brand-new client project synced from a manifest with no
// resolvable owner must be flagged so it never again sits at owner_id NULL
// with nobody knowing (the client-a-prod incident this fix addresses).
func TestOwnerUnresolvedWarning_ClientProjectNoOwner(t *testing.T) {
	if !ownerUnresolvedWarning(true, nil, "client") {
		t.Fatal("ownerUnresolvedWarning = false, want true for a new client project with no owner")
	}
}

// TestOwnerUnresolvedWarning_TeamProjectNeverWarns keeps dada's own
// internal/platform/example-project/fin-core rows (owner_type=team, ownerless
// by design) from ever tripping this warning.
func TestOwnerUnresolvedWarning_TeamProjectNeverWarns(t *testing.T) {
	if ownerUnresolvedWarning(true, nil, "team") {
		t.Fatal("ownerUnresolvedWarning = true, want false for owner_type team")
	}
}

// TestOwnerUnresolvedWarning_ResolvedOwnerNeverWarns confirms a manifest that
// did resolve to a users.id never trips the warning, regardless of owner_type.
func TestOwnerUnresolvedWarning_ResolvedOwnerNeverWarns(t *testing.T) {
	id := uuid.New()
	if ownerUnresolvedWarning(true, &id, "client") {
		t.Fatal("ownerUnresolvedWarning = true, want false when an owner resolved")
	}
}

// TestOwnerUnresolvedWarning_ExistingProjectNeverWarnsAgain keeps every
// re-sync of an already-known ownerless project (isNew=false) from re-logging
// the same warning on every poll interval forever.
func TestOwnerUnresolvedWarning_ExistingProjectNeverWarnsAgain(t *testing.T) {
	if ownerUnresolvedWarning(false, nil, "client") {
		t.Fatal("ownerUnresolvedWarning = true, want false for an existing (not newly-inserted) project")
	}
}
