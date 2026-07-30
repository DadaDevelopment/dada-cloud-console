# ADR-016: Dada Box runtime — gVisor sandboxes on a dedicated fleet of Beget VMs

## Status

Accepted (architecture) — 2026-07-30. The substrate decision below is binding on
the control plane, which is why it is recorded now: `backend/internal/box`,
`backend/internal/models/box.go` and migration `061_boxes.sql` are already written
against it. The runtime itself (`box-agent`) is NOT built yet, and three of the
assumptions the decision rests on are still spikes (see *Assumptions still open*).
Firecracker is deferred, explicitly and reversibly.

## Context

A Dada Box is an ephemeral machine with root inside it, handed to a customer's own
coding agent in seconds, into which managed Postgres and S3 attach mid-flight, and
which — if the prototype survives — crystallizes into a permanent VM. The customer
runs arbitrary code as root in it. So the runtime question is not "which container
tool" but "on what substrate can we safely execute hostile code, in seconds, with a
kernel boundary".

Two facts from this codebase eliminate most of the option space before any
benchmark:

1. **Our Kubernetes is Beget's MANAGED Kubernetes.** `clusters/beget-prod` is the
   only cluster, and `backend/internal/beget/client.go` is a read-only client
   against `GET /v1/k8s/cluster`. On managed k8s we cannot register a RuntimeClass,
   rewrite containerd's config or load kernel modules. **gVisor and Kata are
   therefore unavailable on our cluster** — and, independently of that, hostile
   tenant code must not share nodes with Portainer, Argo CD, Postgres and the
   backend.
2. **We already provision real Beget VMs reliably, in production.**
   `portainer-agent/internal/worker/create_appserver.go` →
   `internal/terraform/templates/main.tf.tmpl` (`beget_compute_instance`, with
   cpu/ram_mb/disk_mb already template variables) → `internal/ssh/bootstrap.sh.tmpl`
   → Portainer enrollment → `doDeployStack`. This is the VM track, it works, and it
   is also the endpoint of crystallization.

So the substrate is neither "our k8s" nor "our own hardware", but a **fleet of
ordinary Beget VMs on which we are root and on which nothing runs except boxes**.

### The fork the brief posed, and why it dissolves

The product brief framed the choice as: (a) a container that is later converted
into a bootable VM image, or (b) a micro-VM from birth so that crystallization is
merely a policy change.

Both options rest on an assumption worth rejecting: **that crystallization must
produce a bootable VM image.** It does not.

A box is an OCI image + named volumes + env + attached resources. A crystallized
box is *the same OCI image and the same volumes*, running as a compose service on a
dedicated Beget VM under Portainer — precisely what `create_appserver.go`,
`doDeployStack` and `ImportComposeStackPayload` already do in production.

Crystallization becomes "container → container, plus a change of lifecycle policy
on the object". No rootfs-to-disk-image conversion, no synthesizing systemd units
from an entrypoint. That whole class of fragility is **removed, not mitigated**.

## Decision

**Run every box as a gVisor (`runsc`, platform `systrap`) sandbox on a dedicated
fleet of Beget VMs that we own root on and that run nothing but boxes.**

Concretely:

- **Isolation boundary: gVisor, never `runc`.** Every tenant process runs under
  `runsc`. `systrap` implements the Linux syscall surface in userspace, in Go, and
  needs neither `/dev/kvm` nor host kernel modules — which is exactly why it is
  available to a company with managed k8s and no hardware of its own while still
  giving a real kernel boundary.
- **Not on our cluster.** Box hosts are outside `beget-prod`, outside the
  `dada-vms` Portainer edge group, and carry no platform secret. That deviation
  from ADR-007 is large enough to own its own record: **ADR-017**.
- **Boxes are not created on demand.** They are pre-created, already running and
  parked in quarantine; `box up` is a *claim* (bind identity, mount the volume,
  write a signed principal, switch netns out of quarantine, unfreeze). Everything
  that costs minutes — `terraform apply`, a 10GB image pull, a cold sandbox start —
  is moved off the request path, because time-to-ready is the one claim marketing
  cannot compensate for.
- **The claim path does not go through the `operations` table.**
  `portainer-agent/internal/config/config.go` polls operations every `5s`
  (`VM_POLL_INTERVAL_DB`), and 5s alone exceeds the entire time-to-ready budget
  (`metrics.BoxReadyBudget = 10s`, p50 target 3s). Operations remain for
  minutes-scale work: provision a host, attach a database, crystallize.
- **Crystallization targets the existing VM track**, unchanged: `CreateAppServer` →
  bootstrap → `ImportComposeStack`/`DeployStack`, with the box's own `environments`
  row promoted in place from `runtime='box'` to `runtime='vm'` (D1, migration 061).
- **Firecracker is deferred, behind a seam, not designed out.** The sandbox
  interface (`box-agent/internal/sandbox`) is where a Firecracker backend lands
  later; nothing above it knows which one is in use. `backend/internal/box`'s
  `BoxRuntime` interface is already written at that level of abstraction.

### Why gVisor rather than Firecracker in v1

Firecracker requires `/dev/kvm`. Beget instances are themselves KVM guests and
almost certainly do not expose nested virtualization. Micro-VMs would therefore
mean dedicated bare metal: new capital, a new provisioning path, a new networking
story — and throwing away `internal/terraform` and `internal/beget`, both of which
work today.

The trade we accept: **the isolation boundary CHANGES at crystallization** (a
shared host under gVisor becomes a dedicated VM). That change is a
*strengthening*, and it is one-directional, which is what makes it safe.

## Consequences

### Positive

- The capital profile of the container option with the reliability of the micro-VM
  option, reusing a provisioning path that is already in production.
- A real kernel boundary on managed infrastructure, with no hardware purchase and
  no privileged access to the k8s cluster we do not have.
- Crystallization is a policy change on an object we already model, not an image
  conversion. The riskiest failure mode of the whole product ("crystallization lost
  my state") loses most of its surface area.
- Time-to-ready is architecturally achievable rather than aspirational: the warm
  pool, and the claim path's deliberate avoidance of `operations`, are what make it
  so.

### Negative / accepted costs

| Cost | Why we accept it, or how it is bounded |
|---|---|
| gVisor syscall tax: 10–40% on syscall-heavy work (`npm install`), ~0% on CPU-bound compiles | Pre-warmed package caches in the image are what actually decide install time, and they cut far more than gVisor adds. But this is spike S1's gate: **>2× on `npm install` and the substrate is out entirely** |
| `docker` inside a box is awkward under gVisor | rootless BuildKit + fuse-overlayfs + a `docker` CLI shim. `docker build`/`docker run` work; `--privileged` and kernel-module tricks do not. **Documented honestly before the landing page ships, not after** (spike S3) |
| The isolation boundary differs before and after crystallization | It only ever strengthens, and the change is one-way |
| A second fleet to operate, outside Portainer | Owned by ADR-017; the cost is real and is paid deliberately |
| No memory oversubscription → lower density than a container platform | A runaway agent build must not OOM its neighbour. Density is a price; a neighbour's lost work is not payable |

### Assumptions still open (this ADR is accepted with them named)

| # | Assumption | Cheapest disproof | Failure means |
|---|---|---|---|
| S1 | gVisor `systrap` performs acceptably inside a nested Beget VM | 1 day: `npm install`, `cargo build`, `go build` under `runsc` vs `runc` on the same box | >2× on `npm install` → the substrate is out, and with it this ADR |
| S2 | Beget VMs do not expose `/dev/kvm` | 30 minutes: `ls /dev/kvm; grep -c vmx /proc/cpuinfo` | If KVM *is* available, revisit Firecracker immediately — the seam exists for this |
| S4 | The seconds-scale budget is real | 2 days: a hardcoded script that pre-starts a sandbox, claims it, measures all seven phases **before** the control plane is written | p95 > 5s → rework the claim path, do not proceed |
| B4 | Beget's ToS and tolerance permit a fleet running third-party code | A conversation plus reading the ToS. Free, and it can sink the product | Beget says no → the hardware question returns on day one, not month six |

S1, S2 and S4 are gates on *shipping*, not on this decision: the decision is what
those spikes are shaped to test.

## Alternatives rejected

- **gVisor/Kata on `beget-prod`.** Impossible, not merely unwise: managed
  Kubernetes gives us no RuntimeClass, no containerd config and no kernel modules.
  And it would put hostile code on the nodes running Portainer, Argo and Postgres.
- **Plain `runc` containers on shared hosts.** A container is not an isolation
  boundary for hostile root. This is the one line the product cannot cross:
  isolation is cost of goods, not a feature — it does not get sold, and without it
  we cannot open.
- **Firecracker micro-VMs in v1.** Needs `/dev/kvm`, therefore dedicated hardware,
  therefore new capital and the loss of `internal/terraform` + `internal/beget`.
  Deferred behind the sandbox seam rather than argued about.
- **Convert a box's rootfs into a bootable VM image at crystallization.** The
  fragility this ADR deletes. Round 3 flagged rootfs→image conversion and
  entrypoint→systemd synthesis as the likeliest source of a silent state loss, and
  a single state loss severs the monetization ladder at step two.
- **Dedicated host per tenant.** Correct isolation, wrong unit economics at this
  stage. Revisit if a customer pays for it.

## Related

- **ADR-017** — box hosts outside the Portainer fleet (the deliberate ADR-007
  deviation, and the reason it cannot be avoided).
- **ADR-007** — Portainer Edge Agent as the remote Docker runtime layer. Holds
  unchanged for the app-server/VM track; box hosts are carved out of it.
- `docs/plans/2026-07-29-box-runtime-architecture.md` — the full runtime plan,
  including the ten mandatory isolation controls, the latency budget table and the
  abuse-control design.
- `docs/plans/2026-07-29-box-backend-slice.md` — D1 (a box owns one `environments`
  row) and the operation contract.
- `backend/internal/box/` — the control-plane seams (`BoxRuntime`, `WarmPool`,
  `AttachProvider`, `Crystallizer`) this decision is written against.
- `backend/internal/metrics/box.go` — `BoxReadyBudget`, the phase histogram and the
  budget-breach counter that make the central claim measurable.
