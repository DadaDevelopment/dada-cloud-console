# DadaAgent ↔ dada-cloud "Cloud Task" integration — Design

Status: design (locked before implementation)
Date: 2026-06-27
Author: brainstorm session (caveman mode)
Related: tasks/vercel-flow-design.md (separate build/deploy track — NOT this)

## Goal

Vercel-"Agent"-button parity: the console shows a suggested-task chip on an app
(e.g. "Yandex Metrika + goals"). One click fires an autonomous job — no chat. An
external agent (DadAgent) picks the right skill, clones the app's git repo, runs the
skill, opens a PR, and produces artifacts. The console shows live status, the PR link,
and the artifacts. First shipped task = Yandex Metrika counter + conversion goals.

## Key insight

Both sides already exist. The integration is a thin contract, not a new engine.

- **DadAgent** (`/Users/alex/IdeaProjects/DadAgent`, FastAPI) already owns: intent
  submission + execution (`POST /v1/agentsync/intents`, `.../execute`), worker
  scheduling (`app/workspace/k8s_runner.py`), a skill catalog (`GET /v1/skills`,
  `SkillDetail.intent` = trigger), artifacts (`POST/GET /v1/files`), and workflow
  status (`GET /v1/workflows/{id}/mission-control|timeline`). Worker planning is its
  domain.
- **dada-cloud** already owns: the GitHub connect-repo flow (LIVE on prod, GitHub App
  `argocd-dada`, AES-GCM encrypted tokens in `git_integrations`), the project/app
  console, and the operations machine.

The "chip" is therefore a small reusable contract that lets the cloud fire a task
mindlessly and lets the agent report back. The agent does all heavy lifting
(skill-pick, clone, PR, artifacts); the cloud stays dumb (render chip, fire, display).

## Architecture decisions (locked)

| # | Decision | Why | Cost |
|---|----------|-----|------|
| D1 | **Repo access = reuse cloud GitHub App.** Cloud mints a short-lived, repo-scoped install token and passes `repo_full_name + token` inside the intent payload. Agent clones `https://x-access-token:<tok>@github.com/<full>.git`, opens PR. | Connect-repo already live; least privilege; one identity; token expires ~1h. | DadAgent git tooling is Bitbucket-centric today — needs a GitHub clone/PR path. |
| D2 | **Catalog = hybrid curated.** DadAgent exposes `cloud-task`-tagged skills (each declares a param schema). Cloud keeps a curated map `task_type → {skill_id, applicability filter, param resolver}`. | Agent authoritative on *what runs*; cloud authoritative on *what surfaces where* (e.g. metrika only on web apps). | A tag/convention + param-schema declaration on skills. |
| D3 | **Status = agent webhook callback.** On every task-graph transition + artifact upload + PR open, DadAgent POSTs an event to cloud `POST /api/v1/webhooks/dadagent`. | Real-time console; no polling loop; agent already has a webhook subsystem. | New inbound cloud endpoint + auth + idempotency/retry handling. |
| D4 | **Auth = Keycloak client-credentials, both directions.** Cloud SA client `dada-cloud-backend` calls the agent; agent SA client `dada-agent` calls the cloud webhook. Both validate RS256 JWKS, realm `master`, issuer `https://id.dada-tuda.ru/realms/master`. | One identity model everywhere; aligns with the approved DADA SSO contract. | Two Keycloak clients provisioned (argo-infra gitops); JWKS validation on both ends. |
| D5 | **Metrika params resolved server-side, zero-form (MVP).** Cloud pulls the Metrika OAuth token from the existing `crossplane-system/yandex-metrica-credentials` secret, auto-creates/reuses a counter, uses a fixed default goal set. | Fastest path; the manual flow already proved the inputs; no UI to build. | Custom counter id / custom goals deferred to a later param-form iteration. |
| D6 | **Artifacts proxied, not mirrored.** Cloud proxies the agent's `/v1/files` on demand; the agent stays the source of truth. | Simpler; no duplicate storage; no sync drift. | Console artifact view depends on agent availability. |

## Core data shape — the contract

### Intent payload (cloud → agent), inside `IntentSubmitRequest`

```
summary:    human one-liner ("Wire Yandex Metrika + conversion goals into <app>")
task_type:  catalog key, e.g. "yandex-metrika-goals"
priority:   "medium"
payload (carried in the structured fields the agent already accepts):
  cloud_task_id:   uuid (cloud's row id — echoed in callbacks for correlation)
  repo:
    provider:        "github"
    full_name:       "org/name"
    default_branch:  "main"
    install_token:   "<short-lived GitHub App install token>"   # secret, ~1h
  params:            task-specific object validated against the skill's param schema
                     (metrika: { metrika_oauth_token, counter_id?, goals[] })
  callback:
    url:             "https://console.dada-tuda.ru/api/v1/webhooks/dadagent"
    # auth = Keycloak SA bearer minted by the agent per call (D4); no shared secret
```

### Callback event (agent → cloud), `POST /api/v1/webhooks/dadagent`

```
cloud_task_id:  uuid (correlation)
intent_id:      string
workflow_id:    string
event:          "task_status" | "artifact" | "pr_opened" | "completed" | "failed"
status:         IntentTaskStatus mirror: planned|running|completed|failed|blocked
pr_url:         string (on pr_opened/completed)
artifacts:      [{ file_id, name, size, kind }]   # refs into agent /v1/files
error:          string (on failed)
emitted_at:     RFC3339
```

Idempotency: `(cloud_task_id, event, status, emitted_at)` — duplicate callbacks are
no-ops (agent retries on cloud 5xx).

## End-to-end flow

```
console app page → curated chip "Yandex Metrika + goals" (shown only for web apps)
  → POST /api/v1/projects/:p/environments/:e/apps/:app/cloud-tasks { task_type }
      backend:
        1. authz: getUserProjectRole → canWrite ⇒ 403 else continue
        2. resolve catalog entry (task_type → skill_id, param resolver, applicability)
        3. applicability check (app kind == web) else 422
        4. mint GitHub App install token scoped to the app's git_repos repo
        5. param resolver: read metrika token from crossplane secret, default goals
        6. Keycloak SA token (client_credentials, cached until exp)
        7. POST agent /v1/agentsync/intents { summary, task_type, payload }  → workflow_id
        8. POST agent /v1/agentsync/intents/{intent_id}/execute
        9. INSERT cloud_tasks (status=running, intent_id, workflow_id)
       10. 202 { cloud_task }
  → DadAgent:
        pick skill by task_type (cloud-task tag) → clone via install_token →
        run skill → open PR → upload artifacts (/v1/files) →
        POST cloud webhook on each transition (status, pr_url, artifact refs)
  → cloud webhook POST /api/v1/webhooks/dadagent (JWKS-verified, agent SA):
        correlate by cloud_task_id → UPDATE cloud_tasks (status, pr_url, artifacts)
  → console polls GET .../cloud-tasks/:id (3s while active) → live status timeline,
       PR link, artifact list (artifact bytes proxied from agent /v1/files on click)
```

The cloud never schedules workers, never runs the skill, never touches the working
tree. The agent never stores cloud identity beyond echoing `cloud_task_id`.

## dada-cloud additions

### Migration `backend/migrations/0NN_cloud_tasks.sql`
```sql
cloud_tasks (
  id            uuid primary key,
  project_id    ... not null,
  environment_id ... not null,
  app_name      text not null,
  git_repo_id   ... not null,          -- the connected repo (D1)
  task_type     text not null,          -- catalog key (D2)
  intent_id     text,                   -- agent intent
  workflow_id   text,                   -- agent workflow
  status        text not null default 'running',  -- running|completed|failed|canceled
  pr_url        text,
  artifacts     jsonb not null default '[]',       -- [{file_id,name,size,kind}]
  error         text,
  actor_id      ...,                    -- who clicked
  created_at    timestamptz default now(),
  updated_at    timestamptz default now()
)
-- forward-only, idempotent (IF NOT EXISTS), GRANT ... TO dada; footer
-- index on (project_id, app_name); partial index on status='running'
```

### Catalog config (Go, embedded)
`backend/internal/cloudtask/catalog.go`: a static registry
`task_type → { skill_id, label, description, applies_to(appKind) bool, resolveParams(ctx, app) (map, error) }`.
MVP entry: `yandex-metrika-goals` → skill id (discovered/pinned from agent
`/v1/skills`), `applies_to = web`, `resolveParams` reads the crossplane Metrika secret
+ default goal set. Catalog is the cloud's curation layer; adding a task = one entry.

### Backend
- `backend/internal/api/cloud_tasks.go`:
  `POST .../apps/:app/cloud-tasks`, `GET .../apps/:app/cloud-tasks`,
  `GET .../cloud-tasks/:id`, `GET .../cloud-tasks/:id/artifacts/:fileId` (proxy to
  agent `/v1/files/:id`). Auth/authz mirrors `apps.go` (getUserProjectRole, canWrite).
- `backend/internal/api/webhooks_dadagent.go`: `POST /api/v1/webhooks/dadagent`
  registered OUTSIDE the user-auth group; validates the agent's Keycloak bearer via
  JWKS (reuse the principal/oidc pkg from the MCP/SSO track); correlate + update row;
  idempotent.
- `backend/internal/dadagent/client.go`: Keycloak SA token (client_credentials, cached
  to exp), `SubmitIntent`, `ExecuteIntent`, `GetFile` (proxy). Base URL + client creds
  from config (out-of-git Secret, same pattern as monitoring config).
- Reuse the existing GitHub App install-token minting (connect-repo path) for D1.

### Frontend
- App page: render curated chips (one per applicable catalog task) with a "Run"
  button; on click → `cloudTasksApi.create` → optimistic running card.
- Task detail panel: status timeline (from `cloud_tasks.status` + agent timeline if
  exposed), PR link (button), artifact list (download = hit the proxy route). Poll
  every 3s while `status=running` (copy operations-polling UX).
- `lib/types.ts`: `CloudTask`, `CloudTaskArtifact`, `CloudTaskCatalogEntry`.
- `lib/api.ts`: `cloudTasksApi` (create/list/get/artifact).

## DadaAgent additions

- **Skill tagging**: mark `cloud-task` skills + declare a JSON param schema so the
  cloud's curation/validation has a contract. Expose tag + schema via `/v1/skills`.
- **GitHub clone+PR path**: accept `{repo, install_token}` in the intent payload;
  clone with the token; open a PR on completion. (Today the git tooling is
  Bitbucket-centric — this is the main new agent-side work.)
- **Callback emitter**: on each task-graph transition + artifact upload + PR open, mint
  a Keycloak SA token (`dada-agent` client) and POST the callback to the cloud webhook;
  retry with backoff on cloud 5xx; carry `cloud_task_id`.
- **Reference skill `yandex-metrika-goals`** (automates the proven manual flow):
  1. Ensure a Metrika counter (reuse `counter_id` if passed, else create via Metrika
     Management API using the OAuth token).
  2. Create `action`-type JS goals via the Metrika API (default set:
     `form_submit`, `form_start`, `cta_contact_click`, `messenger_click`),
     skipping ids that already exist.
  3. Inject `ym(<counter>, 'init', …)` + `ym('reachGoal', …)` calls into `src/` and
     `index.html` at the form/CTA/messenger handlers.
  4. Open a PR.
  5. Upload artifacts: the diff, a goals table (id/name/identifier), and a screenshot
     or JSON dump of the Metrika goal config.

## Auth detail (D4)

- **Cloud → agent**: backend holds Keycloak client `dada-cloud-backend`
  (client_credentials grant); caches the access token to its exp; sends as Bearer to
  agent endpoints. Agent validates RS256/JWKS, `iss`+`exp`, group/role check.
- **Agent → cloud webhook**: agent holds client `dada-agent`; mints a token per
  callback (or caches); cloud webhook validates RS256/JWKS the same way and checks the
  token's client/role is `dada-agent`.
- Both clients provisioned via argo-infra gitops (out of this repo's scope; documented
  as a prerequisite). No shared static secret anywhere.

## Security notes

- **Install token is a secret in transit**: TLS only; never logged; agent must not
  persist it beyond the workspace lifetime; token is short-lived (~1h) and repo-scoped.
- **Webhook endpoint is unauthenticated at the user layer** but bearer-gated by JWKS;
  reject any token whose client ≠ `dada-agent`. Rate-limit. Idempotent writes.
- **Metrika OAuth token** travels in the intent payload — same handling as the install
  token (TLS, no logs, workspace-scoped). Sourced from the existing crossplane secret;
  the cloud never mints it.
- **Untrusted code execution** is the agent's existing concern (its k8s_runner /
  workspace sandbox) — the cloud adds no new code-execution surface.

## Phasing

1. **Close the loop (one task).** Migration, catalog with the single
   `yandex-metrika-goals` entry, backend create + webhook + agent client, GitHub
   install-token handoff, Keycloak clients, agent GitHub clone/PR path + the metrika
   skill + callback emitter, minimal console chip + status card.
2. **Artifacts UX.** Artifact list + proxy download + PR button polish.
3. **Catalog growth.** Second/third tasks (e.g. "bump vulnerable dep", "add sitemap");
   each = one catalog entry + one tagged agent skill.
4. **Param forms (optional).** Per-task param UI when a task needs user input beyond
   server-resolvable defaults (custom goals, custom counter).
5. **Polling fallback (optional).** Add a poll path if callbacks prove lossy.

## What we are NOT building (YAGNI)

- No polling loop in MVP (callback only — D3).
- No artifact mirroring/copy (proxy only — D6).
- No generic param UI in MVP (server-side resolution — D5).
- No monorepo subdir / multi-repo targeting (one connected repo per app).
- No chat / interactive agent surface — fire-and-report only.
- Not the build/deploy vercel-flow (separate track, `tasks/vercel-flow-design.md`).

## New components summary

| Component | Side | Job |
|---|---|---|
| `cloud_tasks` table | cloud | task rows: intent/workflow ids, status, pr_url, artifacts |
| catalog registry | cloud | curated `task_type → skill_id + applicability + param resolver` (D2) |
| `cloud_tasks.go` API | cloud | create/list/get + artifact proxy |
| `webhooks_dadagent.go` | cloud | JWKS-gated callback intake (D3/D4) |
| `dadagent/client.go` | cloud | Keycloak SA + submit/execute/getfile |
| console chips + task card | cloud | render, fire, live status, PR, artifacts |
| GitHub clone+PR path | agent | clone via install token, open PR (D1) |
| `cloud-task` skill tag + param schema | agent | curation contract (D2) |
| callback emitter | agent | push status/artifact/PR events (D3) |
| `yandex-metrika-goals` skill | agent | the reference task (counter+goals+inject+PR) |
| Keycloak clients `dada-cloud-backend`, `dada-agent` | argo-infra | s2s identities (D4) |

Reused unchanged: connect-repo GitHub App + AES-GCM token infra, operations/console UI
patterns, agent intent/execute/skills/files/workflow APIs, agent k8s_runner worker
scheduling, Keycloak realm `master` + JWKS, crossplane Metrika secret.
