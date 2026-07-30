# VM App-Settings parity + full custom-domain wiring

**Goal:** the console App Settings page is k8s-only. For a VM (compose) environment it shows the wrong surface (empty helm config, ungated k8s mutations). Make every tab VM-aware and wire custom domains end-to-end for VM.

**Owner direction (2026-07-23):**
- Config (helm) -> this app's compose service slice (not the whole compose.yaml, not helm values)
- Storage -> this app's compose volume
- Resources -> removed for VM (k8s cpu/mem profile n/a)
- Domains -> nginx ingress + FULL custom-domain wiring (domain_hostnames -> compose ingress + TLS)
- Env -> keep (already works)
- Backend runtime guards -> yes, defense-in-depth

**Discriminator:** `environments.runtime IN ('k8s','vm')` (+ `app_server_id`). An "App" is a `resource_snapshots` row `kind='App'`; VM app summary_json = `{runtime:"compose", desired:{image,ports,volumes,compose}}`.

**Constraints:**
- Trunk-based on `main` (single release line, M4 n/a). Auto-push after each commit.
- Stage explicit paths, never `-A` (parallel worktrees p00b-work / worktree-agent-* exist). (M3)
- Frontend is a CUSTOMIZED Next.js — read `node_modules/next/dist/docs/` before writing Next code (frontend/AGENTS.md).
- New backend routes -> `swag init -o internal/api/docs` or TestOpenAPICoverage gates CI.
- No source comments (house rule); godoc/docstrings only.
- Phase 5 touches prod routing + TLS + git commits to user repos -> highest care, verify live.

---

## Phase 0 - Grounding (DONE)
- [x] Agent A: compose config model. KEY: ONE aggregate compose.yaml per ENV ({proj}-{env} Portainer stack); each App = one service block. Source of truth = DB snapshot `desired` (Image/Ports/Volumes; `desired.compose` map for ADOPTED) + `env_vars` table. Per-app `apps/<n>/compose.yaml` is LEGACY/superseded; the raw /compose WS editor edits DEAD files (save does NOT run renderEnvAggregate). Mutation template = `updateComposeAppImage` (dbwatcher.go:1548): patch snapshot.desired -> UpsertSnapshot -> renderEnvAggregate. UpdateAppEnvVarsPayload declared but NOT wired; env edits ride SetEnvVar (env_vars table) + take effect next deploy.
- [x] Agent B: domains/TLS. KEY: whole custom-domain path is k8s-hardwired. doAttachCustomHostname renders k8s Ingress into resources.values.yaml which renderEnvAggregate NEVER reads -> no-op on VM. VM nginx ingress (CreateIngress, ingress.go:55) takes hosts from user-typed payload; domain_hostnames unread; NO frontend caller, NO read/update endpoint (console only visualizes, read-only IngressDetail). DNS target = k8s LB (wrong for VM; need AppServer.VMIP). **TLS: ZERO automation for VM** — certs hand-placed on host /etc/letsencrypt, paths user-supplied. No ACME/certbot/cert-manager for VM.

## Phase 1 - Backend runtime guards (DONE + VERIFIED, uncommitted)
- [x] Helper: envRuntime + requireVM/requireK8s + valuesFile*ForRuntime (internal/api/runtime_guard.go NEW)
- [x] UpdateAppProfile: explicit 400 for VM (requireK8sRuntime) - apps.go
- [x] Reverse guards: RollbackApp / RestartApp / AdoptApp -> requireVMRuntime (400 for k8s) - apps.go
- [x] GetValuesToken: runtime-aware file gate (VM: compose.yaml/.env; k8s: values.yaml) - apps_values.go
- [x] (UpdateAppStorage: left k8s-guarded until Phase 4 repurposes it for VM)
- [x] Verified: gofmt clean, go build ./... 0, go vet 0, go test ./internal/api/... PASS (coverage gate ok)

## Phase 2 - Frontend per-runtime tab set
- [ ] settings/page.tsx: read selectedEnv.runtime; VM tab set = [Env, Config, Storage, Domains, Git], DROP Resources
- [ ] Surface the orphan /compose route (fold into Config tab for VM or link)
- [ ] tsc/lint/build green

## STATUS
- Phase 1 backend guards: PUSHED 4d1c409 (verified).
- Phase 3+4 BACKEND + gitops-agent: PUSHED 6b788d6 (verified: build/vet, swag coverage gate, go test api + gitops worker/renderer). New ops UpdateComposeConfig, UpdateComposeVolume. Worker patches desired -> renderEnvAggregate.
- Phase 2 + 3/4 FRONTEND: COMMITTED LOCAL 6328734, NOT PUSHED (verified: tsc/eslint/next build green, i18n ru+en, api contract match, port-regex widened for compose syntax). Editors read desired.compose.* ?? desired.*; bind-mount reject mirrors server.
- ALL PUSHED: owner authorized combined push; origin/main 6b788d6 -> d804a7d (my 6328734 + 10 concurrent dbmove commits). Jenkins triggered; NOT yet confirmed green (findJobsWithScmUrl errored, plugin ClassCastException; local verify was green).
- Phase 5 (domains, BYO TLS): SHIPPED 2be63f0 (pushed). VMIngressSpec.ExtraHosts serving vhost + BYO cert `/etc/nginx/certs/live/<host>/`; attach/detach worker VM-branch mutates ingress custom_hosts; doCreateIngress persists key_path+custom_hosts; recordTarget->VMIP for VM; frontend Domains tab re-added. REVIEW CATCH: rebuildIngressCompose REFUSES when ingress has basic_auth (path not persisted -> would silently drop password gate). NOT live-verified (needs real VM + operator cert). Inert until a domain is attached.
- ALL 5 PHASES SHIPPED. Jenkins builds NOT confirmed green (API errored). Live-VM verification is the remaining gate for Phase 3/4/5 behaviour.

## Phase 3 - Config tab = compose service-slice editor (VM)  [tractable]
- Read GET: app's summary_json.desired (Image/Ports/Volumes for authored; desired.compose map for adopted)
- Write: NEW op `UpdateComposeConfig` (backend handler copy RollbackApp + typed payload) -> gitops-agent NEW dispatch case + handler patching snapshot.desired -> UpsertSnapshot -> renderEnvAggregate (template = updateComposeAppImage dbwatcher.go:1548)
- Frontend: new structured editor mirroring CommonConfigEditor, mounted for VM in Config tab
- DEPLOY ORDER: gitops-agent handler must ship BEFORE frontend enqueues the op (else op claimed -> dispatch default -> fails). Land backend+agent, verify live, then frontend.

## Phase 4 - Storage tab = compose volume editor (VM)  [tractable]
- NEW op `UpdateComposeVolume` patching desired.volumes (per-service mount) / desired.stack_volumes -> renderEnvAggregate re-derives external pins (externalVolumesFor) — data-safe pin preserved
- Same deploy-order rule as Phase 3

## Phase 5 - Domains = full compose custom-domain wiring (VM)  [BLOCKED on owner decision]  [RISKIEST]
Whole path is k8s-hardwired; needs compose branches in doAttachCustomHostname / doDetach / doAttachDefaultDomain, DNS target -> AppServer.VMIP, wire domain_hostnames -> VM nginx ingress spec -> re-render nginx conf into ingress App desired.compose -> renderEnvAggregate. Plus a create/edit ingress endpoint+UI (none today).
- [ ] **BLOCKER: VM TLS issuance strategy** (no automation exists). Options: (a) BYO — user uploads/points to host cert; (b) certbot sidecar on VM (HTTP-01 through the nginx); (c) platform issues LE cert + pushes to VM. Owner must choose — prod routing + cert lifecycle + security.
- [ ] After decision: implement chosen TLS path + domain wiring + verify live (DNS + cert + 200 through VM nginx)

## Phase 6 - Verify + ship
- [ ] Backend: go build ./... + go test ./... green; swag init if new routes
- [ ] Frontend: build green
- [ ] Live smoke on the reported app (env 5bff9f95)
- [ ] Review section below

---

## Review
(to fill in after implementation)

---

## SEO weekly loop (2026-07-31)

Plan and evidence: `tasks/seo/PLAN.md`, baseline `tasks/seo/2026-07-30.md` / `.json`.

- [x] Pull real data (Webmaster v4 + Metrika stat v1), classify all 97 shows into intent clusters
- [x] 301 the five dead slugs (ru+en) in `next.config.ts`
- [x] Link the six orphan landings from the footer
- [x] Apex `dada-tuda.ru` cert + 301 to `cloud.dada-tuda.ru`
- [x] Stop serving 5xx to crawlers during a frontend roll (`URL_ALERT_5XX` now ABSENT)
- [x] Ship `/oplatit-vercel-iz-rossii` and `/rabotaet-li-vercel-v-rossii` (ru+en) for the payment-block cluster
- [x] Fix `FaqList` — answers were never in the DOM, so every landing read as thin to a crawler
- [x] Expand the ten sub-500-word pages past ~900 words of page-specific content
- [x] Submit the changed URLs to IndexNow once the deploy is live — 94 URLs, Yandex 202, Bing 200
- [x] Verify live after the roll: 94/94 sitemap URLs 200, new payment pages 1112-1482 words
- [ ] Watch, do not pre-expand: 32 thin `/developer/*` docs pages (442-668 words). Expand only if
      the weekly pull shows them indexed then dropped as `LOW_QUALITY`
- [x] **Owner did 07-31:** region set via Yandex Business UI — diagnostics not re-evaluated yet
      (`last_state_update` still 07-30T13:35), verdict deferred to the 08-06 pull
- [x] **Owner did 07-30:** Google Search Console verified, `/sitemap.xml` processed Успешно,
      88 URLs discovered. Real Googlebot started crawling 07-30 17:17 (ingress logs: `/` 200,
      `/analog-railway` 200, CSS chunk = rendering service). 0 clicks is not yet a signal — the
      Performance chart ends 07-28, before submission
- [x] Fix the apex redirect dropping the path (`dada-tuda.ru/robots.txt` -> homepage HTML);
      host-conditioned 301 in `next.config.ts`, verified locally
- [ ] Repoint the apex ingress at the frontend service so that redirect takes effect
      (argo-infra `cloud-console/resources.values.yaml`, drop `permanent-redirect`)
- [ ] **Only remaining Google lever: backlinks.** A fresh third-level domain with none will not
      rank no matter how many pages it has
- [ ] 2026-08-06: re-run `scripts/seo-weekly.py`, grade the three standing predictions in PLAN.md
