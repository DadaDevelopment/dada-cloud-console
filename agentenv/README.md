# agentenv

**A body for your coding agent — and one specification for what happens to it.**

`agentenv` is a small tool and a smaller specification for one thing that nothing else models:
**a remote environment whose lifecycle stage is a property of the object, not a different kind of
object.**

Today a sandbox and production are different products, in different formats, often from
different vendors, and moving between them is a rewrite. `agentenv` describes an environment
that starts as a three-second disposable thought and can become production **without ever being
recreated** — and, critically, that *declares what it fails to preserve* when it does.

> ### Status: pre-v0.1, spec draft only
>
> This repository currently contains the wire contract, the JSON Schemas and editor examples.
> **The CLI is not here yet.** It is being extracted from a working implementation rather than
> written speculatively — a reference client that cannot be run is a museum piece, and shipping
> one would contradict the discipline this spec is about.
>
> Nothing here is normative yet. `spec/DRAFT-wire-contract.md` describes intent; the normative
> `spec/0.1/` appears only once the client has users who came back.

## This is not a new protocol

It is deliberately **a profile over [MCP](https://modelcontextprotocol.io)**, and the word
"protocol" is not used anywhere in this repository on purpose.

Layer by layer, almost everything is already claimed and reimplementing it would be pure cost:

| Layer | Already owned by |
|---|---|
| Framing, transport, session, cancellation, progress | MCP (JSON-RPC, stdio, Streamable HTTP) |
| Auth | OAuth 2.1, RFC 9728 |
| Tool discovery, input schemas, destructive-operation hints | MCP `tools/list`, `ToolAnnotations` |
| Environment *definition* — image, features, ports | OCI, `devcontainer.json` |
| Command execution inside the environment | SSH, and the agent's own shell tool |

Two slots are empty, and they are the whole content of this profile:

1. **A long-lived remote object whose lifecycle stage is a readable field**, not a separate
   resource type with separate endpoints.
2. **A declared preservation contract across a stage transition** — what survives, what is
   recreated, and what is lost.

MCP standardises verbs and their shapes. It has no notion of a durable object, no notion of an
operation that promises identity invariance across a transition, and no notion of a profile at
all — there is no profile registry and no conformance concept. That slot is empty and cheap to
occupy honestly.

## The admission test

Every candidate operation is measured against one rule:

> **If a reflector pointed at an OpenAPI document could have generated it, it does not belong in
> this spec.**

That rule exists because the failure mode is real and observed: a generator that walks an
OpenAPI document and emits one tool per operation, with `readOnly: method == GET` as its entire
semantic contribution, produces a hundred-plus tools and zero meaning. Wrapping a cloud API in
MCP is not a profile.

Four properties pass the test, and no CRUD wrapper can express any of them:

- **Identity invariance.** The same `env_id` is valid before and after promotion. No recreation,
  no new address, no re-attachment.
- **Stage as a field.** One operation advances it; the stage is read, not inferred.
- **Attachment survival with a declared injection contract.** `env_attach` returns not "a
  database was created" but the exact set of environment variables materialised into the running
  environment, and whether that set is byte-identical after promotion.
- **Declared loss.** The implementation must enumerate what it could not preserve. No vendor
  volunteers this, which is precisely why it is worth standardising — and why a conformance
  suite has teeth.

## Compose with `devcontainer.json`, don't replace it

If a `devcontainer.json` is present, image, features and ports are read from it. This profile
adds only the four things it lacks: **stage, TTL, attachments, promotion target.** Competing with
an established environment-definition format would be a fight with no prize.

## Operations

Seven MUST, four MAY. Full semantics in [`spec/DRAFT-wire-contract.md`](spec/DRAFT-wire-contract.md).

| Operation | What it does |
|---|---|
| `env_create` | allocate a body; returns the object with `stage`, a connection descriptor and its `guarantees` |
| `env_get` | read state, stage, attachments, addresses, consumed budget, `capabilities`, `guarantees` |
| `env_attach` | attach a managed resource to a **running** environment; returns the injected variables, `requires_restart`, `survives_promotion` |
| `env_expose` | publish a port; returns the URL and whether the address is `ephemeral` or `stable` |
| `env_promote` | advance the stage; returns **the same `env_id`** and a `preservation_report` |
| `env_destroy` | terminate and release |
| `env_receipt` | an immutable record of who did what, at what cost, with an explicit `redaction` statement |
| *MAY* | `env_quote`, `env_sleep`, `env_wake`, `env_detach` |

`env_get`'s `capabilities` field is a **declaration, never an enforcement claim.** A profile
cannot enforce anything inside someone else's guest, and pretending otherwise would be the worst
kind of security theatre. It tells the agent what is permitted *before* it acts, instead of
letting it discover a wall by hitting it — which is how agents waste twenty minutes and reach a
wrong conclusion about the state of the world.

`env_promote`'s `preservation_report` is the entire intellectual content of the profile. For each
of filesystem, volumes, environment variables, attachments, address and running processes it
returns exactly one of `preserved | recreated | lost`. **An implementation that reports `lost` for
something it advertised in `guarantees` fails conformance.**

`env_receipt`'s `redaction` field is not politeness. Any implementation that records what an
agent did is building the most attractive secret store in its own perimeter, so the schema forces
it to state what it withheld.

## Explicitly out of scope

Written down as a numbered section in the spec, because a published and argued non-goals list is
one of the strongest credibility signals a small specification can emit — and it costs a day.

Command execution, file transfer, pty, terminal multiplexing · image building and environment
definition · transport, framing, auth · **rollback, checkpoints and reversibility** · **data
branching and production forks** · **capability enforcement inside the guest** · inferring a
build spec from a recorded session · billing, quotas, abuse policy, isolation technology · model
or token access · multi-environment scheduling.

Several of those are absent because they were designed and then killed. Reversibility of a whole
stack is not implementable — moving state back is not the same as moving consequences back, and a
promise that leaks once is worse than no promise. Inferring a build spec from a session dies on a
single `curl … | bash`: one line in the recording, hundreds of changes in the system.

## Licence and governance

Apache-2.0, including the spec text. The express patent grant is the one line a hosting
provider's lawyer actually reads before letting an engineer implement, and it costs nothing.
Trademarks are not granted — see `TRADEMARKS.md`.

`GOVERNANCE.md` says the unflattering true thing: v0.x is one company and one maintainer. There
is no working group, no TSC and no RFC process, and inventing them at zero implementations reads
as theatre to exactly the audience that matters. One forward commitment: **at three independent
implementations, the spec moves to a neutral home.**

## Honest expectations

A specification is not distribution. MCP became a standard because its author owned a client and
could make the spec valuable to everyone in a day; this project owns no client and has no
installed base. So the plan does not treat publication as a channel: the tool is the
distribution, the spec is a differentiator and a commitment device, and the conformance suite —
when it exists — is published as a **scoreboard**, with our own implementation's failures on it.

Kill criteria are written down and dated in `GOVERNANCE.md`, including the one that matters: if
at six months the only implementations are ours, the repository is downgraded and stops being
described as a standard.
