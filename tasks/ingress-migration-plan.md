# Plan: migrate ingress-nginx main → console-migration (App + external chart + working values), zero-downtime LB/DNS cutover

## Goal
Move the cluster ingress controller from the `main` app-helm wrapper (broken/half-manual values)
to `console-migration` as a proper `platform.dada-tuda.ru/v1alpha1 App` referencing the **external**
ingress-nginx chart 4.12.1 + a **working** values.yaml that includes:
- `tcp: {"8000": "portainer/portainer:8000"}`  ← restores Portainer edge tunnel → unblocks cloud-console
- Beget external-LB annotations (so it gets a public IP)
- correct ingressClass so it serves all existing Ingresses

Because the Beget LB IP **cannot** be preserved, do a **parallel-run + DNS cutover** so there is no downtime.

## Facts (measured)
- ~24 public domains on current LB IP `159.194.204.57`; **no wildcard** DNS; IP not pinned.
- 11 domains under Crossplane **PublicApi** (DNS = A→hardcoded IP); ~13 domains have **manual** Beget DNS.
- Old ingress app = `main` app-helm appset, release `ingress-nginx-network`, ns `network`, chart 4.12.1 wrapper.
- Beget DNS API creds: ConfigMap `beget-dns-api-config` (ns k8s-components). PublicApi composition = `publicapi-beget-dns`.

## FINAL STATUS — ALL DONE
- Ingress migrated to ingress-nginx-pub (console-migration), HA 2 replicas, old decommissioned, DNS centralized.
- ALL 3 cloud-console features verified GREEN via its API:
  1. Live state ✅ (containers populate)  2. Compose deploy ✅ (e2e-nginx on test-vm-01)
  3. Manual VM connect ✅ (manual-vm-01 217.114.13.57 → Ready, edge agent connected via portainer.dada-tuda.ru:8000)
- Bugs found & fixed: retry status (eccff49c, deployed); edge endpoints used in-cluster URL + non-idempotent
  bootstrap (c39d3b70, deployed). Manually built/pushed c39d3b70 (Jenkins lagging).
- RESIDUAL (noted, not blocking): DeleteAppServer does NOT delete the Portainer endpoint (orphan reused with
  stale edge key — worked around manually); monitoring sidecars (filebeat/prometheus-agent) crash-loop on the
  loaded VM (best-effort, edge agent unaffected); 2 nodes keep auto-cordoning (cluster-health, separate).

## STATUS (updated)
- Phase 0 ✓  Phase 1 ✓  Phase 2 ✓ (NEW_IP=155.212.223.198)  Phase 3 ✓ (all 27 + apex on new IP)
- Phase 4 (decommission old) = HELD as rollback fallback (soak). Phase 5 features: 2/3 done.
- VERIFIED via cloud-console: Live state ✓ (containers populate), Compose deploy ✓ (e2e-nginx running on VM).
- Tunnel opens through new ingress-nginx-pub :8000. Heartbeat healthy.
- New controller HA: 2 replicas + PDB + topology spread (after a single-replica eviction outage when
  a node was cordoned — cluster periodically cordons 2/3 nodes; uncordoned to recover).
- ALL ~27 *.dada-tuda.ru + apex cut to new IP. Apex done via direct Beget changeRecords preserving MX/TXT/SPF.
  External serving sweep: healthy; 404/000 cases have exact old=new parity (not regressions).
- Centralized DNS: publicApi.lbTarget single value drives all 27 PublicApi records.
- Bonus fix: retry endpoint set dead 'Queued' -> now 'Created' (backend eccff49, building via Jenkins).
- Manual VM connect: heartbeat+tunnel proven; needs a (billable) VM to demo end-to-end.
- DONE: old ingress decommissioned (deployment+svc pruned; stuck LB finalizer cleared — Beget LB already gone;
  IngressClass nginx recreated+owned by ingress-nginx-pub). Retry fix deployed (backend eccff49c).
- Manual VM connect (217.114.13.57): found + fixed TWO real bugs in portainer-agent (commit c39d3b70):
  (1) edge endpoints advertised the in-cluster Portainer URL (svc.cluster.local) — unreachable by external
      VMs; added PORTAINER_EDGE_URL=https://portainer.dada-tuda.ru.
  (2) SSH bootstrap `apt-get install docker.io` aborted (set -e) on VMs with existing docker-ce; made docker
      install conditional + observability stack best-effort + clean edge-agent redeploy.
  Deploying c39d3b70 + retrying once Jenkins build + Argo rollout complete.
- OPEN: investigate WHY 2 nodes keep getting cordoned (cluster-health, separate issue — flagged to user).

## Phase 0 — Pre-flight verification (read-only, no risk)
- [x] Snapshot: all Ingress hosts, all PublicApi CRs, current DNS A-records (dig each of 24), old Service status IP.
- [ ] Confirm external-chart app.yaml schema (template: neo4j/eck-operator — repoURL + chart + targetRevision + releaseName + skipDefaultParameters).
- [ ] Confirm chart values keys: controller.service.annotations (beget), controller.service.type=LoadBalancer,
      controller.ingressClassResource (name nginx, controllerValue k8s.io/ingress-nginx), controller.electionID (UNIQUE),
      controller.fullnameOverride (UNIQUE, avoid clashing with old `ingress-nginx-network-*`), tcp block.

## Phase 1 — Build new App in console-migration (commit only, no apply)
- [ ] Create `clusters/beget-prod/projects/<project>/environments/<env>/apps/ingress-nginx/app.yaml`
      (external chart, releaseName e.g. `ingress-nginx-pub`, skipDefaultParameters: true).
- [ ] Create sibling `values.yaml` with WORKING flat upstream values:
      - controller.fullnameOverride: ingress-nginx-pub (distinct from old)
      - controller.ingressClassResource.name: nginx, controllerValue: k8s.io/ingress-nginx, ingressClass: nginx
      - controller.electionID: ingress-nginx-pub-leader (UNIQUE → no fight with old)
      - controller.service.type: LoadBalancer + Beget annotations (external) + labels lbType:external
      - controller.watchIngressWithoutClass: false
      - tcp: {"8000":"portainer/portainer:8000"}
- [ ] Commit to console-migration. DO NOT delete old app yet.

## Phase 2 — Bring up new controller in PARALLEL (NEW IP), verify before DNS
RISK: two controllers (same controllerValue/class) both serve all Ingresses (good for 0-downtime) but both
write Ingress .status (cosmetic flap). Unique electionID avoids leader conflict. Accept during cutover.
- [ ] Argo sync new app → new Beget LB → capture NEW_IP.
- [ ] BEFORE DNS: verify NEW_IP serves every domain:
      `curl -k --resolve <host>:443:<NEW_IP> https://<host>/` for all 24 hosts.
- [ ] Verify Portainer tunnel on new path: chisel :8000 on NEW_IP; endpoint 10 tunnel opens.

## Phase 3 — Centralize DNS target + cutover ("one value")
- [ ] Add single `publicApiLbTarget` value; point all 11 PublicApi dns.target to it; set = NEW_IP.
- [ ] Bring the ~13 manual domains under PublicApi (per-domain CR: upstream=existing svc, fqdn=host, target=publicApiLbTarget).
- [ ] Apply → all 24 A-records → NEW_IP (Crossplane reconciles via Beget API).
- [ ] Wait TTL; verify `dig <host>` = NEW_IP and HTTPS works for all 24.

## Phase 4 — Decommission old ingress (release old IP)
- [ ] Remove ingress-nginx from main app-helm (namespaces/network/values.yaml) → old Service + old LB + old IP released.
- [ ] Confirm nothing resolves to old IP; all green on NEW_IP.

## Phase 5 — Verify cloud-console 3 features (original goal) via cloud-console API
- [ ] Live state: container list populated (tunnel works).
- [ ] Compose deploy via cloud-console CreateApp → stack on VM.
- [ ] Manual VM connect via cloud-console → Ready.

## Rollback
DNS keeps old IP until Phase 3; old LB stays up until Phase 4. Any failed verify → stop; old path still serves.
Revert console-migration commit; new LB is additive only.

## Open decisions (confirm before executing)
1. Which project/env path in console-migration should host the ingress App? (infra/network-ish)
2. OK to bring all 13 manual domains under PublicApi (13 new CRs)? Or keep manual + batch DNS update?
3. Acceptable cutover window for cosmetic status-flap from dual controllers?
