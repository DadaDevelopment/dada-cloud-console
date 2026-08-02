# ServiceIdentity — implementation plan

Spec: [ADR-021](../docs/adr/ADR-021-service-identity.md).
Trigger: `reels-tracker` moved project on 2026-08-02, its pasted gateway key
stayed bound to the old (now deleted) project, and every inference call returned
`401 no credential for project/provider openrouter`.
Principal is the **app**, not the project — a move must not touch credentials.

## Phase 1 — the credential names an app

- [ ] Migration: `service_identities (id, app_name, environment_id, scopes,
      created_at)`, unique on `(app_name, environment_id)`. Add
      `ai_gateway_keys.identity_id UUID REFERENCES service_identities(id) ON
      DELETE CASCADE`; backfill existing keys to an identity per app; drop
      `ai_gateway_keys.project_id` last, in its own migration, once nothing
      reads it.
- [ ] `AIIntrospectKey`: resolve the project from
      `resource_snapshots (kind='App', name, environment_id)` instead of
      `k.project_id`. Hot path of every inference call — cache it, with the same
      TTL discipline as the gateway's own introspect cache.
- [ ] Test first, before the migration lands: an identity whose App snapshot row
      is missing introspects as **invalid**, never as a default project.
- [ ] CRD `ServiceIdentity` (cluster-scoped) + RBAC for the console SA to write
      Secrets in app namespaces (it already reads them, `cloudtask/dbcreds.go`).
- [ ] Renderer: `ServiceIdentity` entry in `resources.values.yaml`
      (`gitops-agent/internal/renderer/resources_values.go`), golden test
      alongside the ServiceDatabaseV2 goldens.
- [ ] Reconciler: no active key → mint + deliver `<appRef>-identity-credentials`.
      No project-change branch — that is the point of the grain.
- [ ] App render: consume `DADA_AI_API_KEY` / `DADA_AI_BASE_URL` via
      `secretKeyRef`, never a literal.

## Phase 2 — moves and preflight

- [ ] `classifyMoveChildren`: `ServiceIdentity` joins the movable set.
- [ ] MoveApp: re-render with `spec.namespace=<dstNs>` + dst labels, so the
      secret lands in the destination namespace. No re-mint, no revoke. Golden
      test on the moved manifest.
- [ ] Move rehearsal on a throwaway app that asserts the **same** token keeps
      working across the move — the regression test for 2026-08-02.
- [ ] MoveImpact: per attached identity, list providers the source project has
      an `ai_provider_credentials` row for and the destination does not.
      Warning, not a blocker — the half no identity fixes.

## Phase 3 — attribution follows the grain

- [ ] `agent_token_usage` gains `identity_id` (and so the app), so per-app cost
      stops being unanswerable.
- [ ] Per-app scopes and quota on the identity.

## Phase 4 — migrate the pasted keys

- [ ] Inventory apps whose `env_vars` hold an `sk-dada` literal.
- [ ] `reels-tracker` first: declare the identity, cut over to `secretKeyRef`,
      drop `OPENROUTER_API_KEY` from argo-infra, revoke the user-service key.
- [ ] Restore its direct-provider models once the identity resolves to a project
      holding an openrouter credential — `OPENROUTER_MODEL` is pinned to the
      `medium` tier alias meanwhile, and `OCR_VISION_MODEL` / `WEB_SEARCH_MODEL`
      have no working tier equivalent at all.

## Phase 5 — generic identity

- [ ] Keycloak service-account client per identity for service-to-service auth,
      delivered into the same secret. Same resource, second payload.

## Out of scope

Scheduled rotation. Mint-and-deliver already exists in reconcile, so rotation is
a trigger on top of Phase 1 rather than new machinery, and it is not needed to
close the failure that started this.
