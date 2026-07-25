# dbmove hardening + stateful-move Part 2

Grounded on LIVE prod moves (telemost-bot->internal, n8n->platform, 2026-07-23/24) and a
live Longhorn v1.6.1 scratch-ns probe (2026-07-25, TRIGGER_WORKED). Evidence in review.

## Part A - dbmove tool (tools/dbmove), test per fix - DONE (40 tests green, dry-run verified)

- [x] Gap 1: longhornBackupStep snapshot-while-attached; wait Snapshot readyToUse;
      verify on backupvolume.status.lastBackupAt ADVANCING (not Backup.state). +test
      (waitBackupAdvanced/waitSnapshotReady + seqRunner end-to-end signal test)
- [x] Gap 2: restoreVolumeYAML add spec.frontend: blockdev. +test
- [x] Gap 3: folder-move rewrites app.yaml spec.helm.path source->target path. +test
      (applyFolderLiteralEdits temp-dir test)
- [x] Gap 4: capture <app>-db-credentials pre-move, re-patch into target post-move. +test
      (capture/repatch round-trip via DBMOVE_STATE_DIR)
- [x] Gap 5: claimRef-bound Retain PV + volumeName injected into chart PVC; drop our own
      PVC; skip unmounted volumes (n8n main replicas:0). +test
- [x] Gap 6: verify uses per-app DB schema (n8n schema=n8n). +test
- [x] Gated reclaim step: NOT in default plan; requires --reclaim --execute
      --confirm-reclaim; inert otherwise (CLI-verified). Code+test only, NOT run.
- [x] Fix git add -A -> explicit paths in folderMoveStep.
- [x] cd tools/dbmove && go build ./... && go vet ./... && go test ./... (all green)
- [x] configs/n8n.yaml: dbSchema + per-volume chartTemplate/hasData.
- [x] main.go: -reclaim / -confirm-reclaim flags.

## Part B - Part 2 (gitops-agent doMoveApp DB re-point + backend reclassify)

- [x] Task 1: ADR-014 rewrite. Status=Accepted (P1+P3 shipped, P2 spec-only). Phase 3
      = Orphan-safe shared-DB re-point (cluster-scoped XR, spec.namespace only, verbatim
      name/appRef/database/backup invariants) + cluster-scoped pre-adopt handoff (both
      ArgoCD markers, tracking-id authoritative) + pre-move Kanister safety backup. Phase
      2 = copy-into-fresh-RWX reality lives in tools/dbmove (operator-run); in-agent
      DELIBERATELY aborts behind MOVE_VOLUME_ENABLED (flag-ON still aborts, no empty-PVC
      data-loss path); backend keeps volume a hard blocker. Rejected: claimRef-rebind,
      DB rename/new-database. Execution steps renumbered to include pre-adopt.
- [x] Task 2: gitops-agent rerenderServiceDatabaseForMove (spec.namespace=dstNs) +
      repointResourcesValuesDB (self-extract from rv) + renderer.ManifestOfKind. +test
- [x] Task 3: wire DB re-render into doMoveApp (drop DB abort, replace RemoveKind
      strip with re-point). PLUS: ServiceDatabaseV2 is CLUSTER-SCOPED (spec.namespace
      tracking, discovery.go:68) so preAdoptClusterScopedResources now hands off the
      DB composite to target Argo BEFORE src prune (else prune drops creds secret /
      rotates PG pw). +test (8 green: rerender, repoint x2, DB pre-adopt).
- [x] Task 4: backend computeMoveImpact reclassify DB child Blocker->Movable (deleted
      the DB-blocker branch; dropped unused moveBlockerReasonDatabase) via extracted
      pure classifyMoveChildren(appName,hasVolume,children,envVarCount). Volume stays a
      HARD blocker. Best-effort startMoveSafetyBackups (pre-move logical backup of each
      attached DB, guarded by kanister.Enabled, new DBBackupKindPreMove, non-responding
      resolveManagedDatabaseName). Swagger @Description updated. +test (5 green:
      DB-movable, volume-blocks, volume+DB, envvars, never-nil). VERIFIED in an isolated
      HEAD worktree (parallel session left an uncommitted unused-import break in
      agent_chat.go): build+vet clean, full api suite + OpenAPI coverage gate PASS.
      Swagger struct shapes UNCHANGED so coverage gate green without regen; regen
      DEFERRED (would fold the parallel session's uncommitted agent_chat.go annotations).
- [x] Task 5: MOVE_VOLUME_ENABLED flag (default off). gitops-agent config field +
      load line + pure moveVolumeAllowed(flag) predicate + worker guard wired: flag
      OFF aborts "disabled" (today), flag ON aborts DISTINCT "copy not implemented,
      refusing to move without data" (no empty-PVC data-loss path). +test (9 green).
      DELIBERATE deviation: backend keeps volume a HARD blocker (does NOT read flag)
      so console never advertises a move the worker will refuse — flip when Phase 2
      execution ships. Documented in ADR (Task 1).
- [x] Task 6: golden fixtures + full build/vet/test both modules. Golden test
      TestMoveGoldenRepointResourcesValues pins byte-exact repointResourcesValuesDB
      output (n8n_src -> n8n_moved: DB re-homed to platform-prod ns + rewritten
      labels/op, PublicApi UNTOUCHED proving DB-only, name/appRef/database/backup
      verbatim). VERIFIED in isolated HEAD worktree (parallel session left an
      uncommitted unused-import break in agent_chat.go): gitops-agent full
      build+vet+test ./... all green; backend full build+vet clean, api suite +
      OpenAPI coverage gate PASS. Swagger regen DELIBERATELY DEFERRED: MoveImpact/
      MoveMovableItem/MoveBlockerItem JSON shapes UNCHANGED (only classify logic +
      free-text @Description), so TestOpenAPICoverage is green without regen;
      regenerating would fold the parallel session's uncommitted agent_chat.go
      annotations into this commit. Committed swagger @Description text stays
      cosmetically stale; gate stays green.

## Constraints (HARD)
- No // comments in Go (repo hook); plain ASCII only.
- Stage explicit paths, never git add -A (parallel sessions share main).
- Do NOT push dada-cloud main (triggers console rebuild) without owner OK.
- Do NOT reclaim source until owner confirms healthy soak.
