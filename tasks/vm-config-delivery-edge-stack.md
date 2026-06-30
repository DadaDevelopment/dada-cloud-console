# VM config delivery via Portainer Edge Stack (Phase 2)

Deliver observability config (filebeat/prometheus-agent + node_exporter/cadvisor)
to ALL VMs — existing + new — on every config change, without re-SSH. Edge-case
config churn must converge the whole fleet from a single git edit.

## Decision (user, 2026-06-30)
- Phase 2: **Portainer Edge Stack + Edge Group**, native fleet fan-out.
- Config lives in **argo-infra** (alongside VM apps).

## Verified environment (live)
- Portainer **2.39.2** CE, internal `portainer.portainer.svc:9000`, edge tunnel
  host `portainer.dada-tuda.ru` (:8000).
- `EnableEdgeComputeFeatures: false`, `EdgePortainerUrl: ""` → `/api/edge_stacks`
  + `/api/edge_groups` return **503**. PREREQ: enable edge compute + set
  EdgePortainerUrl, then the APIs unblock.
- Edge endpoints (type 4): just **(3, "findata")** — one live VM. Small fleet ⇒
  migration is one VM.
- portainer-agent already deploys user apps as git stacks
  (`CreateStackFromGit`/`RedeployStack`); the observability sidecars are the only
  thing still one-shot `docker run` in `internal/ssh/bootstrap.sh.tmpl`
  (node_exporter, cadvisor, prometheus-agent, filebeat).

## Target architecture
1. **Edge Group `dada-vms`** (dynamic by tag, e.g. tag `dada-managed`) — every VM
   endpoint joins on provision. Portainer fans the stack to all members.
2. **Edge Stack `dada-observability`** from the argo-infra git compose →
   `clusters/beget-prod/.../apps/observability/compose.yaml` + configs
   (filebeat.yml, prometheus-agent.yml, node_exporter/cadvisor). Edge agents poll
   Portainer (`EdgeAgentCheckinInterval=5s`) and pull on change.
3. **Per-VM identity WITHOUT per-stack env**: a single Edge Stack is shared by all
   agents, so per-VM values cannot be per-stack. Resolve identity from the agent
   itself — filebeat `add_host_metadata` / `${HOSTNAME}` for `vm_name` and the
   index `dada-app-logs-${HOSTNAME}-%{+yyyy.MM.dd}`. Shared secrets (ES key, prom
   user/pass — same for every VM today) ride as Edge Stack env vars.
4. **Config change = edit git → bump the Edge Stack** (redeploy/pull). Portainer
   pushes to every agent in `dada-vms`. Existing VMs converge automatically. This
   is the whole point.

## portainer-agent changes
- New client methods (`internal/portainer/client.go` + models):
  - Edge groups: `ListEdgeGroups`, `CreateEdgeGroup`, `EnsureEndpointInEdgeGroup`
    (or tag the endpoint so a dynamic group auto-includes it).
  - Edge stacks: `ListEdgeStacks`, `CreateEdgeStackFromGit`, `UpdateEdgeStackGit`
    (`/api/edge_stacks`, `/api/edge_stacks/{id}/git`).
  - Settings: `EnsureEdgeCompute` (PUT `/api/settings`
    EnableEdgeComputeFeatures=true, EdgePortainerUrl).
- Provision flow (`worker/create_appserver.go`): after the edge endpoint is up,
  add its tag → joins `dada-vms`. Ensure `dada-observability` edge stack exists
  (idempotent). DROP the filebeat/prom/node_exporter/cadvisor `docker run`s from
  `bootstrap.sh.tmpl` — bootstrap = Docker + Edge Agent only.
- Reconcile: a small worker ensures the edge group + edge stack exist + are at the
  desired git rev on startup and on a period; Portainer handles per-agent delivery.

## Migration of existing VMs (findata)
Tag endpoint 3 into `dada-vms`. Portainer deploys `dada-observability` to it →
the stack's containers replace the SSH-baked ones (same names; compose
`container_name` + `docker rm -f` semantics). One-time, no manual SSH. Old
`dada-vm-logs-*` already covered by the multi-index read (Phase 0).

## Build order
1. PREREQ: enable edge compute + EdgePortainerUrl on prod Portainer (API). Verify
   `/api/edge_groups` + `/api/edge_stacks` → 200.
2. Go client methods (edge groups, edge stacks, settings) + build/vet/tests.
3. argo-infra `apps/observability/` compose + configs (host-identity filebeat).
4. portainer-agent: ensure-group + ensure-stack + bootstrap trim + reconcile.
5. Migrate findata; verify metrics + logs still flow, now stack-managed; prove a
   config edit propagates without SSH.

## Risks / flags
- Enabling edge compute is a manual Portainer settings change (not gitops; stored
  in Portainer DB) — flagged as a prod step.
- Edge Stack secrets (ES/prom creds) become Edge Stack env, visible in Portainer —
  acceptable (already plaintext in the agent config), but note it.
- Must not break findata's live metrics/logs during cutover — migrate, verify,
  keep multi-index read.
