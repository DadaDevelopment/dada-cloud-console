# VM fleet config: git-driven, no per-VM manual ops (fluent-bit is payload #1)

Deliver VM node config (observability sidecars) from **git to every VM at once**
via a Portainer **Edge Stack** on the dynamic **Edge Group `dada-vms`**. Editing the
edge stack's compose in git → redeploy → Portainer fans it to **all VMs in the
group** (existing + future), no SSH, no per-VM ops. 10 VMs enrolled at different
times converge to one git-defined config. First payload: swap the broken
filebeat for **fluent-bit → OpenSearch** (the log fix, decision A).

## Why this is the same problem as the log fix

Sidecars (node_exporter, cadvisor, prometheus-agent, filebeat) are installed by
the **one-shot bootstrap** (`portainer-agent/internal/ssh/bootstrap.sh.tmpl`,
`docker run` per sidecar). That runs ONCE at enroll → a VM's config is frozen at
its enroll-time bootstrap version. Fixing the fleet (e.g. filebeat→fluent-bit)
today means re-SSHing every VM. The Edge Stack makes node config a git artifact
that Portainer reconciles onto the whole group.

## Current state (verified live)

- **Edge group `dada-vms`** exists (Id 1, dynamic, tag `dada-managed` Id 1). ✓
- **Client methods all built** (`portainer/client.go`): `EnsureEdgeGroup`,
  `EnsureEdgeStackFromGit`, `CreateEdgeStackFromGit`, `RedeployEdgeStackFromGit`,
  `ListEdgeStacks`, `TagEndpoint`. **But NO worker/backend caller uses them** —
  the machinery is prepared and unwired.
- **findata (endpoint 3) has `TagIds=[]`** → not in the group. No VM is tagged.
- **0 edge stacks** deployed → no fleet config delivery yet.
- Log write-path is broken fleet-wide: filebeat 8.x ⊥ OpenSearch (`_license`
  check), so VM container logs reach no store (see the log diagnosis). fluent-bit
  speaks OpenSearch natively → the fix, delivered as the edge stack's shipper.

## Target architecture

```
git (argo-infra): clusters/beget-prod/fleet/vm-observability/docker-compose.yml
        │  (node_exporter, cadvisor, prometheus-agent, fluent-bit→OpenSearch)
        ▼
Portainer Edge Stack  ──(Edge Group dada-vms, dynamic by tag dada-managed)──►  every tagged VM
        ▲                                                                         │
   edit compose + redeploy  ────────────────────────────────────────────────────┘
   = one git change reconciles the whole fleet
```

- **Enroll** tags the new endpoint `dada-managed` → it auto-joins `dada-vms` →
  Portainer pushes the current edge stack to it. New VMs are config-current by
  construction.
- **Fleet update** = edit the edge stack compose in git → a `RedeployEdgeStack`
  op → Portainer reconciles all group members.
- **Bootstrap shrinks** to just the edge agent (+ Docker guard); it stops
  installing sidecars (the edge stack owns them). Idempotent on existing VMs.

## Phases

### Phase 1 — TAG + author edge stack ✅ DONE (findata; fluent-bit log fix LIVE)
- Ensure every enrolled VM is tagged `dada-managed` (in `doCreateAppServer` after
  enroll: `TagEndpoint(endpointID, tagID)`). Backfill existing endpoints (findata)
  now.
- Author `fleet/vm-observability/docker-compose.yml` in argo-infra: node_exporter,
  cadvisor, prometheus-agent (remote_write to the public Prometheus), **fluent-bit**
  reading `/var/lib/docker/containers/*/*.log` + Docker metadata, output =
  OpenSearch (`https://kibana.dada-tuda.ru/es`, index `dada-vm-logs-<name>-*`).
  fluent-bit's OpenSearch plugin is ES-license-free → works where filebeat can't.
  - Per-VM identity: prometheus `external_labels.vm_name` and the log index need
    the server name. Edge stacks are one compose for the whole group → use a
    portainer/edge templating var or a fluent-bit `record_modifier` keyed off the
    endpoint name the agent injects. (Pin the mechanism in Phase 1.)

### Phase 2 — WIRE the edge stack op (agent)
- A worker op `EnsureFleetStack` (or fold into enroll): `EnsureEdgeStackFromGit`
  for group `dada-vms` from the git compose path. Idempotent — create if absent,
  else no-op.
- A `RedeployFleetStack` op → `RedeployEdgeStackFromGit(stackID)` → fleet update.
  Trigger from the console (a "push config to fleet" action) or on git change.

### Phase 3 — MIGRATE sidecars out of bootstrap
- Strip the sidecar `docker run` blocks from `bootstrap.sh.tmpl`; bootstrap keeps
  only Docker-guard + edge agent. Existing VMs: the edge stack deploys the
  sidecars (replacing the bootstrap-installed ones by same container names) — a
  one-time reconcile per VM, driven by the group, not by hand.
- Purge the old filebeat on each VM as the edge stack's fluent-bit takes over
  (edge stack can `docker rm -f filebeat` via an init step, or name-collision
  handling).

### Phase 4 — VERIFY ✅ proven on findata (sentinel shipped via edge-managed fluent-bit → dada-vm-logs-findata-* in OpenSearch); fleet redeploy = delete+recreate edge stack (Portainer /git redeploy 404 in v2.39.2)
- findata: fluent-bit ships → `dada-vm-logs-findata-*` appears in OpenSearch →
  console Logs tab populates (closes the log gap).
- Prove a fleet update: bump the edge stack compose → redeploy → the change lands
  on findata (and any 2nd VM) with no SSH.
- Document: node config = the edge stack in git; release/fix = edit + redeploy;
  new VMs auto-join by tag. Reproducible for VM #2…#10.

## Open decisions (pin before coding)

1. **Per-VM identity in a group-wide edge stack**: how fluent-bit/prometheus get
   the server name (Portainer edge var vs agent-injected env vs hostname). This is
   the one non-trivial bit — a single compose serves all VMs.
2. **Trigger for fleet redeploy**: console button, or auto on argo/git change.
3. **fluent-bit vs vector**: fluent-bit is the platform's k8s shipper already
   (reuse); confirm it's the fleet shipper too.
4. **Bootstrap migration safety**: how the edge stack replaces bootstrap-installed
   sidecars without a gap (same names + restart policy).
