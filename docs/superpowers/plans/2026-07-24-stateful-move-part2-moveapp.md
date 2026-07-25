# Stateful App Move — Part 2: MoveApp Phase 2/3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Productize the proven move mechanics into gitops-agent `doMoveApp` so a
dada-managed stateful App (attached ServiceDatabaseV2 and/or a persistent volume) can be
moved across projects — Phase 3 (DB re-point) shipped, Phase 2 (volume) flag-gated.

**Architecture:** For dada-managed Apps the DB is the same shared-server logical DB, so
Phase 3 re-points it by re-rendering the ServiceDatabaseV2 CR under the destination
`resources.values.yaml` with `spec.namespace = dstNamespace` (instead of the current
strip), then repointing the DB snapshot — pure git + DB, no data movement, `Orphan`-safe.
Phase 2 (volume) reuses the runbook's Longhorn copy, stays behind a feature flag defaulting
OFF with a dry-run preview, because cross-namespace volume data movement is the risky part
and dada currently has few stateful Apps.

**Tech Stack:** Go (gitops-agent worker + backend API), the existing renderer +
`resources.values.yaml` upsert helpers, Postgres (resource_snapshots), Kanister backup.

## Global Constraints

- Execute ONLY after Part 1's real moves (n8n, telemost-bot) succeed and soak healthy —
  Part 2 encodes what Part 1 proves.
- No inline `//` comments in Go source (repo hook); doc-comments only. Plain ASCII only.
- Keep the owning-app child predicate IDENTICAL across its call sites
  (`gitops-agent/internal/db/snapshots.go` `AppMoveSnapshots`, `doDeleteApp` cascade,
  `ListGCChildSnapshots`, backend `delete_impact.go` `consoleImpact`).
- New/changed routes require `swag init -g cmd/server/main.go --parseInternal
  --parseDependency --outputTypes json -o internal/api/docs`; the coverage gate must pass.
- `go build/vet/test ./...` green in BOTH `backend/` and `gitops-agent/`.
- Do NOT commit/push app changes to prod branches without the owner's integration step
  (mirror `tasks/move-app-phase1-spec.md` "I integrate + review" note) — Part 2 lands as
  reviewed code, not a live prod push.

---

## File Structure

- Modify: `docs/adr/ADR-014-move-app-across-projects.md` — rewrite Phase 2/3 to reality.
- Modify: `gitops-agent/internal/worker/move_app.go` — DB re-point (replace strip);
  volume flag-gated path.
- Modify: `gitops-agent/internal/config/config.go` (or worker cfg) — `MoveVolumeEnabled` flag.
- Create: `gitops-agent/internal/worker/move_app_db.go` — DB re-render + repoint helpers
  (keeps `move_app.go` focused).
- Test: `gitops-agent/internal/worker/move_app_db_test.go`, golden fixtures under
  `gitops-agent/tests/golden/moveapp/`.
- Modify: `backend/internal/api/move_app.go` — reclassify DB as movable; trigger safety
  backup on stateful initiate; volume gated by flag.
- Test: `backend/internal/api/move_app_test.go`.

---

## Task 1: Rewrite ADR-014 Phase 2/3 to the verified reality

**Files:**
- Modify: `docs/adr/ADR-014-move-app-across-projects.md:75-98`

- [ ] **Step 1: Replace the Phase 2 and Phase 3 sections**

Replace the "Phase 2 — stateful move (persistent volume)" and "Phase 3 — attached DB
re-home" bodies with the verified model:
- Phase 3 (DB): the ServiceDatabaseV2 is a logical DB in the shared `postgresql-0`
  (ns `databases`); the Crossplane `Database` MR is `deletionPolicy: Orphan` and
  namespace-independent; `spec.namespace` only selects where `<appRef>-db-credentials` is
  delivered. Re-home = re-render the CR under the dst app's `resources.values.yaml` with
  `spec.namespace=dstNs`; Crossplane redelivers creds; repoint the DB snapshot; take a
  safety backup first. No data movement, `Orphan`-safe. Correct the old "PV rebind by
  clearing claimRef" note: the chosen method is copy-into-fresh-RWX (source retained), not
  claimRef surgery.
- Phase 2 (volume): copy source volume via Longhorn snapshot->backup->restore into a fresh
  volume in the dst namespace (source retained, `Retain`); brief scale-to-0 for a
  consistent snapshot; feature-flag OFF by default with a dry-run preview.
- Add a "Ground truth (2026-07-24)" note: the concrete prod apps (n8n, telemost-bot) are
  raw `tenant-apps`-generated Argo apps, moved by the Part 1 runbook, not this op; this op
  serves future dada-managed Apps. Link `tasks/move-app-phase2-3-spec.md` and both plan
  files.

- [ ] **Step 2: Commit**

```bash
git add docs/adr/ADR-014-move-app-across-projects.md
git commit -m "docs(adr-014): rewrite Phase 2/3 to shared-DB re-point + copy-to-RWX reality"
```

---

## Task 2: gitops-agent — DB re-render helper (Phase 3 core)

**Files:**
- Create: `gitops-agent/internal/worker/move_app_db.go`
- Test: `gitops-agent/internal/worker/move_app_db_test.go`

**Interfaces:**
- Consumes: the DB child snapshot summary (image/spec) already loaded via
  `db.AppMoveSnapshots`; `renderer` package (`renderer.RenderServiceDatabase`,
  `renderer.ServiceDatabaseSpec`).
- Produces: `func rerenderServiceDatabaseForMove(dbName, dstProjectSlug, dstEnvSlug, dstNamespace, appRef, datname string, backupEnabled bool, backupFreq string) (string, error)` returning the ServiceDatabaseV2 manifest YAML with `spec.namespace=dstNamespace` and dst `dada.io/project`/`dada.io/environment` labels, for Upsert into the dst `resources.values.yaml`.

- [ ] **Step 1: Read the current renderer + move_app.go strip site**

Read `gitops-agent/internal/renderer/renderer.go:14-53` (`ServiceDatabaseSpec`,
`serviceDatabaseTmpl`, `RenderServiceDatabase`) and `gitops-agent/internal/worker/move_app.go:176-208`
(the `loadResourcesValues` + `rv.RemoveKind("ServiceDatabaseV2")` block). Confirm the
`ServiceDatabaseSpec` field names before writing the test.

- [ ] **Step 2: Write the failing test**

```go
package worker

import (
	"strings"
	"testing"
)

func TestRerenderServiceDatabaseForMoveSetsDstNamespace(t *testing.T) {
	got, err := rerenderServiceDatabaseForMove("n8n", "platform", "prod", "platform-prod", "n8n", "n8n", true, "@daily")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"kind: ServiceDatabaseV2", "namespace: platform-prod", "appRef: n8n", "database: n8n", "dada.io/project: platform", "dada.io/environment: prod"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered DB CR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "example-project") {
		t.Fatalf("rendered DB CR still references source project:\n%s", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd gitops-agent && go test ./internal/worker/ -run TestRerenderServiceDatabase -v`
Expected: FAIL (undefined: rerenderServiceDatabaseForMove)

- [ ] **Step 4: Implement move_app_db.go**

```go
package worker

import "github.com/dada-tuda/console/gitops-agent/internal/renderer"

// rerenderServiceDatabaseForMove renders the attached ServiceDatabaseV2 CR for
// the destination project/env: spec.namespace and the dada.io labels point at the
// target so Crossplane redelivers the <appRef>-db-credentials secret into the dst
// namespace. The logical database (datname/role) is unchanged and, being
// deletionPolicy=Orphan and namespace-independent, keeps all data.
func rerenderServiceDatabaseForMove(dbName, dstProjectSlug, dstEnvSlug, dstNamespace, appRef, datname string, backupEnabled bool, backupFreq string) (string, error) {
	return renderer.RenderServiceDatabase(renderer.ServiceDatabaseSpec{
		Name:           dbName,
		Namespace:      dstNamespace,
		ProjectSlug:    dstProjectSlug,
		EnvSlug:        dstEnvSlug,
		AppRef:         appRef,
		Database:       datname,
		BackupEnabled:  backupEnabled,
		BackupSchedule: backupFreq,
	})
}
```

(If the actual `ServiceDatabaseSpec` field names differ from Step 1's read, adjust these
key names to match — the test asserts the rendered output, not the struct.)

- [ ] **Step 5: Run test to verify it passes**

Run: `cd gitops-agent && go test ./internal/worker/ -run TestRerenderServiceDatabase -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add gitops-agent/internal/worker/move_app_db.go gitops-agent/internal/worker/move_app_db_test.go
git commit -m "feat(gitops-agent): render attached DB CR for the move target (Phase 3 core)"
```

---

## Task 3: gitops-agent — wire DB re-point into doMoveApp (replace strip)

**Files:**
- Modify: `gitops-agent/internal/worker/move_app.go:114-122` (remove DB abort),
  `:176-208` (replace `rv.RemoveKind("ServiceDatabaseV2")` with re-render+Upsert).
- Test: `gitops-agent/internal/worker/move_app_db_test.go` (add a resources.values.yaml transform test).

**Interfaces:**
- Consumes: `rerenderServiceDatabaseForMove` (Task 2), the `rv` resourcesValues helper
  (`Upsert`, `RemoveKind`).
- Produces: doMoveApp carries the DB CR to the dst `resources.values.yaml` re-pointed,
  and no longer aborts on an attached ServiceDatabaseV2. The DB snapshot repoint is already
  handled by `repointMovedAppSnapshots` (it uses `AppMoveSnapshots`, which returns the DB
  child) — add a test asserting that.

- [ ] **Step 1: Write the failing test (rv transform swaps strip for re-point)**

```go
func TestMoveResourcesValuesRepointsDBInsteadOfStripping(t *testing.T) {
	src := "manifests:\n" +
		"    - apiVersion: platform.dada-tuda.ru/v1alpha1\n" +
		"      kind: ServiceDatabaseV2\n" +
		"      metadata:\n        name: n8n\n" +
		"      spec:\n        appRef: n8n\n        namespace: example-project-prod\n        database: n8n\n"
	out, err := repointResourcesValuesDB(src, "n8n", "platform", "prod", "platform-prod", "n8n", "n8n", true, "@daily")
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if !strings.Contains(out, "namespace: platform-prod") {
		t.Fatalf("DB not re-pointed:\n%s", out)
	}
	if strings.Contains(out, "example-project-prod") {
		t.Fatalf("source namespace still present:\n%s", out)
	}
	if !strings.Contains(out, "kind: ServiceDatabaseV2") {
		t.Fatalf("DB CR was stripped, expected re-point:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gitops-agent && go test ./internal/worker/ -run TestMoveResourcesValuesRepoints -v`
Expected: FAIL

- [ ] **Step 3: Implement `repointResourcesValuesDB` + wire into doMoveApp**

Add to `move_app_db.go`:

```go
import "github.com/dada-tuda/console/gitops-agent/internal/renderer"

// repointResourcesValuesDB replaces the ServiceDatabaseV2 entry in a
// resources.values.yaml document with one re-rendered for the destination
// project/env/namespace, leaving all other manifests intact. Returns the new
// document. If there is no ServiceDatabaseV2 entry it returns src unchanged.
func repointResourcesValuesDB(src, dbName, dstProjectSlug, dstEnvSlug, dstNamespace, appRef, datname string, backupEnabled bool, backupFreq string) (string, error) {
	rv, err := renderer.ParseResourcesValues(src)
	if err != nil {
		return "", err
	}
	if !rv.HasKind("ServiceDatabaseV2") {
		return src, nil
	}
	dbYAML, err := rerenderServiceDatabaseForMove(dbName, dstProjectSlug, dstEnvSlug, dstNamespace, appRef, datname, backupEnabled, backupFreq)
	if err != nil {
		return "", err
	}
	if err := rv.Upsert(dbYAML); err != nil {
		return "", err
	}
	return rv.Marshal()
}
```

Then in `move_app.go`: delete the abort block at `:114-122`, and in the resources.values
carry-over (`:176-208`) replace `rv.RemoveKind("ServiceDatabaseV2")` with a call that
re-renders the DB entry using the DB child snapshot's datname/appRef/backup fields and the
dst project/env/namespace. Keep the volume guard (`:107-112`) until Task 5.

(If the `renderer` resourcesValues API names differ — `ParseResourcesValues`/`HasKind`/
`Upsert`/`Marshal` — use the actual names from `gitops-agent/internal/renderer/resources_values.go`;
Step 1 of Task 2 reads them.)

- [ ] **Step 4: Run tests**

Run: `cd gitops-agent && go test ./internal/worker/ -run 'TestMoveResourcesValues|TestRerender' -v`
Expected: PASS

- [ ] **Step 5: Add a snapshot-repoint assertion test**

```go
func TestAppMoveSnapshotsIncludesDBChild(t *testing.T) {
	t.Skip("integration: requires a seeded resource_snapshots fixture; asserts AppMoveSnapshots returns the ServiceDatabaseV2 child so repointMovedAppSnapshots re-parents it")
}
```

(Convert to a real integration test if the worker package already has a DB test harness;
otherwise this documents the invariant that `repointMovedAppSnapshots` already re-parents
the DB via `AppMoveSnapshots`.)

- [ ] **Step 6: Build + commit**

Run: `cd gitops-agent && go build ./... && go vet ./... && go test ./internal/worker/ -v`
Expected: green

```bash
git add gitops-agent/internal/worker/move_app.go gitops-agent/internal/worker/move_app_db.go gitops-agent/internal/worker/move_app_db_test.go
git commit -m "feat(gitops-agent): re-point attached DB on move instead of blocking (Phase 3)"
```

---

## Task 4: backend — reclassify DB as movable + safety backup on stateful initiate

**Files:**
- Modify: `backend/internal/api/move_app.go` (`computeMoveImpact` ~`:70-154`, `MoveApp` initiate ~`:256-336`)
- Test: `backend/internal/api/move_app_test.go`

**Interfaces:**
- Consumes: existing `computeMoveImpact`, `consoleImpact` (`delete_impact.go:177-210`), the
  backup trigger (`startDBBackup` in `db_backups.go` or the Kanister client).
- Produces: `computeMoveImpact` returns an attached ServiceDatabaseV2 in `Movable` (label
  "database (re-pointed; safety backup taken)") instead of `Blockers`; `MoveApp` triggers a
  safety `CreateDBBackup` for each attached DB before enqueuing the op and 202s.

- [ ] **Step 1: Read move_app.go + write the failing test**

Read `backend/internal/api/move_app.go` to confirm the impact struct field names
(`Movable`, `Blockers`, `CanMove`, reason consts). Then:

```go
func TestComputeMoveImpactDBIsMovableNotBlocker(t *testing.T) {
	imp := computeMoveImpactForTest(...) // seed an app with one ServiceDatabaseV2 child, no volume
	if !imp.CanMove {
		t.Fatalf("expected can_move=true for DB-only app, blockers=%v", imp.Blockers)
	}
	var foundDB bool
	for _, m := range imp.Movable {
		if m.Kind == "ServiceDatabaseV2" {
			foundDB = true
		}
	}
	if !foundDB {
		t.Fatalf("DB not listed as movable: %+v", imp.Movable)
	}
	for _, b := range imp.Blockers {
		if b.Kind == "ServiceDatabaseV2" {
			t.Fatalf("DB still a blocker: %+v", b)
		}
	}
}
```

(Use the package's existing move/delete-impact test harness for seeding; mirror
`delete_impact` tests. If none exists, add a minimal table-driven classifier test around
the child-classification function.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/ -run TestComputeMoveImpactDBIsMovable -v`
Expected: FAIL

- [ ] **Step 3: Implement the reclassification**

In `computeMoveImpact`, move the `ServiceDatabaseV2` case from `Blockers` to `Movable`
with label "database (re-pointed to the target; a safety backup is taken first)". Keep the
volume case in `Blockers` (guarded until Task 5). In `MoveApp` initiate, before the
`INSERT INTO operations`, for each attached DB call the existing backup path
(`startDBBackup(..., DBBackupKindManual)`) and wait for/record it (or enqueue and proceed
if backups are async — record the backup id in the op payload/audit). Update the reason
consts accordingly.

- [ ] **Step 4: Run tests + swagger regen (if response shape changed)**

Run: `cd backend && go test ./internal/api/ -run TestComputeMoveImpact -v`
Then: `cd backend && swag init -g cmd/server/main.go --parseInternal --parseDependency --outputTypes json -o internal/api/docs`
Expected: tests PASS; swagger regen clean; coverage gate passes.

- [ ] **Step 5: Build + commit**

Run: `cd backend && go build ./... && go vet ./... && go test ./internal/api/ -run 'TestMove' -v`

```bash
git add backend/internal/api/move_app.go backend/internal/api/move_app_test.go backend/internal/api/docs
git commit -m "feat(backend): DB attached-child becomes movable (re-point) + safety backup on move"
```

---

## Task 5: Phase 2 volume — flag-gated, dry-run preview (no unproven in-agent surgery)

**Files:**
- Modify: `gitops-agent/internal/config/config.go` (add `MoveVolumeEnabled` env flag, default false)
- Modify: `gitops-agent/internal/worker/move_app.go:107-112` (volume guard becomes flag-gated)
- Modify: `backend/internal/api/move_app.go` (volume gated by the same capability)
- Test: `gitops-agent/internal/worker/move_app_test.go`

**Interfaces:**
- Produces: with the flag OFF (default), a volume remains a blocker and doMoveApp aborts on
  `summary.volume` exactly as today. With the flag ON, backend impact lists the volume as
  movable ("data copy, brief downtime") and doMoveApp emits the volume-copy plan (reusing
  the Part 1 Longhorn copy path) — but only after the Part 1 rehearsal has validated that
  path on this cluster. This task ships the GATING + plan preview; enabling real in-agent
  volume execution is a follow-up that reuses `tools/dbmove` Longhorn code via a shared
  package.

- [ ] **Step 1: Write the failing test (flag off => volume still blocks)**

```go
func TestMoveVolumeBlockedWhenFlagOff(t *testing.T) {
	if moveVolumeAllowed(false) {
		t.Fatal("volume move must be blocked when MoveVolumeEnabled=false")
	}
	if !moveVolumeAllowed(true) {
		t.Fatal("volume move must be allowed when MoveVolumeEnabled=true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gitops-agent && go test ./internal/worker/ -run TestMoveVolumeBlocked -v`
Expected: FAIL

- [ ] **Step 3: Implement the flag gate**

```go
// moveVolumeAllowed reports whether stateful-volume moves are enabled. Volume
// data movement across namespaces is the highest-risk part of a move, so it is
// gated behind an explicit flag defaulting OFF; when off, doMoveApp keeps the
// pre-existing volume abort.
func moveVolumeAllowed(flag bool) bool { return flag }
```

Wire `w.cfg.MoveVolumeEnabled` into the guard at `move_app.go:107-112`: when false, keep
the current abort; when true, proceed into the volume-copy plan (dry-run preview logged;
real execution reuses the Part 1 path). Add `MOVE_VOLUME_ENABLED` (default false) to
gitops-agent config. Mirror the capability in backend `computeMoveImpact` (volume ->
movable only when the same env flag is set).

- [ ] **Step 4: Run tests + build both modules**

Run: `cd gitops-agent && go build ./... && go test ./internal/worker/ -run TestMoveVolume -v`
Run: `cd backend && go build ./... && go test ./internal/api/ -run TestMove -v`
Expected: green

- [ ] **Step 5: Commit**

```bash
git add gitops-agent/internal/config/config.go gitops-agent/internal/worker/move_app.go backend/internal/api/move_app.go gitops-agent/internal/worker/move_app_test.go
git commit -m "feat(move): gate Phase 2 volume moves behind MOVE_VOLUME_ENABLED (default off)"
```

---

## Task 6: Full verification + golden fixtures

**Files:**
- Create: `gitops-agent/tests/golden/moveapp/n8n-resources.values.repointed.yaml` (golden)
- Modify: tests as needed.

- [ ] **Step 1: Golden-file the re-pointed resources.values.yaml**

Add a golden fixture + test that feeds a representative source `resources.values.yaml`
(App + PublicApi + ServiceDatabaseV2) through `repointResourcesValuesDB` and asserts the
output matches the golden (DB re-pointed to dst ns, PublicApi untouched).

- [ ] **Step 2: Run the full suites**

Run: `cd gitops-agent && go build ./... && go vet ./... && go test ./...`
Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: all green; swagger coverage gate passes.

- [ ] **Step 3: Commit**

```bash
git add gitops-agent/tests/golden/moveapp/ gitops-agent/internal/worker/move_app_db_test.go
git commit -m "test(gitops-agent): golden-file DB re-point on move; full suites green"
```

---

## Self-Review

- Spec coverage (Part 2 sections of `tasks/move-app-phase2-3-spec.md`): ADR rewrite ->
  Task 1; Phase 3 DB re-render + wire -> Tasks 2-3; snapshot repoint (existing path) ->
  Task 3 Step 5; backend impact reclassify + safety backup -> Task 4; Phase 2 volume
  flag-gate -> Task 5; golden + full verify -> Task 6.
- Deferred-by-design: real in-agent volume EXECUTION reuses the Part 1 Longhorn path via a
  shared package (follow-up noted in Task 5), not re-implemented blind — flagged, not a
  silent gap.
- Type consistency: `rerenderServiceDatabaseForMove`, `repointResourcesValuesDB`,
  `moveVolumeAllowed` defined once; renderer/resourcesValues API names to be confirmed
  against source in Task 2 Step 1 before use (called out explicitly).
- Open risk carried from spec: confirm `renderer.ServiceDatabaseSpec` field names +
  resourcesValues helper names before Task 2/3 implementation (Task 2 Step 1 reads them).
