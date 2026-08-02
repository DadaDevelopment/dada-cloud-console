# ServiceIdentity — implementation plan

Spec: [ADR-021](../docs/adr/ADR-021-service-identity.md).
Trigger: `reels-tracker` moved project on 2026-08-02, its pasted gateway key
stayed bound to the old (now deleted) project, and every inference call returned
`401 no credential for project/provider openrouter`.
The principal is the identity **row**; project/environment are only where it
currently lives. A move re-points that row and re-mints nothing.

## Phase 1 — the principal exists

- [x] Migration `087_service_identity.sql`: `service_identities` (surrogate id +
      nullable location) and `service_identity_tokens`; nullable `identity_id`
      on `ai_gateway_keys` and `pay_service_keys`. Nothing dropped — 058's
      `project_id` stays until introspection stops reading it.
- [x] `POST /internal/identity/introspect`: one endpoint every platform service
      calls to turn a bearer token into a principal + scopes.
- [x] `AIIntrospectKey` accepts an `sk-dada-id-` token and answers in the shape
      the gateway plugin already parses, so the AI path needs no new format.
      A project-less identity answers invalid rather than a default project.
- [x] Mint/rotate and read routes on the app
      (`.../apps/:appName/identity`), rotation reusing the identity row.
- [x] MoveApp re-points `service_identities` in the same transaction as
      `repointMovedAppSnapshots`.
- [x] Unit tests: token format, prefix routability and disjointness from
      `sk-dada-ai-`, scope matching (no prefix/substring satisfaction), and the
      resolution query's live-row and LEFT JOIN invariants.

## Phase 2 — delivery, so the token stops living in git

- [ ] CRD `ServiceIdentity` (cluster-scoped) + RBAC for the console SA to
      **write** Secrets in app namespaces (it only reads them today,
      `cloudtask/dbcreds.go`).
- [ ] Renderer: `ServiceIdentity` entry in `resources.values.yaml`
      (`gitops-agent/internal/renderer/resources_values.go`), golden test
      alongside the ServiceDatabaseV2 goldens.
- [ ] Reconciler: no live token → mint + deliver `<appRef>-identity-credentials`
      (`DADA_SERVICE_TOKEN`, plus the base URLs the scopes imply). No
      project-change branch — that is the point of the grain.
- [ ] `classifyMoveChildren`: `ServiceIdentity` joins the movable set; MoveApp
      re-renders it with `spec.namespace=<dstNs>` so the secret lands in the
      destination namespace. Golden test on the moved manifest.
- [ ] Move rehearsal on a throwaway app asserting the **same** token still
      authenticates after the move — the regression test for 2026-08-02.
- [ ] App render: consume the token via `secretKeyRef`, never a literal.

## Phase 3 — second audience, proving the generalisation

- [ ] Payment gateway resolves `pay:charge` through the identity instead of
      `pay_service_keys.service` free text; `service_charges` gains a real
      owner.
- [ ] MoveImpact: per attached identity, list providers the source project has
      an `ai_provider_credentials` row for and the destination does not.
      Warning, not a blocker — the half no identity fixes.

## Phase 4 — attribution follows the grain

- [ ] `agent_token_usage` gains `identity_id` (and so the app), so per-app cost
      stops being unanswerable.
- [ ] Per-app scopes and quota on the identity.

## Phase 5 — migrate the pasted keys

- [ ] Inventory apps whose `env_vars` hold an `sk-dada` literal.
- [x] `reels-tracker` cut over to an identity token (2026-08-03). Still a
      literal in argo-infra — `secretKeyRef` waits on Phase 2 — but the value is
      now the app's own credential, so the next move re-points instead of
      orphaning it.
- [ ] Revoke reels-tracker's old user-service key, now unused.
- [x] Its direct-provider models are back: `OPENROUTER_MODEL=or-gpt-41-mini`,
      `OCR_VISION_MODEL`, `WEB_SEARCH_MODEL` all answer through the identity.
      This needed project `internal` to hold an openrouter credential at all —
      the identity fixes *whose* credential it is, not whether the destination
      project has one. See the Phase 3 MoveImpact warning.
- [ ] Issue project `internal` its own openrouter key. It currently shares
      `platform`'s (same ciphertext copied on 2026-08-03 to unblock prod), which
      makes per-project spend unattributable.
- [ ] Mint identities through `POST .../apps/:appName/identity` rather than SQL.
      reels-tracker's row was inserted directly because prod runs
      `AUTH_MODE=keycloak` and the console service account is not a member of
      project `internal`, so the HTTP route answers 404 to it.
- [ ] Drop `ai_gateway_keys.project_id` once nothing reads it.

## Phase 6 — generic identity

- [ ] Keycloak service-account client per identity for service-to-service auth,
      delivered into the same secret. Same resource, second payload.

## Out of scope

Scheduled rotation. Mint-and-deliver already exists, so rotation is a trigger on
top of Phase 2 rather than new machinery, and it is not needed to close the
failure that started this.
