# ServiceIdentity — implementation plan

Spec: [ADR-021](../docs/adr/ADR-021-service-identity.md).
Trigger: `reels-tracker` moved project on 2026-08-02, its pasted gateway key
stayed bound to the old (now deleted) project, and every inference call returned
`401 no credential for project/provider openrouter`.

## Phase 1 — identity owns the AI gateway key

- [ ] Migration: `service_identities (id, app_name, environment_id, scopes,
      created_at)` + `ai_gateway_keys.identity_id UUID REFERENCES
      service_identities(id) ON DELETE SET NULL`. Keys keep working when an
      identity is dropped; they just stop being reconciled.
- [ ] CRD `ServiceIdentity` (cluster-scoped) + RBAC for the console SA to write
      Secrets in app namespaces (it already reads them, `cloudtask/dbcreds.go`).
- [ ] Renderer: `ServiceIdentity` entry in `resources.values.yaml`
      (`gitops-agent/internal/renderer/resources_values.go`), golden test
      alongside the ServiceDatabaseV2 goldens.
- [ ] Reconciler in the console backend: mint → deliver
      `<appRef>-identity-credentials` → revoke keys bound to any other project.
      Order matters; a test must assert the old key is still valid at the moment
      the new secret lands.
- [ ] App render: consume `DADA_AI_API_KEY` / `DADA_AI_BASE_URL` via
      `secretKeyRef`, never a literal.

## Phase 2 — moves and preflight

- [ ] `classifyMoveChildren`: `ServiceIdentity` joins the movable set.
- [ ] MoveApp: re-render with `spec.namespace=<dstNs>` + dst labels, same edit
      as the ServiceDatabaseV2 re-point. Golden test on the moved manifest.
- [ ] MoveImpact: per attached identity, list providers the source project has
      an `ai_provider_credentials` row for and the destination does not. Warning,
      not a blocker — this is the half of the reels failure no identity fixes.
- [ ] Live rehearsal on a throwaway app before any real app is migrated.

## Phase 3 — migrate the pasted keys

- [ ] Inventory apps whose `env_vars` hold an `sk-dada` literal.
- [ ] `reels-tracker` first: declare the identity, cut over to `secretKeyRef`,
      drop `OPENROUTER_API_KEY` from argo-infra, revoke the user-service key.
- [ ] Restore its direct-provider models once the identity lands in a project
      that holds an openrouter credential — `OPENROUTER_MODEL` is pinned to the
      `medium` tier alias meanwhile, and `OCR_VISION_MODEL` /
      `WEB_SEARCH_MODEL` have no working tier equivalent at all.

## Phase 4 — generic identity

- [ ] Keycloak service-account client per identity for service-to-service auth,
      delivered into the same secret. Same resource, second payload.

## Out of scope

Rotation on a schedule. Reconcile already mints, delivers and revokes in the
right order, so a scheduled rotation is a trigger on top of Phase 1 rather than
new machinery — but it is not needed to close the failure that started this.
