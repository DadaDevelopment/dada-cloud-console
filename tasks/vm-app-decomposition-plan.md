# App-server as its own environment + decomposed apps/infra (fin-core / findata)

Model the VM (app-server) as a **first-class environment**, and its workload as
**separate applications 1:1 with the cloud-native ones** (no special marking) plus
**infrastructure** — not as one atomic `profi-vm` compose app.

## Target model (owner-confirmed)

- **app-server (findata) = its own environment** of the project, runtime=compose,
  bound via `environments.app_server_id`. The environment is the discriminator, so
  `profi@k8s` and `profi@findata` coexist with the **same name, no marker** (they
  live in different `environment_id`s → no PK collision on `(project, env, kind,
  name)`).
- In the findata env: applications **`profi`, `profi-backend`** (first-class, like
  k8s) + infrastructure **`postgres`, `nginx`**.
- Physical deploy stays **one compose stack** (`profi-vm`, 4 services) — atomic,
  as compose requires. The per-app/infra records are a **representation layer**
  derived from the stack's discovery; a release still bumps the stack's tag.

## Current state (verified on prod)

- `environments` already supports this: columns `type`, `runtime`, `app_server_id`
  exist. But **every env platform-wide is runtime=k8s** (9/9) — no compose/VM env
  has ever existed; findata is the first.
- fin-core has ONE env `prod` (type=prod, **runtime=k8s**). During the cutover I
  bound `app_server_id`=findata onto **this k8s env** (expedient, wrong home) and
  put the `profi-vm` App snapshot + the k8s apps (`profi`, `profi-backend`) all in
  it → the conflation the owner is reacting to.
- **env-collapse** (see [[project_env_collapse]]) removed the multi-env selector
  from the UI ("one implicit prod env per project"). So **a second env is
  invisible/unnavigable until the frontend is taught to show it** → data and UI
  must move together, else changes are dead or break single-env assumptions.
- The `profi-vm` stack is **live and serving** on endpoint 3. Nothing here may
  disrupt the running containers or the k8s apps.

## The core risk

Restructuring a live project's environments on prod: the single-env UI + default
-project nav + gitops compose path all assume one env. A naive 2nd-env insert can
strand apps (unreachable in UI) or break navigation. Mitigate by moving data +
UI together, behind verification, and keeping the k8s `prod` env untouched except
removing the mis-placed `app_server_id`.

## Phases

### Phase 0 — DISCOVER the blast radius (read-only) ✅ mostly done
- Confirmed env schema, the single-env-per-project UI assumption, the git compose
  path `clusters/beget-prod/projects/<proj>/environments/<envSlug>/apps/<app>/`,
  and `GetComposeDeployTarget` resolving endpoint via `env.app_server_id`.
- TODO: audit every place that assumes one env per project (frontend default-env
  resolution, project overview, apiFetch env id source) so Phase 3 is complete.

### Phase 1 — MODEL: give findata its own environment (backend/data)
- Create env `findata` (name; namespace can stay `fin-core-prod` or a distinct
  one — decide) in project fin-core, **type=vm/compose, runtime=compose**,
  `app_server_id`=findata.
- Move the `app_server_id` binding OFF the k8s `prod` env (set NULL there).
- Move the `profi-vm` compose git files + the DeployStack target to the new env
  slug (`.../environments/findata/apps/profi-vm/`), or keep the stack under a
  stable path and point the new env at it. The **running stack is untouched**;
  only the next redeploy uses the new path.
- Acceptance: `GetComposeDeployTarget(fin-core, findata)` → endpoint 3; k8s `prod`
  env no longer has an app_server; running containers unaffected.

### Phase 2 — DECOMPOSE: apps + infra records in the findata env
- Classify the stack's discovered services (reuse DiscoverWorkload data): app
  images → `App` (profi, profi-backend); infra images (postgres/nginx/redis/…) →
  infra kinds (`postgres` → ServiceDatabase-like, `nginx` → Ingress-like, or a
  generic `Infra` kind — decide the kind mapping).
- Write these as `resource_snapshots` in the findata env. Names match k8s 1:1
  (profi, profi-backend) — no collision (different env).
- Decide the source of truth: a one-shot classifier now, or wire it into
  DeployStack/Discovery so it stays in sync on every deploy (preferred, so it is
  reproducible for VM #2).
- Acceptance: `ListApps(fin-core, findata)` returns profi + profi-backend;
  infra listed separately; k8s `prod` env still returns its own profi/profi-backend.

### Phase 3 — FRONTEND: make the VM environment navigable
- Teach the console to show the app-server as an environment view (the app-server
  page is the natural home: "Applications" + "Infrastructure" sections for its
  env), OR re-introduce a minimal env switch scoped to VM envs.
- Must not regress the env-collapsed single-k8s-env UX for normal projects.
- Render apps 1:1 with k8s (same cards), infra in its own section.
- Acceptance: on prod, findata shows profi + profi-backend as apps + postgres +
  nginx as infra; k8s apps still show in the k8s env; no navigation regressions.

### Phase 4 — REPRODUCIBLE + LOCK-IN
- The decomposition must re-run for the next VM unchanged (classifier keyed off
  discovery, not hand-authored per VM).
- Document: app-server = env; compose stack = atomic deploy; services surfaced as
  apps+infra; release = bump tag in the stack's compose.yaml.

## Decisions (PINNED 2026-07-03)

1. **Infra kind = generic `Infra`** with a `subtype` in summary_json
   (database/proxy/cache/…). Any compose infra maps without forcing k8s semantics.
   postgres → Infra{subtype:database}, nginx → Infra{subtype:proxy}.
2. **Namespace** for the findata env: reuse `fin-core-prod` (default).
3. **Decomposition = wired into the deploy/discovery path** (reproducible for the
   next VM), not a one-shot hand-populate.
4. **Frontend = restore the env switcher** (partial revert of env-collapse):
   full multi-env navigation (k8s `prod` ⇆ `findata`). More code + regression risk
   across all projects — the env-collapsed single-env UX must stay correct when a
   project has exactly one env.

## Execution order (like the discovery feature: code → CI → deploy → prod data)

1. **Backend**: generic `Infra` kind (list endpoint / include in resource views);
   VM-env creation path; classifier (app vs infra by image) wired into
   DeployStack/Discovery so the findata env's App+Infra snapshots stay in sync.
2. **Frontend**: restore env switcher (guard: 1-env projects unchanged); render an
   Infrastructure section alongside Applications; VM env shows profi/profi-backend
   + pg/nginx.
3. **CI + deploy** the 6 images (main → build → argo).
4. **Prod data**: create env `findata` (runtime=compose, app_server=findata),
   unbind app_server from the k8s `prod` env, move the profi-vm stack path to the
   new env; let the classifier populate App+Infra. Verify UI + running stack intact.
