# Vercel-style git→build→deploy on dada-cloud — Design

Status: design (locked before implementation)
Date: 2026-06-20
Author: design pass

## Goal

Turn a `git push` into a running app on a temp domain, in minutes, zero Dockerfile —
the Vercel core loop — using dada-cloud's existing rails. Custom-domain + SSL is a
*separate parallel track* (engine already exists via cert-manager), not part of MVP.

## Key insight

dada-cloud already has 90% of the parts. The deploy path, the GitOps state machine,
ingress, and **automatic Let's Encrypt SSL** all exist today. The ONLY genuinely new
subsystem is a **build service**. Everything downstream is reuse.

| Vercel step | dada-cloud today | Gap |
|---|---|---|
| Signup (Google) | Keycloak SSO | none |
| Connect GitHub | `git_integrations` (AES-GCM tokens) — but scoped to argo-infra repo | extend: GitHub App + user-repo scope |
| Pick repo | — | list repos via GitHub App |
| Framework preset detect | — | new — buy off-shelf (Nixpacks) |
| **Build (clone → OCI image)** | — | **new — the only real engine to build** |
| Deploy | `CreateApp` / `UpdateAppImage` → Operation → gitops → Argo → pod | reuse as-is |
| Temp domain | `PublicApi` CRD → Ingress + cert-manager LE cert | reuse, auto-assign wildcard |
| Custom domain + SSL | cert-manager issues LE certs already | parallel track, engine done |

## Architecture decision: build is NOT GitOps

Build artifacts are not declarative state. You do not commit a build to git. Build is
imperative, log-heavy, ephemeral, and runs untrusted user code. Therefore:

- New worker **`build-agent`**, sibling to `gitops-agent` and `portainer-agent`.
- Build lives in its own `builds` table + k8s Jobs, NOT in the Operation/gitops machine.
- Only the *result* (an immutable image tag) re-enters the existing declarative deploy
  path via the current `UpdateAppImage` Operation. Clean separation, full reuse.

## End-to-end flow (MVP)

```
GitHub push to production branch
  → GitHub App webhook → build-agent
  → create Build row (commit_sha, branch, status=queued)
  → framework detect (Nixpacks)
  → spawn k8s Job: clone → BuildKit build → push to Harbor
       image tag = harbor.dada-tuda.ru/<proj>/<app>:<gitsha>   (immutable)
  → stream build logs → builds table + existing WS hub
  → on success: enqueue EXISTING Operation UpdateAppImage(image=<tag>)
       └─ from here current rails run: gitops-agent renders → commit argo-infra
          → ArgoCD syncs → pod live
  → auto-create/refresh PublicApi: fqdn <app>-<sha8>.apps.dada-tuda.ru
       └─ ingress-nginx-pub + cert-manager issue LE cert (existing)
  → temp domain live, https
```

The single elegant move: build-agent's success path just calls the deploy API that
already exists. Build-agent never touches Argo, Helm, or k8s workload objects directly.

## Tech choices (decided)

- **Framework detect + build plan**: **Nixpacks** (Railway/Coolify-proven, single binary,
  detects Node/Python/Go/Rust/etc, zero Dockerfile). This is the zero-config killer
  feature, bought off-shelf. Tradeoff vs Cloud Native Buildpacks (Paketo): Buildpacks
  give rebaseable layers + more "managed" feel but heavier ceremony; Nixpacks simpler and
  broader. **MVP = Nixpacks.** Revisit Buildpacks if layer-rebase / SBOM matters later.
- **Image builder (rootless, in-cluster, no Docker daemon)**: **BuildKit** (buildkitd pod).
  Faster + better cache than Kaniko. Cache exported to registry or PVC.
- **Registry**: **Harbor** in-cluster. Multi-tenant PaaS needs per-project quota, RBAC,
  vuln scan, robot accounts. Lighter alt = Zot (drop if Harbor too heavy for stage 1).
- **Git trigger**: **GitHub App** (webhook + repo listing + commit status checks back to
  PR). Reuse existing AES-GCM encrypted-token infra in `git_integrations`.

## Data model additions

```sql
-- user source repos linked to an app
git_repos (
  id, project_id, env_id, app_name,
  provider,                -- github | gitlab
  repo_full_name,          -- org/name
  github_install_id,       -- GitHub App installation
  production_branch,       -- default: repo default branch
  root_dir,                -- monorepo subdir, default '.'
  framework_override,      -- nullable; else Nixpacks autodetect
  created_at
)

-- one row per build attempt (imperative, NOT gitops)
builds (
  id, project_id, env_id, app_name,
  git_repo_id, commit_sha, branch,
  image_uri,               -- harbor.../proj/app:<sha>
  status,                  -- queued|detecting|building|pushing|success|failed|canceled
  logs_ref,                -- object-store key or builds_logs table ref
  trigger,                 -- push|pr|manual|rollback
  started_at, finished_at
)

-- immutable deploy pointer (enables instant rollback)
deployments (
  id, build_id, env_id, app_name,
  image_uri, is_current, created_at
)
```

Immutable deploy + **instant rollback for free**: tag = commit SHA → rollback = new
`deployments` row pointing at a prior build's `image_uri` → existing `UpdateAppImage`.
k8s rollout = seconds (not Vercel's ms global routing; acceptable at our scale).

## Temp domain (cheap, reuses everything)

- Wildcard DNS `*.apps.dada-tuda.ru` → cluster public LB (155.212.223.198 / ingress-nginx-pub).
- On deploy auto-create `PublicApi` fqdn `<app>-<sha8>.apps.dada-tuda.ru`.
- Per-branch alias `<app>-git-<branch>.apps.dada-tuda.ru` (same pattern, later).
- cert-manager issues the LE cert. Nothing new.

## Custom domain + SSL — PARALLEL TRACK (deferred)

Engine done (cert-manager + PublicApi). Remaining = UI + DNS-verify plumbing:
- user adds domain → show CNAME/A target → poll DNS until resolves → create PublicApi
  with their fqdn → cert-manager HTTP-01 issues cert. No new infra. Build separately.

## Phased rollout

1. **MVP — close the loop.** GitHub App → webhook on prod-branch push → build-agent
   (Nixpacks + BuildKit) → Harbor → `UpdateAppImage` → temp domain. Single repo/env,
   prod branch only.
2. **Build logs + PR status.** Stream logs to WS hub (exists); post commit status to GitHub.
3. **Rollback + deployments table.** Re-point to prior SHA.
4. **Preview envs.** PR → ephemeral environment (multi-env per project already exists) +
   branch domain + teardown on PR close.
5. **Custom domain + SSL.** Parallel track above.

## Hard parts (honest)

- **Multi-tenant build isolation** (the real security work): untrusted user code building
  in your cluster. Need gVisor/Kata runtime OR dedicated build node pool, NetworkPolicy
  deny-all egress except registry+git, NO cluster creds / serviceaccount token in build
  Jobs, per-build ephemeral namespace, CPU/mem/timeout quotas.
- **Build cache** across builds: BuildKit cache → registry-backed or per-app PVC. Perf only.
- **VM track builds**: skip MVP. Build in k8s; image is portable → later deploy same image
  to Beget VM via docker-compose.
- **What we will NOT match**: Vercel's millisecond global routing layer (126 PoPs, alias→
  deployment pointer). Don't try. k8s rollout in seconds is fine for our scale.

## New components summary

| Component | Type | Job |
|---|---|---|
| `build-agent` | new Go worker | webhook intake, build orchestration, log stream, deploy handoff |
| GitHub App | new external | repo list, webhooks, commit status |
| Harbor | new infra (helm) | image registry, quota, RBAC, scan |
| BuildKit | new infra | rootless in-cluster image build |
| build node pool / gVisor | new infra | build isolation |
| `git_repos`/`builds`/`deployments` | new tables | source link, build history, deploy pointer |
| build UI (connect repo, logs, rollback) | frontend | console pages |

Reused unchanged: Operation machine, gitops-agent, ArgoCD, Crossplane App, PublicApi,
cert-manager, ingress-nginx-pub, Keycloak, WS hub.
