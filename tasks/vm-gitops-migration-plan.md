# VM → full GitOps: a reproducible flow (fin-data first)

Bring an existing "hand-run containers" VM onto GitOps rails (Portainer stack
pulled from git) **without losing prod data**, and do it as a **reproducible,
VM-agnostic flow** — not a one-off manual setup. fin-data is the first subject;
the same flow must work for the next VM unchanged.

Two hard constraints (from the owner):

1. **SSH is READ-ONLY analysis only.** No hand-fixing, restarting, `docker rm`,
   or reconfiguring the workload over SSH. Every *mutation* of the VM goes
   **through the cloud** — Portainer (via `portainer-agent`) and gitops. The one
   sanctioned SSH *write* is the console-orchestrated edge-agent bootstrap
   (additive, idempotent, never touches prod containers); after that, SSH is
   never used to change the workload again.
2. **Reproducible flow, not a bespoke fin-data cutover.** Each step is a
   parameterized capability (VM = any endpoint, app = any compose app) that we
   can re-run for VM #2, #3, … The dominating risk — the **Postgres data volume
   name** — is mitigated by tooling, not by remembering to do the right thing.

End state: a new release is **bump the image tag digit in the gitops
`compose.yaml` → console redeploys → Portainer pulls the new image**, PG volume
untouched — the exact same path for every VM.

## The reproducible flow (overview)

```
┌ Phase 0  DISCOVER ──────────── read-only SSH ──────────────────────────────┐
│  scripts/vm-discover.sh user@host  →  inventory + volumes.compose.yaml      │
│  (auto-pins every named volume external — the data-safety artifact)         │
└─────────────────────────────────────────────────────────────────────────────┘
┌ Phase 1  AUTHOR ────────────── git (cloud) ────────────────────────────────┐
│  gitops compose.yaml + .env that MATCH prod byte-for-byte, external volumes │
└─────────────────────────────────────────────────────────────────────────────┘
┌ Phase 2  ENROLL ───────────── console-driven (one SSH bootstrap) ──────────┐
│  edge agent onto the VM → Portainer controls docker → SSH retired           │
└─────────────────────────────────────────────────────────────────────────────┘
┌ Phase 3  REHEARSE ──────────── cloud, throwaway ───────────────────────────┐
│  prove external-volume adoption + the release bump on disposable infra      │
└─────────────────────────────────────────────────────────────────────────────┘
┌ Phase 4  CUTOVER ──────────── 100% cloud (Portainer/gitops) ───────────────┐
│  backup → cloud-stop old containers → deploy stack (adopts volume) → verify │
└─────────────────────────────────────────────────────────────────────────────┘
┌ Phase 5  LOCK-IN ──────────── gitops ──────────────────────────────────────┐
│  release = bump tag in git; ban down -v; same flow for the next VM          │
└─────────────────────────────────────────────────────────────────────────────┘
```

Everything from Phase 2 on is cloud/gitops. SSH appears only in Phase 0 (read)
and once in Phase 2 (console bootstrap).

## Current state (verified from code)

- **fin-data VM** (`findata`, Portainer edge endpoint 3) was brought in by the
  `beget-reader` **adopt** path (`portainer-agent/internal/worker/beget_reader.go`,
  `terraform/adopt.go`): `app_servers` row + frozen Terraform `import {}`
  (`ignore_changes = all`). **SSH bootstrap + Portainer enrollment were skipped**
  → **no edge agent yet**, so Portainer can't manage it. Prod (postgres + two
  apps) runs as **hand-created docker containers** managed by nobody.
- **Deploy rails** (target): `gitops-agent` renders
  `clusters/beget-prod/projects/<slug>/environments/<env>/apps/<app>/compose.yaml`
  + `.env`; `portainer-agent` `doDeployStack` (`worker/deploy_stack.go`) does
  `RedeployStack` if the stack exists else `CreateStackFromGit` (Portainer runs
  `docker compose up`, **project name = stack name**).
- **Already-built cloud primitives** we lean on (no SSH):
  - `portainer/client.go`: `CreateEdgeEndpoint`, `EnsureEdgeCompute`,
    `EnsureEdgeGroup`/`TagEndpoint`, `CreateEdgeStackFromGit`,
    `CreateStackFromGit`/`RedeployStack`, **`ListContainers`**, `StreamLogs`.
  - ⇒ once the edge agent is on, Portainer's docker proxy can **read** the
    workload (ListContainers) with no SSH at all.
- **Gap for a cloud-native cutover:** the client can *list* containers but not
  *stop/remove* them. The one place the flow needs to retire the hand-run
  containers (they lock the PG datadir; two postgres on one volume = corruption)
  currently has no cloud verb. See **Phase 4 / decision**.

## The core risk, precisely (PG volume)

`docker compose up` names a service's named volume `"<project>_<vol>"` and
**creates it if absent**. Deployed as stack `fin-data-db`, a naive
`volumes: { pgdata: {} }` yields a brand-new empty `fin-data-db_pgdata` →
Postgres `initdb`s a fresh cluster → **the real prod volume is orphaned** and a
later `down -v`/prune **deletes** it. That is the data-loss outcome.

**Mitigation (automated in Phase 0):** the gitops compose declares every
stateful volume **external**, pinned to the literal live name, so compose
*adopts* it:

```yaml
volumes:
  pgdata:
    external: true
    name: <EXACT live volume name>   # emitted by scripts/vm-discover.sh
```

`scripts/vm-discover.sh` generates this block straight from `docker inspect`, so
the name is never hand-typed. Bind mount instead of named volume? The report
flags it; mirror the host path verbatim.

## Phase 0 — DISCOVER (read-only SSH) ✅ tooling landed

Run the committed, read-only inventory tool. Zero mutation; every remote command
is `inspect`/`ls`/`version`:

```bash
scripts/vm-discover.sh root@<fin-data-ip> -i <readonly_key> -o ./vm-discovery
```

Produces per VM: `REPORT.md` (images, tags, ports, restart, networks, mounts),
`inspect/*.json` (the record), `volumes.json`, and the safety artifact
`volumes.compose.yaml` (**external-volume block, names auto-pinned**).

Also capture the **real DB creds**: `POSTGRES_*` env is only read at first
`initdb`; the prod volume is already initialised, so those are cosmetic for the
DB — but the apps' DSNs must use the credentials baked in at init. Pull them from
the app containers' env in `inspect/*.json`, not from a guess.

Reproducible: same command, any VM. This is the only SSH the flow needs for
analysis.

## Phase 1 — AUTHOR the gitops compose (git / cloud)

Under `clusters/beget-prod/projects/<fin-data-slug>/environments/prod/apps/<app>/`:

- `compose.yaml` reproducing Phase-0 reality: exact current **image tags** (this
  is the baseline the future "bump the digit" diffs against), matching ports and
  networks, and the **`volumes.compose.yaml` external block pasted verbatim**.
- `.env` with the real runtime env / DSN from Phase 0. (Plaintext-in-git per the
  platform's existing convention — `renderer.RenderEnvFile` treats the repo as a
  secret store. Confirm acceptable for these creds or route via the platform's
  secret path.)

Acceptance: a `docker compose config` dry render shows the datadir bound to the
**existing external volume**, no volume redefinition.

## Phase 2 — ENROLL the edge agent (console-driven; SSH retired after)

Get an Edge Agent onto the VM so all further work is cloud-native. Use the
existing manual-connect op (`doCreateManualAppServer` → `RunBootstrap`).
`bootstrap.sh.tmpl` is safe for a live prod VM: Docker install is skipped if
present; it only **adds** `portainer_edge_agent` (+ optional obs sidecars) and
never stops/removes/reconfigures prod containers or volumes.

- This is the one sanctioned SSH *write*, orchestrated by the console (not a
  human hand-editing the box). After it, endpoint 3 is online in Portainer and
  **SSH is never used on this VM again**.
- Verify prod still up + agent green. **Deploy no stack yet.**
- From here, discovery can also be re-confirmed via `ListContainers` (Portainer
  docker proxy) with no SSH — useful for VM #2+ as a cross-check.

## Phase 3 — REHEARSE (cloud, throwaway)

On a disposable VM/endpoint, prove the two mechanics that must never fail on
prod, entirely through the cloud path:

1. Init a Postgres in a named volume, write a sentinel row.
2. Deploy the Phase-1-style compose (external volume pinned) via the real
   `CreateStackFromGit` path. Assert: sentinel survives, **no new `<stack>_*`
   volume** appears.
3. Simulate a release: bump the app image tag in git → redeploy → app container
   recreated, **DB volume untouched**.
4. Exercise the Phase-4 takeover mechanism (below) end-to-end here first.

Green here gates prod.

## Phase 4 — CUTOVER (100% cloud: Portainer + gitops)

The one moment we hand running, non-compose containers to compose management.
Data is preserved because the volume is external; the sequence is fully
cloud-driven (no SSH):

1. Maintenance window with the customer.
2. **Backup ×2, via the cloud path** (Portainer exec / a one-shot job container
   on the endpoint — not an SSH session):
   - `pg_dump -Fc` the DB;
   - volume-level `tar` of the PG volume;
   - copy both off-box.
3. **Retire the hand-run containers via the cloud** (they hold the datadir lock;
   a second postgres on the same volume corrupts it — so the old ones must stop
   *before* the stack starts). This needs a cloud stop/remove — see the decision
   below.
4. **Deploy the stack** (`doDeployStack` → `CreateStackFromGit`). Portainer runs
   `up`; Postgres attaches the **existing external volume**; apps come up on
   their real image tags.
5. **Verify** (Phase 5) before closing the window.

Fail-safe: never `down -v`, never prune. On any anomaly, restore from backup; the
external volume survives every path except an explicit manual delete.

### Decision needed — how the cloud retires the old containers

To keep the cutover reproducible and SSH-free, the flow needs a cloud verb to
stop/remove the pre-existing containers. Options:

- **A. Add `portainer-agent` container-lifecycle methods** (`StopContainer`,
  `RemoveContainer` over Portainer's docker proxy) + a small **"adopt VM"
  worker op** that: lists containers → stops the ones the stack will replace →
  deploys the stack → optionally removes the stopped originals. Fully
  reproducible, one console action per VM. *(New Go code in the prod-facing
  agent; most work, best long-term.)*
- **B. Operator does it in the Portainer UI** (stop the containers), then the
  console deploys the stack. No new code; reproducible-by-runbook, not by button.
- **C. compose without matching `container_name`** so there's no name collision,
  but this does NOT solve the datadir lock — the old postgres must still stop
  first — so C alone is insufficient and is rejected.

Recommendation: **A** (matches "reproducible flow, all via cloud"), with **B** as
the interim for the fin-data first-run so we don't block on new agent code.

## Phase 5 — LOCK-IN (gitops)

Verify: `ListContainers`/`docker volume ls` show only the original PG volume (no
`<stack>_*`); row/sentinel counts match; apps healthy; Portainer shows fin-data
as a git stack on endpoint 3.

Lock the release flow (the point of all this):

- **Release = edit the image tag in the app's `compose.yaml` in git → console
  deploy op → `RedeployStack{PullImage:true, Prune:false}`.** New image pulled,
  app container recreated, PG (external) untouched. Same for every app/VM.
- Keep `Prune:false`; document a hard **ban on `docker compose down -v`** for VM
  stacks (volume lifecycle is out-of-band/manual only).
- The whole flow is now repeatable for VM #2: `vm-discover.sh` → author → enroll
  → cutover, no bespoke steps.

## Risk register

| Risk | Mitigation |
|------|------------|
| New empty PG volume on first `up` (data loss) | `external: true` + literal `name:` auto-emitted by `vm-discover.sh`; asserted in Phase 3 and Phase 5. |
| Volume is a bind mount, not named | Phase-0 report flags it; mirror host path verbatim. |
| Two postgres on one datadir during cutover (corruption) | Old containers stopped via the cloud **before** stack deploy (Phase 4 decision A/B). |
| Wrong DB creds in `.env` | Real init-time creds captured from `inspect/*.json`; data safe regardless. |
| Accidental `down -v`/prune | `Prune:false` kept; external volume; explicit ban documented. |
| SSH used to hand-fix prod | Banned by constraint 1; only read-only discovery + one console bootstrap. |
| Bootstrap disturbs prod | Idempotent, Docker-guarded, additive; verified before any deploy. |
| Secrets plaintext in git `.env` | Accept per platform convention or route via secret path; call out before writing. |

## Execution checklist

- [x] **Phase 0** tooling: `scripts/vm-discover.sh` (read-only inventory +
      external-volume block). Run it against fin-data to fill the inventory.
- [ ] **Phase 1** author `compose.yaml` + `.env`; dry-render diff vs inspect.
- [ ] **Phase 3** rehearse adoption + release bump on throwaway (before prod).
- [ ] **Phase 2** enroll edge agent; verify prod untouched + endpoint online.
- [ ] **Phase 4** decision A vs B for cloud container-retire; backup ×2; cutover.
- [ ] **Phase 5** verify volume identity + data; document release-bump + `down -v`
      ban; confirm the flow re-runs unchanged for the next VM.
```
