# VM track → first-class per-service Applications (unify with k8s)

**Goal (owner-stated):** on the VM/compose track, an **Application** is a first-class
per-service entity — exactly like the k8s track — with its own
monitoring / logs / metrics / state / params / view. **Compose is NEVER shown to
the user.** It is a *rendering detail* the **AppServer layer** owns: the AppServer
knows its Applications and renders them into ONE `compose.yaml` → ONE stack → ONE
VM under the hood, but the platform stores and manages them as separate
Applications.

> Import of `api + postgres + nginx + redis` = **4 normal Applications** on the
> VM environment. Not one compose stack. Not "stack + discovered service". The
> compose file is invisible plumbing at the AppServer level.

This brings the VM track to the model the **k8s track already has** (app =
first-class, per-service). Today `app == stack` ONLY on the compose track — that
is the divergence being removed.

## Current state (verified)

- **k8s track**: `Application` is first-class per service. `RenderApp` /
  `RenderAppValues` render one App → one Deployment (Helm). `DeployImageVersion`
  is per app. State/metrics/logs are per app.
- **VM/compose track**: `app == compose stack`. `RenderComposeFromDiscovery`
  renders the WHOLE selected workload into ONE `compose.yaml`
  (`AppComposeGitPath(project, env, appName)` → one file per "app"). `doDeployStack`
  deploys it as ONE Portainer stack. The MVP `ImportComposeStack` imports the
  selected services into ONE App. So a multi-service VM shows as ONE app.
- **My session's divergence (to be superseded by this plan):** I hand-created
  per-service App snapshots (`profi`, `profi-backend`) + Infra snapshots in the
  `findata` env, but they have **no backing stack/compose** (the only stack is
  `profi-vm`), so Deploy/Rollback/Restart on them would fail. This plan makes
  per-service Apps *real* by moving the compose to the AppServer layer.
- **Per-service filtering already possible:** `ListContainers` supports a label
  filter; compose stamps `com.docker.compose.service=<svc>` on every container, so
  per-app state/metrics/logs can filter to one service's container.

## Target model

```
Project
  Environment (runtime=vm, bound to an AppServer)
    AppServer  ── owns ──►  Application: api     (image, ports, env, volumes, view)
       │                    Application: frontend
       │                    Application: postgres
       │                    Application: nginx
       │
       └── renders ALL its Applications ──► ONE compose.yaml ──► ONE Portainer stack ──► ONE VM
                                            (invisible plumbing)
```

- **Application** (per service) is the unit the UI shows and the user acts on:
  Deploy / Restart / Rollback / Logs / Metrics / State / params — all per app.
- **AppServer** is the aggregation + render boundary: it holds the set of
  Applications and renders the combined `compose.yaml`. Compose is never surfaced.
- **One VM = one stack = N Applications.** The stack is derived from the Apps.

## The core shift

Move the compose file from **per-app** (`apps/<app>/compose.yaml`) to
**per-AppServer** (`app-servers/<server>/compose.yaml`, or the env's single
stack). Each Application contributes ONE service block. Any per-app action
re-renders the AppServer's combined compose from the current set of Applications
and redeploys the single stack.

## Phases (plan; owner reviews before code)

### Phase 1 — MODEL: Application as per-service on VM
- An `App` resource_snapshot per service in the VM env, carrying: `service` name,
  `image`, `ports`, `volumes` (external-pinned for stateful), `env`/params,
  `restart`, and its owning `app_server`. Reuse the existing `App` kind (drop the
  separate `Infra` kind — postgres/nginx are just Applications, per the owner:
  "api+postgres+nginx+redis = 4 normal Applications"). Data-safety (external
  volumes) is a per-App property, not a separate concept.
- Decide storage of per-app spec: snapshot `summary_json` (fast) vs a typed table.

### Phase 2 — RENDER: AppServer combines Applications → one compose
- New `RenderAppServerCompose(apps []AppSpecVM)` → one `compose.yaml` with one
  service per Application + a top-level `volumes:` external block aggregated from
  all stateful Apps. Absolute host binds preserved. This REPLACES per-app
  `RenderComposeFromDiscovery`/`AppComposeGitPath` for the VM track.
- Git path becomes per-AppServer (one stack file). Editing any Application
  re-renders the whole file deterministically.

### Phase 3 — DEPLOY / ROLLBACK / RESTART, per app → one stack
- **Deploy app X (new image/params):** update X's spec → re-render the AppServer
  compose → `RedeployStack` (whole stack; only X's container changes because only
  its service block changed + `PullImage` scoped is not possible in compose, so
  the stack recreates changed services).
- **Rollback app X:** revert X's spec to its previous version (git history of the
  AppServer compose is coarse — need PER-APP param history, e.g. a deploy_history
  per Application, or diff the service block). Re-render → redeploy.
- **Restart app X:** `docker compose restart <service>` via the docker proxy
  (per-service), NOT a whole-stack redeploy. Needs a portainer-agent
  restart-one-service verb (docker `POST /containers/<id>/restart` filtered by the
  compose-service label).
- Ops stay per-app in the API/UI; the AppServer layer translates to stack render +
  targeted container action.

### Phase 4 — IMPORT: discover → N Applications
- `ImportComposeStack` becomes `ImportWorkload`: for each included discovered
  service, create ONE Application on the VM env (not one compose app). Then render
  the AppServer compose once + deploy. Per-app naming from the image (api =
  profi-backend, etc.), user-renamable in the wizard.

### Phase 5 — PER-APP state / metrics / logs
- **State:** the App's compose-service container status (ListContainers filtered by
  `com.docker.compose.service=<svc>`).
- **Metrics:** cadvisor series filtered to that service's container (label).
- **Logs:** the fleet fluent-bit stream filtered to that container (add
  `com.docker.compose.service` into the log record so the console can filter per
  app, mirroring the k8s per-app log filter).

### Phase 6 — UI: Application panel, no compose
- The Applications list + app detail are runtime-agnostic (same components k8s
  uses). VM apps show per-app state/metrics/logs + Deploy/Restart/Rollback.
- The AppServer page shows its set of Applications (its "location") — no stack,
  no compose editor surfaced. Compose editing (if ever needed) is an advanced/hidden
  affordance, not the primary model.

### Phase 7 — MIGRATE findata to the new model (prod-safe)
- Reconcile the live `profi-vm` stack (nginx/backend/frontend/postgres) into **4
  Applications** on the `findata` env, owned by the AppServer, with the combined
  render reproducing the CURRENTLY running stack byte-for-byte (external volume
  `compose_profi_pg_data` pinned). Prove `RenderAppServerCompose` == the live
  compose before any redeploy. Only then does per-app Deploy/Rollback/Restart work.
- Until migrated, findata stays on the current single `profi-vm` stack (running,
  data-safe) — the inconsistent per-service snapshots I created are removed or
  converted here.

## Decisions to pin before code

1. **Kind model:** all services are `App` (drop `Infra` kind) vs keep an infra
   subtype for DB/proxy. Owner said "4 normal Applications" → lean all-App with an
   optional `role` hint (database/proxy) for the UI only.
2. **Per-app param + rollback history:** where the per-Application spec + its
   previous versions live (snapshot json vs typed table vs git per-service block
   history). Rollback semantics depend on this.
3. **Restart granularity:** per-service container restart (needs a new proxy verb)
   vs whole-stack recreate. Owner wants per-app → per-service.
4. **Git layout:** one compose per AppServer (`app-servers/<name>/compose.yaml`) —
   confirm the path + how the portainer-agent DeployStack resolves it (today it's
   `apps/<app>/compose.yaml`).
5. **Backwards compat:** k8s track unchanged; this only restructures the VM track.
   The MVP `ImportComposeStack` (just shipped) is superseded by `ImportWorkload`.

## Risk / safety

- **Prod (findata):** the live stack + data are untouched until Phase 7, which
  gates redeploy on a byte-for-byte render match. External-volume pinning stays the
  data-safety invariant throughout.
- **Big surface:** model + render + deploy/rollback/restart + import + per-app
  telemetry + UI. Land it phase-by-phase behind the existing running stack; never
  a flag-day.
