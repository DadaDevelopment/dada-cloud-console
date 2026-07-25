package api

import "testing"

func hasMovable(items []MoveMovableItem, kind, name string) bool {
	for _, it := range items {
		if it.Kind == kind && it.Name == name {
			return true
		}
	}
	return false
}

func hasBlocker(items []MoveBlockerItem, kind string) bool {
	for _, it := range items {
		if it.Kind == kind {
			return true
		}
	}
	return false
}

func TestClassifyMoveChildrenDatabaseIsMovable(t *testing.T) {
	children := []ImpactItem{
		{Kind: "ServiceDatabaseV2", Name: "n8n", Group: impactGroupDatabase, Source: impactSourceConsole},
		{Kind: "PublicApi", Name: "n8n", Group: impactGroupDomain, Source: impactSourceConsole},
	}
	movable, blockers := classifyMoveChildren("n8n", false, children, 0)

	if !hasMovable(movable, "ServiceDatabaseV2", "n8n") {
		t.Errorf("attached ServiceDatabaseV2 must be MOVABLE under Phase 3 (Orphan-safe re-point), got movable=%+v", movable)
	}
	if hasBlocker(blockers, "ServiceDatabaseV2") {
		t.Errorf("attached ServiceDatabaseV2 must NOT be a blocker, got blockers=%+v", blockers)
	}
	if !hasMovable(movable, "PublicApi", "n8n") {
		t.Errorf("PublicApi child must be movable, got movable=%+v", movable)
	}
	if len(blockers) != 0 {
		t.Errorf("a DB-only app has no blockers, got %+v", blockers)
	}
}

func TestClassifyMoveChildrenVolumeStillBlocks(t *testing.T) {
	movable, blockers := classifyMoveChildren("api", true, nil, 0)
	if !hasBlocker(blockers, "Volume") {
		t.Errorf("a persistent volume must remain a blocker (Phase 2 copy not implemented), got blockers=%+v", blockers)
	}
	if len(movable) != 0 {
		t.Errorf("no children and no env vars means nothing movable, got %+v", movable)
	}
}

func TestClassifyMoveChildrenVolumeAndDatabase(t *testing.T) {
	children := []ImpactItem{{Kind: "ServiceDatabaseV2", Name: "db", Group: impactGroupDatabase}}
	movable, blockers := classifyMoveChildren("app", true, children, 0)
	if !hasBlocker(blockers, "Volume") {
		t.Errorf("volume must block even when a DB is present, got blockers=%+v", blockers)
	}
	if !hasMovable(movable, "ServiceDatabaseV2", "db") {
		t.Errorf("the DB is still movable regardless of the volume blocker, got movable=%+v", movable)
	}
	if hasBlocker(blockers, "ServiceDatabaseV2") {
		t.Errorf("the DB must never be a blocker, got blockers=%+v", blockers)
	}
}

func TestClassifyMoveChildrenEnvVars(t *testing.T) {
	withVars, _ := classifyMoveChildren("app", false, nil, 3)
	if !hasMovable(withVars, "EnvVars", "3 vars") {
		t.Errorf("a non-zero env var count adds one synthetic movable EnvVars entry, got %+v", withVars)
	}
	noVars, _ := classifyMoveChildren("app", false, nil, 0)
	if hasMovable(noVars, "EnvVars", "0 vars") {
		t.Errorf("a zero env var count adds no EnvVars entry, got %+v", noVars)
	}
}

func TestClassifyMoveChildrenNeverNil(t *testing.T) {
	movable, blockers := classifyMoveChildren("app", false, nil, 0)
	if movable == nil || blockers == nil {
		t.Fatalf("both slices must be non-nil so they JSON-encode as [] not null; movable=%v blockers=%v", movable, blockers)
	}
	if len(movable) != 0 || len(blockers) != 0 {
		t.Errorf("a stateless app with no children/env vars is fully clean, got movable=%+v blockers=%+v", movable, blockers)
	}
}
