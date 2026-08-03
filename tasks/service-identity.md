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

- [x] RBAC for the console SA to **write** Secrets in app namespaces (it only
      read them before, `cloudtask/dbcreds.go`). Cluster-wide on Secrets for
      the same reason `db-creds-reader` is: app namespaces are created per
      project, so no per-namespace Role can cover them.
- [x] Reconciler `api/identity_delivery.go`: every k8s app gets a
      `service_identities` row, and its namespace gets
      `<app>-identity-credentials` (`DADA_SERVICE_TOKEN`, `DADA_AI_BASE_URL`).
      Convergent on a 10m tick under advisory lock `0x64616461_0008`, so
      nothing has to remember to deliver. A Secret whose token no longer
      resolves counts as missing — that is the only repair path for a dead
      credential, since the console keeps the hash and the cluster keeps the
      sole plaintext.
- [x] No project-change branch, as designed: on a move the destination is
      empty while the old namespace still holds a live token, so delivery
      **adopts** that plaintext instead of minting, and the stale copy is
      pruned. Minting there would kill the token the running pod is using —
      exactly the 2026-08-02 breakage. The first version did mint, and the
      move test caught it.
- [x] Regression test for 2026-08-02: after an app changes namespace the
      delivered token is byte-identical and the source namespace is emptied.
      Plus mint-idempotence (a converged app is never rotated), re-mint of a
      revoked token, and an unmanaged Secret of the same name left untouched.
- [x] Minting is the last resort: it happens only when the identity holds zero
      live tokens. An identity whose token is live but invisible to the loop is
      the pre-delivery world — reels-tracker's plaintext is a literal in
      argo-infra — and minting there would revoke the credential the running
      bot is using. Caught by reading the loop's target set against the prod
      database before the first tick, not by a test: the tests modelled only
      the world after delivery.
- [x] Prod pre-seeded on 2026-08-03: `internal-prod` now holds
      `reels-tracker-identity-credentials` carrying the token already live for
      identity `6dc328c6`, labelled `dada.io/managed-by=dada-cloud-console` and
      `dada.io/identity=<id>`. Verified the Secret's sha256 matches the live
      `service_identity_tokens` row, so delivery adopts instead of minting
      whichever build lands first. Nothing consumes the Secret yet — that is
      the `secretKeyRef` step below.
- [x] Verified in prod on 2026-08-03, console `fcaeaadb`: the loop logs
      `started interval=10m0s secret=<app>-identity-credentials
      key=DADA_SERVICE_TOKEN` on both replicas, and the first tick reports
      `apps=72 delivered=71 pruned=0` — 71 apps got a credential they never
      had, and the one skipped is reels-tracker, whose Secret was already
      there. Its token is unchanged and still the only live row for identity
      `6dc328c6` (secret sha256 still matches, revoked count 0), so the guard
      held on the run that mattered. `internal-prod` now carries eight
      `*-identity-credentials` Secrets, one per app.
- [x] Both audiences re-checked on that build: reels-tracker's in-pod LLM call
      returns `IDENTITY_OK` through `ai-gateway-service` (200), the same token
      gets 403 `identity is not granted the pay:charge scope` on
      `POST /api/v1/pay/charges`, and an unknown `sk-dada-id-` is 401.
- [ ] CRD `ServiceIdentity` (cluster-scoped) + renderer entry in
      `resources.values.yaml`. Not needed for delivery — the console writes the
      Secret through the API directly, which keeps the token out of git, unlike
      the renderer's `AppEnvSecretSpec` channel that commits `stringData`
      plaintext to argo-infra.
- [x] App render: consume the token via `secretKeyRef`, never a literal.
      Deliberately after delivery ships: a `secretKeyRef` to a Secret that is
      not there yet wedges the pod in `CreateContainerConfigError`. Done for
      reels-tracker on 2026-08-03 (argo-infra `5afdddbf`). The `env` block was
      the wrong home twice over — it renders into a **ConfigMap** mounted at
      `/app/.env`, so the token was plaintext in git *and* in a ConfigMap.
      `extraEnv` is real container env, and pydantic-settings reads `os.environ`
      before the dotenv source, so the name defined there wins. `LLM_ENDPOINTS`
      was deleted rather than rewritten: its single entry carried the same key
      and base_url that `_llm_endpoints()` already derives from
      `OPENROUTER_API_KEY` + `OPENROUTER_BASE_URL`, so keeping it meant pasting
      the token a second time. Verified live: `.env` ConfigMap holds zero
      `sk-dada` strings, the container env resolves the key from the Secret, and
      the in-pod call is `200` with `IDENTITY_OK`.

## Phase 3 — second audience, proving the generalisation

- [x] Payment gateway resolves `pay:charge` through the identity instead of
      `pay_service_keys.service` free text; `service_charges` gains a real
      owner (2026-08-03, migration 089). Both owner families stay valid --
      dada-vpn-bot's live `sk-dada-pay-` key keeps working -- and a CHECK makes
      the pair exclusive, so every charge has exactly one owner. 083's
      idempotency contract needed its own partial unique index on the identity
      half: `UNIQUE (service_key_id, external_ref)` is vacuous when
      service_key_id is NULL, so without it every retry would have created a
      second YooKassa payment.
- [x] `POST .../apps/:appName/identity` accepts a `scopes` array, validated
      against a grantable set. `pay:charge` stays out of the defaults: an app
      that can spend money says so at mint time.
- [x] Verified in prod on 2026-08-03, console `2bc4af01`: migration 089 is the
      head of `schema_migrations`, `service_charges` carries `identity_id`,
      a nullable `service_key_id`, the `service_charges_one_owner` CHECK and
      `uq_service_charges_identity_ref`. Against `console.dada-tuda.ru`:
      reels-tracker's token (scopes `ai:chat ai:embeddings`) gets 403 "identity
      is not granted the pay:charge scope"; a probe identity holding
      `pay:charge` lists its own charges (200, empty), reads a foreign charge id
      as 404 and reaches body validation (400 `external_ref is required`); an
      unknown `sk-dada-id-` is 401. Both tokens' `last_used_at` advanced, so the
      pay path touches the same token row the AI path does. The probe identity
      was deleted after the run. The money-moving half (a real YooKassa payment)
      is deliberately unexercised in prod.
- [ ] MoveImpact: per attached identity, list providers the source project has
      an `ai_provider_credentials` row for and the destination does not.
      Warning, not a blocker — the half no identity fixes.

## Phase 4 — attribution follows the grain

- [x] `agent_token_usage` gains `identity_id` (and so the app), so per-app cost
      stops being unanswerable. Migration 094 + console `4a28e46` + ai-gateway
      `1b05695`, which have to ship together: the console writes whatever the
      gateway sends, and the gateway only learned to send it in that commit.
      The relay is introspection -> auth metadata -> pre-call metadata ->
      ledger row, and a break anywhere in it is silent — it shows up only as a
      permanently empty column, which is why the gateway test walks all four
      hops rather than asserting on one.
      `identity_id` enters the INSERT through a subselect, not a parameter: the
      usage callback fires after the response is already out, so an app deleted
      in between would raise a foreign-key violation and drop the whole row —
      trading the cost to save an attribution that no longer exists. Unknown or
      malformed degrades to NULL, which is what the column already means for
      console chat and project-scoped keys.
      Read side: `GET /projects/{id}/ai/usage` gains `apps[]`, and the project
      AI page renders it with the unattributed remainder spelled out. That
      remainder is the interesting number until every app is on an identity.
- [ ] Verify in prod: a reels-tracker call lands in `agent_token_usage` with
      its `identity_id` set, and the project page splits the spend.
- [ ] Per-app scopes and quota on the identity.

## Phase 5 — migrate the pasted keys

- [x] Inventory apps whose `env_vars` hold an `sk-dada` literal. Done
      2026-08-03 against argo-infra: three, all now cut over —
      `reels-tracker` (`OPENROUTER_API_KEY`), `profi-backend`
      (`OPENAI_API_KEY`) and `telemost-bot` (`GROQ_API_KEY`). `clusters/`
      contains no `sk-dada-` literal any more.
- [x] `profi-backend` and `telemost-bot` on `secretKeyRef` (argo-infra
      `cbfcc566`). Preconditions checked before the edit, not after: `fin-core`
      holds an openai credential, `internal` a groq one, both identities live in
      the same project as their app, and both tokens already answered `200`
      through the gateway on the app's own alias (`gpt-4o`, `groq-gpt-oss`).
      Verified after rollout from inside each pod, resolving config exactly the
      way the app does — `200 IDENTITY_OK` both times, and neither `.env`
      ConfigMap contains `sk-dada`. `telemost-bot` reads config with
      `load_dotenv()`, whose default `override=False` leaves an existing
      environment variable alone, so container env beats the file there too.
- [ ] **The old keys are still live and the console cannot revoke them.** Both
      pre-cutover literals still authenticate at the gateway (`200`), and
      neither is in `ai_gateway_keys` — that table holds three rows, all
      `sk-dada-ai-`, all revoked. Per `ai_keys.go:21`, the gateway sends
      `sk-dada-ai-` to the console, `sk-dada-id-` to identity introspection and
      **anything else in the `sk-dada-` family to user-service**. So these are
      user-service keys: not tied to an app, not visible on
      `/admin/ai-gateway`, and not revocable from here. Revoking them is what
      finishes the migration, and it has to happen in user-service.
- [x] `reels-tracker` cut over to an identity token (2026-08-03) and, later the
      same day, off the literal entirely — argo-infra now carries a
      `secretKeyRef`, not a value. Its next move re-points the identity row and
      the delivered Secret; git changes nothing.
- [ ] Duplicate `reels-tracker` App in project `platform`, created 2026-08-02
      while chasing the 401 and never removed. It has no database, no `env` and
      no ingress traffic, so both pods CrashLoop on the missing
      `TELEGRAM_BOT_TOKEN`; delivery counted it as a real app and minted it an
      identity. Delete through the console (not by hand in git) so the
      `resource_snapshots` rows go with it.
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
