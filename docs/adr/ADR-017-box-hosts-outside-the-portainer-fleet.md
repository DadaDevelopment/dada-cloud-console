# ADR-017: Box hosts run outside the Portainer fleet — a deliberate deviation from ADR-007

## Status

Accepted — 2026-07-30. This is a carve-out from ADR-007, not a reversal of it:
ADR-007 continues to govern the app-server (VM/compose) track without change. Box
hosts are excluded from it, permanently, and the exclusion is a **gate on public
access**, not a preference.

## Context

ADR-007 made Portainer CE + the Portainer Edge Agent the remote Docker runtime
layer, and it was the right call: it removed a large amount of custom tunneling,
reconnect and agent-lifecycle code, and it is in production today. Every VM we
provision is enrolled the same way, by `portainer-agent/internal/ssh/bootstrap.sh.tmpl`.

ADR-016 introduces a second fleet: Beget VMs that run gVisor sandboxes in which
**customers execute arbitrary code as root**. The obvious move is to enroll those
hosts the same way as every other VM — one bootstrap script, one fleet, one
operational story.

Read what that bootstrap actually mounts into the agent
(`portainer-agent/internal/ssh/bootstrap.sh.tmpl`, lines 66–68):

```
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/docker/volumes:/var/lib/docker/volumes \
  -v /:/host -v portainer_agent_data:/data \
```

Three mounts, each individually decisive on a host running hostile code:

- **`/var/run/docker.sock`** is root on the host. The Docker daemon socket will
  start a container with `--privileged`, with the host PID namespace, with `/`
  bind-mounted. Access to it is not "container management"; it is uid 0 with no
  audit boundary.
- **`/:/host`** is the entire host filesystem, read-write, inside the agent
  container.
- **`EDGE_KEY`** (passed as an env var to that same container) is the credential
  that joins this host to the Portainer control channel — the channel through which
  we deploy stacks to **every other VM in the fleet**.

So an escape from a sandbox on a box host does not end at that host. It reaches
`docker.sock`, and through the edge key and the edge tunnel it reaches the control
channel of the whole fleet. The blast radius of one gVisor escape becomes *every
customer's VM*, and it does so through a component we installed for convenience.

gVisor is a good boundary. It is not a boundary we should be willing to bet the
entire fleet's control plane on, and we do not have to: the agent is on that host
for our operational comfort, not for anything the box needs.

## Decision

**Box hosts are not part of the Portainer fleet, and never will be.** Specifically,
a box host:

1. **Does not run the Portainer Edge Agent.** No `docker.sock` mount, no `/:/host`
   mount, no `EDGE_KEY` on the machine.
2. **Is not in the `dada-vms` edge group** and receives no edge stacks — including
   the `vm-observability` fluent-bit stack the app-server fleet gets. Box telemetry
   ships through the box's own agent instead, and ships **metadata only**.
3. **Carries no platform secret whatsoever.** No Beget token, no gitops token, no
   Portainer credential, no registry write credential. The only credentials on a
   box host are (a) its own mTLS identity for the broker channel and (b) the
   *tenant's own* resource credentials for boxes it is currently hosting.
4. **Gets a separate bootstrap**, `portainer-agent/internal/boxhost/bootstrap.sh.tmpl`,
   which installs `box-agent` as a systemd unit and nothing else. It deliberately
   does not reuse `internal/ssh/bootstrap.sh.tmpl` — see *Alternatives rejected*:
   sharing that template with a flag is how the mounts come back.
5. **Accepts no inbound connections** except SSH from our own /32s. There is no
   inbound port for the control plane: `box-agent` opens ONE outgoing, host-initiated,
   long-lived, multiplexed mTLS gRPC connection to `box-broker`, and everything
   rides inside it. Zero listening ports for tenants also means zero public IPs per
   box, which matters because Beget IPs are scarce and billed.
6. **Runs boxes and nothing else.** No other workload, ours or a customer's, is
   scheduled on a box host.

We accept building the host↔control-plane transport ourselves — the thing ADR-007
was written to avoid — because the specific reason ADR-007 gave for not building it
(the problem is solved by an existing agent) does not apply when that agent cannot
be installed. The shape is the same as Portainer's edge tunnel, proven in our own
estate; only the implementation is ours.

### What this deviation does NOT change

- ADR-007 stands for app servers. The VM/compose track keeps Portainer, keeps the
  existing bootstrap, keeps `dada-vms`.
- **Crystallization targets the Portainer fleet on purpose.** A crystallized box
  becomes an ordinary app server: a dedicated VM, enrolled in Portainer, running its
  compose stack through `doDeployStack`. That is safe precisely because a
  crystallized box is *no longer running unknown code on shared infrastructure* —
  it is one customer's own dedicated VM, exactly like every other app server. The
  isolation boundary strengthens at promotion, and the fleet it joins is chosen to
  match.

## Consequences

### Positive

- One gVisor escape is contained to one host and its own boxes. It cannot reach
  `docker.sock`, the edge key, or the fleet control channel, because none of them
  are present.
- Zero inbound ports and zero public IPs per box: the attack surface a tenant can
  reach is the broker, which is also the single place abuse control and the kill
  switch live.
- Compromise of a box host leaks no platform credential, so recovery is
  "reprovision the host", not "rotate the fleet".
- Abuse complaints arrive at *our* broker before they arrive at Beget, which is what
  keeps the ToS conversation (risk B4 in ADR-016) survivable.

### Negative / accepted costs

| Cost | Why we accept it |
|---|---|
| A second transport (`box-agent` ↔ `box-broker` mTLS gRPC) that we write and operate | The alternative is betting the fleet's control channel on a userspace kernel. The transport is bounded work with a known shape |
| Two bootstrap paths to maintain | Duplication is the point: a shared template with a flag is precisely how `docker.sock` gets back onto a box host in six months, in a diff nobody reads as security-relevant |
| Portainer's UI is not an escape hatch for debugging a box host | SSH from our /32s plus `box-agent`'s own diagnostics. Deliberate: a UI that can exec into a tenant sandbox is a UI that can read tenant work |
| Box-host telemetry needs its own shipping path | It ships **metadata only** — bytes, directions, denied syscalls, never content. The existing fluent-bit edge stack tails container logs, which on a box host would mean tenant output, so reusing it would have been a privacy regression as well as a security one |
| Two fleets to size, patch and monitor | Bounded, and it is the cost of goods for isolation |

### This is a public-access gate, not a preference

Control #2 of the ten mandatory isolation controls in
`docs/plans/2026-07-29-box-runtime-architecture.md` is exactly this decision. All
ten are required before any public box access; **the gate is the list, not a date.**
If a box host is ever found enrolled in Portainer, or found with `docker.sock`
mounted into any container, that is a stop-ship, not a cleanup ticket.

## Alternatives rejected

- **Enroll box hosts in Portainer like every other VM.** Rejected: `docker.sock` +
  `/:/host` + `EDGE_KEY` on a machine running hostile root code makes one sandbox
  escape a fleet-wide compromise. This is the whole reason the ADR exists.
- **Enroll them, but with a hardened agent (no `docker.sock`, no `/:/host`).** An
  Edge Agent without the Docker socket cannot manage Docker, which is the only
  reason to run it. What remains is the edge key and the tunnel — i.e. the fleet
  control channel with none of the benefit.
- **Reuse `internal/ssh/bootstrap.sh.tmpl` behind a `BOX_HOST=1` flag.** Rejected on
  drift grounds. The mounts live in one `docker run` invocation that someone will
  edit for a good reason on the app-server track, and a flag is not a boundary. A
  separate file makes "does this host mount `docker.sock`?" answerable by reading
  one file end to end.
- **A separate Portainer instance for box hosts.** Shrinks the blast radius to the
  box fleet but does not remove `docker.sock` or `/:/host` from a host running
  hostile code, so cross-tenant escape between boxes stays reachable — and it adds
  a second Portainer to operate for no isolation gain.
- **Put boxes on `beget-prod` with gVisor.** Not available (managed Kubernetes: no
  RuntimeClass, no containerd config, no kernel modules) and, independently, it
  would place tenant code on the nodes running Portainer, Argo CD, Postgres and this
  backend. See ADR-016.

## Related

- **ADR-007** — Portainer Edge Agent as the remote Docker runtime layer. Unchanged
  for app servers; this ADR carves box hosts out of it.
- **ADR-016** — the box runtime decision (gVisor on a dedicated Beget VM fleet) that
  makes this carve-out necessary.
- `portainer-agent/internal/ssh/bootstrap.sh.tmpl` — the mounts quoted above (lines
  66–68); the concrete artifact this ADR is a reaction to.
- `docs/plans/2026-07-29-box-runtime-architecture.md` — the ten isolation controls,
  the broker/tunnel design and the abuse-control point.
