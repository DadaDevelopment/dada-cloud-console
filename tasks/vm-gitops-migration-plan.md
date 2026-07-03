# fin-data VM → full GitOps migration plan

Move the **fin-data prod VM** from its current "partially imported, hand-run
containers" state onto the same GitOps rails the rest of the platform uses
(Portainer edge stack pulled from git), **without losing prod data**. The
single dominating risk is the **Postgres data volume name**: a careless first
`docker compose up` creates a fresh empty volume and the live DB re-initialises
on top of nothing while the real data volume is orphaned.

End state: the next fin-data release is just **bump the image tag digit in the
gitops `compose.yaml` → console redeploys → Portainer pulls the new image**, PG
volume untouched.

## Current state (verified from code)

- **VM (`findata`, Portainer edge endpoint 3)** was brought in by the
  `beget-reader` **adopt** path (`portainer-agent/internal/worker/beget_reader.go`,
  `terraform/adopt.go`):
  - an `app_servers` row exists + a Terraform `import {}` with
    `lifecycle { ignore_changes = all }` — the VM is *frozen/imported*, never
    mutated by TF.
  - **SSH bootstrap and Portainer edge enrollment were deliberately skipped**
    ("an externally created VM has no agent credentials we control").
  - ⇒ **no Portainer Edge Agent on the VM**, so Portainer cannot push any
    stack to it yet. Prod runs as **manually-created docker containers**
    (postgres + the two apps), managed by nobody but the customer/us by hand.
- **Dev** runs in the k8s cluster (Helm app path); **prod** is this VM. Same two
  apps, two runtimes. Dev is our free rehearsal environment — it costs nothing
  to break.
- **How GitOps deploy works** for VM/compose apps (the target rails):
  - `gitops-agent` renders `compose.yaml` + sibling `.env` into the gitops repo
    at `clusters/beget-prod/projects/<projectSlug>/environments/<envSlug>/apps/<app>/compose.yaml`
    (`renderer.AppComposeGitPath`).
  - `portainer-agent` `doDeployStack` (`worker/deploy_stack.go`):
    - if a Portainer **stack with that name already exists** → `RedeployStack`
      (`PullImage:true, Prune:false`);
    - else → `CreateStackFromGit` (Portainer clones the gitops repo, runs
      `docker compose -f compose.yaml up` with **project name = stack name**).
  - The existing prod containers are **not** a Portainer stack, so the very
    first deploy takes the `CreateStackFromGit` branch — this is the dangerous
    moment.

## The core risk, precisely

`docker compose up` names a service's named volume `"<project>_<volume>"` and
**creates it if absent**. Portainer's project = the stack name we choose. So a
naive compose like:

```yaml
services:
  db:
    image: postgres:16
    volumes: [ pgdata:/var/lib/postgresql/data ]
volumes:
  pgdata: {}
```

deployed as stack `fin-data-db` produces a brand-new empty volume
`fin-data-db_pgdata`. Postgres sees an empty datadir → runs `initdb` → a fresh
cluster. The real prod data (in whatever volume it lives today) is **orphaned**,
and if anyone later runs `docker compose down -v` / Portainer prune, it is
**deleted**. That is exactly the "проебать данные прода" outcome.

**Fix:** the gitops compose must attach Postgres to the *exact existing* docker
volume, declared **external**, so compose *adopts* it instead of creating one:

```yaml
services:
  db:
    image: postgres:16.4        # exact current prod tag — see Phase 0
    container_name: <existing>  # match the running container name
    restart: unless-stopped
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  pgdata:
    external: true
    name: <EXACT existing volume name from docker inspect>   # ← the whole ballgame
```

`external: true` ⇒ compose never creates or destroys it; it must already exist,
and compose binds the live data. `name:` pins the literal docker volume name,
independent of the stack/project prefix.

> If prod uses a **bind mount** (`/var/lib/postgresql/data` → a host path)
> instead of a named volume, mirror that host path verbatim in the compose
> `volumes:` short syntax instead of a named external volume. Discovery (Phase 0)
> tells us which it is — **do not assume**.

## Guiding principles

1. **Adopt, never recreate.** The compose must describe prod *as it already
   runs* (image tags, volume, container names, ports, networks, env), so the
   first `up` is a no-op-ish takeover, not a rebuild.
2. **Read-only discovery before touching anything.** All of Phase 0 is
   `inspect`/`ls` — zero mutation.
3. **External volumes for every stateful mount.** PG first, but also any other
   data volume (uploads, redis, etc.). Never let compose own prod data's
   lifecycle.
4. **Never `down -v`, never prune.** The redeploy path already sets
   `Prune:false`; keep it. Document a hard ban on `docker compose down -v` for
   this stack.
5. **Rehearse on dev / a throwaway VM first.** Prove the external-volume
   adoption on something disposable before prod.
6. **Full backup immediately before the one cutover.** `pg_dump` **and** a
   volume-level copy. Cutover only inside a maintenance window.

## Phase 0 — Discovery (read-only, on the prod VM)

Capture the ground truth and paste it into this doc / an ops ticket. Nothing
here changes prod.

```bash
# every running container, its image (with exact tag) and status
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'

# the Postgres container in full — pin datadir mount, exact image, env, network
docker inspect <pg_container>            # look at .Mounts, .Config.Image,
                                         # .Config.Env, .NetworkSettings.Networks,
                                         # .HostConfig.RestartPolicy, .Config.Cmd

# the volume(s) — exact Name / Mountpoint / Driver
docker volume ls
docker volume inspect <pg_volume_name>

# app containers, same treatment
docker inspect <app1_container> <app2_container>

# networks (compose recreates these; capture names/driver to match or external)
docker network ls
docker network inspect <net>

# exact postgres server version living in that volume (tag ≠ on-disk version)
docker exec <pg_container> postgres --version
docker exec <pg_container> psql -U <user> -c 'select version();'
```

Record, per container: **exact image tag**, **volume name(s) + mountpoint (or
bind path)**, **published ports**, **env (esp. the real DB creds — see note)**,
**restart policy**, **network membership**, **`container_name`**.

> **Postgres creds gotcha:** `POSTGRES_USER/PASSWORD/DB` are only read on *first*
> `initdb`. The prod volume is already initialised, so those envs are cosmetic
> for the DB — but the **apps' DSNs must use the credentials that were baked in
> at first init**. Capture the real user/password/db from the app env or the
> running container, not from a fresh guess. Getting this wrong = apps can't
> auth, but data is still safe.

## Phase 1 — Author the gitops compose to match prod byte-for-byte

Author, in the gitops repo, under
`clusters/beget-prod/projects/<fin-data-slug>/environments/prod/apps/<app>/`:

- `compose.yaml` that reproduces Phase-0 reality:
  - **Postgres**: `external: true` volume pinned to the discovered name (or the
    discovered bind path); `image:` = the **exact** current tag; matching
    `container_name`; same published ports; same network.
  - **The two apps**: exact current image tags (this is the baseline the future
    "bump the digit" flow diffs against); their real env via the sibling `.env`.
- `.env` sibling with the **real** runtime env / DB DSN captured in Phase 0
  (note: plaintext-in-git — the repo is already treated as a secret store per
  `renderer.RenderEnvFile`; confirm that's acceptable for these creds, otherwise
  route secrets the way the platform does elsewhere).

Cross-check the composed file against `docker inspect` output field by field.
The success criterion for this phase: **a `docker compose config`/dry diff shows
the compose would attach the existing volume and not redefine the datadir.**

## Phase 2 — Enroll the VM into Portainer edge (no prod impact)

Get an **Edge Agent** onto the VM so Portainer can manage stacks. Use the
existing manual-connect path (`doCreateManualAppServer` → `RunBootstrap`).
`bootstrap.sh.tmpl` is **idempotent and safe for a live prod VM**:

- Docker install is skipped if Docker is already present (explicit guard against
  the `docker.io` package conflict on pre-provisioned VMs).
- It only **adds** the `portainer_edge_agent` container (+ optional obs
  sidecars). It does **not** stop, remove, or touch the prod app/DB containers
  or their volumes.

Steps:
1. Confirm the VM's `app_servers` row (already exists from adopt). Decide whether
   to enroll via the console "connect manual VM" op or a targeted one-off; the
   agent, edge endpoint 3, and this row must line up so `GetComposeDeployTarget`
   resolves the right `EndpointID`.
2. Run bootstrap → Edge Agent connects → endpoint 3 goes online in Portainer.
3. **Do not deploy any stack yet.** Verify prod is still up and the agent is
   green.

> Optional: the obs sidecars (node_exporter/cadvisor/prometheus-agent/filebeat)
> can be delivered later via the Edge Stack plan in
> `tasks/vm-config-delivery-edge-stack.md`; keep bootstrap = Docker + Edge Agent
> only for this migration to minimise prod surface.

## Phase 3 — Rehearse the volume adoption (dev / throwaway)

On the k8s dev env or a throwaway VM, prove the mechanic before prod:

1. Create a named volume, init a Postgres in it, write a sentinel row.
2. Deploy the Phase-1-style compose (external volume pinned to that name) as a
   Portainer stack via the real `CreateStackFromGit` path.
3. Assert: the sentinel row survives, no `*_pgdata` new volume was created,
   `docker volume ls` shows only the original.
4. Then simulate a release: bump the app image tag in git, trigger redeploy,
   assert the app container is recreated and the DB volume is **untouched**.

Only proceed to prod once this is green.

## Phase 4 — Prod cutover (single maintenance window)

This is the only step with (brief) downtime, because handing already-running,
non-compose containers to compose management requires recreating them under the
compose project labels. Data is preserved because the volume is external.

1. **Announce a maintenance window** with the customer.
2. **Fresh backup, twice over:**
   - `docker exec <pg> pg_dump -U <user> -Fc <db> > fin-data-<date>.dump`
   - volume-level copy: `docker run --rm -v <pg_volume>:/v -v $PWD:/b alpine \
     tar czf /b/fin-data-vol-<date>.tgz -C /v .`
   - copy both **off the VM**.
3. **Container-name collision handling:** compose won't attach to an existing
   container that lacks its project labels; it will error "name already in use".
   Resolve by, in order, per service:
   - stop + `docker rm` the hand-run container (the **named/external volume
     persists** — this is just a restart), then let the stack create the
     replacement attached to the same external volume; **or**
   - if you prefer zero-touch on PG, keep the DB container name distinct in
     compose and point apps at it — but matching names + a clean recreate is the
     cleaner long-term adopt. Prefer the stop/rm/recreate for PG since it's a
     normal restart with data intact.
4. **Deploy the stack** via the console/`doDeployStack` (`CreateStackFromGit`).
   Watch: Portainer clones gitops, runs `up`, Postgres attaches the **existing**
   external volume, apps come up on their real images.
5. **Verify** (see Phase 5) before closing the window.

**If anything looks wrong** (empty DB, wrong volume): stop immediately, do
**not** run `down -v`, restore from the backup, and re-diagnose. The old
hand-run setup can be restarted from the untouched volume as the fallback.

## Phase 5 — Verify & lock in the future-release path

Verify:
- `docker volume ls` → only the original PG volume; **no** new `<stack>_pgdata`.
- Row counts / sentinel checks match pre-cutover; `pg_dump` schema matches.
- Both apps healthy, serving, connected to the DB.
- Portainer shows fin-data as a git stack on endpoint 3.

Lock in the release flow (the whole point):
- **Next release = edit the image tag in the app's `compose.yaml` in git →
  console deploy op → `doDeployStack` finds the existing stack → `RedeployStack`
  with `PullImage:true, Prune:false`.** Portainer pulls the new image, recreates
  the app container; PG (external volume) is untouched.
- Confirm `Prune:false` stays (`worker/deploy_stack.go`) — guarantees removed
  services never trigger a volume prune.
- Add a repo guard / runbook note: **`docker compose down -v` on fin-data is
  banned**; volume lifecycle is out-of-band and manual only.

## Rollback

- Pre-cutover: nothing to roll back — Phases 0–2 are read-only/additive.
- At cutover: restore is (a) restart the original hand-run containers against the
  untouched external volume, or (b) restore `pg_dump`/volume tarball into a fresh
  volume and repoint. Because we never prune and the volume is external, the data
  volume survives every failure mode except an explicit manual delete.

## Risk register

| Risk | Mitigation |
|------|------------|
| **New empty PG volume created on first `up`** (data loss) | `external: true` + pinned `name:` = the discovered volume; verified in Phase 3 rehearsal and Phase 5 (`docker volume ls`). |
| Volume is a **bind mount**, not named | Phase 0 `docker inspect .Mounts` reveals it; mirror the host path in compose instead of a named external volume. |
| **Wrong DB creds** in `.env` → apps can't auth | Capture real init-time creds in Phase 0; data stays safe regardless. |
| **container_name collision** blocks the stack | Planned stop/rm/recreate against the persistent external volume in the window. |
| Accidental `down -v` / prune later | `Prune:false` kept; explicit ban documented; volume external so `down` alone can't delete it. |
| Bootstrap disturbs prod | Bootstrap is idempotent, Docker-install-guarded, and only *adds* the edge agent; verified in Phase 2 before any stack deploy. |
| Secrets in plaintext git `.env` | Accept per existing platform convention, or route via the platform's secret path; call out explicitly before writing creds. |

## Build/execution order (checklist)

1. Phase 0 discovery on prod VM; paste the inventory here. **(read-only)**
2. Phase 1 author `compose.yaml` + `.env`; field-by-field diff vs inspect.
3. Phase 3 rehearse external-volume adoption on dev/throwaway; must be green.
4. Phase 2 enroll VM edge agent; verify prod untouched + endpoint online.
5. Phase 4 backup ×2 → cutover in window → deploy stack.
6. Phase 5 verify volume identity + data + apps; document the bump-the-digit
   release flow and the `down -v` ban.
```
