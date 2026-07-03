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
┌ Phase 3  REHEARSE ──────────── throwaway (tool: vm-rehearse.sh) ───────────┐
│  proves external-volume adoption + release bump + negative control (green)  │
└─────────────────────────────────────────────────────────────────────────────┘
┌ Phase 4  CUTOVER ──────────── backup → manual stop → cloud deploy ─────────┐
│  backup ×2 → operator stops old containers by hand → deploy stack → verify  │
└─────────────────────────────────────────────────────────────────────────────┘
┌ Phase 5  LOCK-IN ──────────── gitops ──────────────────────────────────────┐
│  release = bump tag in git; ban down -v; same flow for the next VM          │
└─────────────────────────────────────────────────────────────────────────────┘
```

Everything is cloud/gitops except three bounded manual touches: Phase 0 read-only
discovery, the one console-orchestrated bootstrap in Phase 2, and a single manual
`stop` of the old containers at cutover (Phase 4). No cloud kill verb is added.

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
- **Secret channel (verified):** creds live in the `env_vars` table
  (`gitops-agent/internal/worker/envvars.go`) — **AES-GCM encrypted at rest**,
  decrypted **only at render time** (`resolveRuntimeEnv`), **never** in
  `operations.payload`. For the compose/VM track the renderer merges plain +
  secret into the app's `.env` (`doCreateComposeApp`, `dbwatcher.go:617-633`);
  that `.env` still lands plaintext in the gitops repo (no SealedSecret channel
  for compose today — the platform ceiling). So "the secret channel" means:
  register creds as `env_vars(is_secret=true)`, let the agent render `.env` — do
  **not** hand-write `.env` into git.
- **Retiring the hand-run containers is a MANUAL, non-cloud step (owner
  decision):** we do **not** add a cloud stop/remove verb to `portainer-agent`.
  Before the stack deploy the operator stops the old containers by hand (Portainer
  UI preferred; SSH allowed for the stop only), **after a mandatory backup**. No
  new Go code in the prod-facing agent. See **Phase 4**.

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
- **Secrets via the platform secret channel — do NOT hand-write `.env` into git.**
  Register the Phase-0-discovered runtime env (PG creds, the apps' DSN) as
  `env_vars` rows for this compose app via the console, `is_secret=true`,
  `scope=runtime`. They are stored **AES-GCM encrypted at rest** and the
  gitops-agent renders the `.env` at deploy time (`resolveRuntimeEnv` →
  `RenderEnvFile`); creds never pass through `operations.payload`. Single source,
  editable in console, re-resolved on redeploy.
  - Honest caveat: on the compose track the rendered `.env` still lands as
    plaintext in the gitops repo (no SealedSecret for compose today). This is the
    platform's current ceiling and matches every other app; the win is *encrypted
    at rest + single source + not in payloads*, not *secrets never in git*.
  - The creds MUST be the **real init-time creds baked into the existing PG
    volume** (from Phase 0 `inspect/*.json`), because the external volume is
    already initialised — the apps' DSN has to match what's on disk.

**Authoring mechanism (verified in code):** compose apps are edited through the
console's file editor (`gitops-agent` `handleFileWS`) — `compose.yaml` and `.env`
are committed to git on save. `RenderComposeSkeleton` only seeds an nginx
skeleton at create; the **real compose.yaml is authored by editing that file**
(paste the Phase-0 images/ports/networks + the external-volume block). `.env`
renders from `env_vars` **at create time** (`doCreateComposeApp`), so register the
creds *before* creating the app; thereafter the editor can also write `.env`
directly.

**Sequencing constraint (important):** every `compose.yaml`/`.env` save
**auto-enqueues a `DeployStack`** (`ws_handler.go:174`). So finalize authoring
**while the endpoint is NOT yet enrolled** (Phase 2) — an enqueued deploy can't
land without an edge agent, so it's harmless. This is why **author (Phase 1)
precedes enroll (Phase 2)**: it makes an accidental early deploy to prod
impossible. Do not edit these files again until the Phase-4 window.

Acceptance: a `docker compose config` dry render shows the datadir bound to the
**existing external volume**, no volume redefinition; the rendered `.env` carries
the Phase-0 creds (not guesses).

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

## Phase 3 — REHEARSE (throwaway) ✅ tooling landed + green

`scripts/vm-rehearse.sh` proves the two mechanics that must never fail on prod,
on throwaway local Docker, self-cleaning. Run it; it must exit 0 before any prod
cutover. It asserts:

1. Seed a stand-in "prod" PG named volume + sentinel row, then stop the container
   (data stays in the volume) — mirrors the Phase-4 manual stop.
2. **Adoption:** deploy a compose with the volume pinned `external: true` →
   asserts **no fresh `<stack>_pgdata`** appears and the **sentinel survives**.
3. **Release bump:** change the app image tag → redeploy → asserts the **DB
   volume is untouched** and the sentinel is intact.
4. **Negative control:** a naive (non-external) compose → asserts it creates a
   fresh empty `<stack>_pgdata` with **no sentinel** — the exact data-loss the
   external pin prevents, made concrete.

Verified: `ALL CHECKS PASSED`, exit 0, zero leftovers. The one gap vs prod is the
transport (local `docker compose` here vs `CreateStackFromGit` on the endpoint);
the volume-identity + release semantics it proves are transport-independent. Re-run
after Phase 1's real compose/`.env` exist to rehearse them verbatim on a
disposable endpoint.

## Phase 4 — CUTOVER (backup → manual stop → cloud deploy)

The one moment we hand running, non-compose containers to compose management.
Data is preserved because the volume is external. Owner decisions baked in:
**no cloud kill verb — the operator stops the old containers by hand; a backup is
mandatory first.**

1. Maintenance window with the customer.
2. **Backup ×2 — MANDATORY, before touching anything:**
   - `pg_dump -Fc` the DB;
   - volume-level `tar` of the PG volume;
   - copy both **off-box** and verify they restore.
   Run via Portainer exec / a one-shot job container (cloud) or read-path SSH —
   either is fine, it is read-only against the data.
3. **Operator stops the hand-run containers by hand** (Portainer UI preferred,
   still cloud; SSH `docker stop` permitted for the *stop only*). They hold the
   datadir lock — a second postgres on the same volume corrupts it, so the old
   postgres must be **stopped before the stack starts**. Stop is enough; no
   `rm` needed (the stack uses its own container names + external volume). **No
   new agent code for this — deliberate.**
4. **Deploy the stack via the cloud** (`doDeployStack` → `CreateStackFromGit`).
   Portainer runs `up`; Postgres attaches the **existing external volume**; apps
   come up on their real image tags with the `.env` rendered from the secret
   channel (Phase 1).
5. **Verify** (Phase 5) before closing the window. Only after green: optionally
   remove the now-stopped original containers by hand (never `-v`).

Fail-safe: never `down -v`, never prune. On any anomaly, restart the old
containers / restore from backup; the external volume survives every path except
an explicit manual delete.

The only manual, non-gitops touch in the whole flow is this one **stop** (plus
read-only discovery). Everything else — author, enroll, deploy, future releases —
is cloud/gitops and reproducible for the next VM.

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
| Two postgres on one datadir during cutover (corruption) | Old containers **manually stopped before** stack deploy (Phase 4); mandatory backup ×2 first. |
| Wrong DB creds in `.env` | Real init-time creds captured from `inspect/*.json`, entered via secret channel; data safe regardless. |
| Accidental `down -v`/prune | `Prune:false` kept; external volume; explicit ban documented. |
| SSH used to hand-fix prod | Read-only discovery + one console bootstrap + one manual `stop` at cutover; nothing else. |
| Bootstrap disturbs prod | Idempotent, Docker-guarded, additive; verified before any deploy. |
| Secrets in git `.env` | Registered as `env_vars(is_secret=true)` — encrypted at rest, not in payloads; rendered `.env` still plaintext-in-git (platform ceiling, accepted). |

## Execution checklist

- [x] **Phase 0** tooling: `scripts/vm-discover.sh` (read-only inventory +
      external-volume block). Run it against fin-data to fill the inventory.
- [ ] **Phase 1** author `compose.yaml` (external volume) in git; register creds
      via secret channel (`env_vars`, no hand `.env`); dry-render diff vs inspect.
- [x] **Phase 3** tooling: `scripts/vm-rehearse.sh` (adoption + release + negative
      control) — verified `ALL CHECKS PASSED`, exit 0, self-cleaning. Re-run on a
      disposable endpoint once Phase-1 compose/`.env` exist.
- [ ] **Phase 2** enroll edge agent; verify prod untouched + endpoint online.
- [ ] **Phase 4** backup ×2 → manual stop of old containers → cloud deploy stack.
- [ ] **Phase 5** verify volume identity + data; document release-bump + `down -v`
      ban; confirm the flow re-runs unchanged for the next VM.
```
