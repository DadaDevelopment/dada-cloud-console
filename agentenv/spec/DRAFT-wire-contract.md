# Agent Environment Profile (AENV) — draft wire contract

**Version:** pre-0.1 · **Status: NON-NORMATIVE.** This document describes intent. It becomes
`spec/0.1/` only once a client exists and has users who came back.

An MCP profile: a domain vocabulary and a lifecycle, carried over MCP's existing transport, auth
and tool-discovery. No new framing, no new session model, no new auth.

---

## 1. The object

An **environment** is a durable, addressable remote object with a readable lifecycle **stage**.

```jsonc
{
  "env_id": "env_7f3a9c",          // stable for the object's whole life, across every stage
  "stage": "ephemeral",            // ephemeral | persistent
  "state": "ready",                // requested | booting | ready | idle | sleeping | promoting | failed | destroyed
  "connection": {
    "kind": "ssh",
    "endpoint": "boxes.example.com:22",
    "principal": "env-7f3a9c",
    "host_key_fingerprint": "SHA256:…"
  },
  "addresses":   [ { "url": "https://env-7f3a9c.example.com", "stability": "ephemeral" } ],
  "attachments": [ { "id": "att_1", "kind": "postgres", "alias": "main" } ],
  "ttl_expires_at": "2026-07-30T20:00:00Z",
  "budget":       { "spent": 12.4, "cap": 300, "currency": "RUB" },
  "capabilities": { "root": true, "docker_build": true, "egress": "metered" },
  "guarantees":   { "filesystem": "preserved", "volumes": "preserved",
                    "env": "preserved", "attachments": "preserved",
                    "address": "recreated", "processes": "recreated" }
}
```

**Normative intent, and the reason the profile exists:**

> `env_id` MUST remain valid and MUST refer to the same environment for the object's entire life,
> across every stage transition. An implementation that mints a new identifier on promotion does
> not implement this profile.

`capabilities` is what is **permitted here**. `guarantees` is what **survives promotion**. Both
are declarations the agent reads *before* acting.

### 1.1 On enforcement

`capabilities` is **not** an enforcement claim. A profile cannot enforce anything inside a guest
it does not control, and any wording suggesting otherwise would be security theatre. Enforcement
is the implementation's own problem, by its own means, and is out of scope (§6).

The value of declaring is behavioural: an agent that reads a contract stops before a wall it
would otherwise spend twenty minutes trying to climb — and, worse, would draw a wrong conclusion
about the state of the world from failing to climb.

---

## 2. MUST operations

Tools are prefixed `env_`. A server that must namespace further (a multi-product server) MAY use
`<vendor>_env_*` and MUST declare the prefix in its capabilities.

### `env_create`

**In:** `image` or `devcontainer` reference · `size` · `ttl_seconds` · `labels` ·
`idempotency_key`
**Out:** the environment object, `stage` defaulting to `ephemeral`.

Idempotent under `idempotency_key`. If `devcontainer` is given, image, features and ports come
from it; this profile adds only stage, TTL, attachments and promotion target.

### `env_get`

**In:** `env_id` · **Out:** the full object, including `capabilities` and `guarantees`.

### `env_attach`

**In:** `env_id` · `kind` (`postgres` | `s3` | `redis` | …) · `class` · `alias`
**Out:**

```jsonc
{
  "id": "att_1",
  "kind": "postgres",
  "injected": ["DATABASE_URL"],     // the EXACT variable names materialised into the environment
  "requires_restart": false,
  "survives_promotion": true
}
```

**The highest-value operation here and the least claimed elsewhere.** Every cloud API can create
a database. None models *the credential surface injected into a live environment, guaranteed
unchanged after that environment changes stage.*

Idempotent by `alias`. MUST attach to a **running** environment — an operation that requires a
restart to take effect is not a mid-flight attach, and MUST report `requires_restart: true`
rather than silently restarting.

### `env_expose`

**In:** `env_id` · `port` · **Out:** `{ "url": …, "stability": "ephemeral" | "stable" }`

Thin on purpose. It exists for the address half of the promotion contract.

### `env_promote`

**In:** `env_id` · optional `domain` · optional preservation requests
**Out:** **the same `env_id`**, the new stage, and:

```jsonc
{
  "env_id": "env_7f3a9c",
  "stage": "persistent",
  "preservation_report": {
    "filesystem":  { "result": "preserved" },
    "volumes":     { "result": "preserved", "bytes": 5138022400 },
    "env":         { "result": "preserved", "keys": 7 },
    "attachments": { "result": "preserved", "count": 2 },
    "address":     { "result": "recreated", "note": "temporary hostname now 308-redirects" },
    "processes":   { "result": "recreated", "note": "same argv, same cwd, restarted once" },
    "excluded":    ["/proc", "/sys", "/dev", "/run", "/tmp", "/boot", "/lib/modules",
                    "/etc/fstab", "/etc/machine-id"],
    "downtime_seconds": 34
  }
}
```

Every field is one of `preserved | recreated | lost`.

**Conformance:** an implementation that reports `lost` for something it advertised in
`guarantees` **fails**. This single rule is what makes the profile more than vocabulary.

`excluded` is required, not optional. An exclusion list that lives only in an implementer's
source is a list that silently drifts from reality; printing it makes drift reviewable.

`downtime_seconds` is required and MUST be measured. **A number that was not measured MUST NOT be
reported** — omit the field and report `unknown` instead.

### `env_destroy`

**In:** `env_id` · **Out:** a terminal receipt reference.

### `env_receipt`

**In:** `env_id`, window · **Out:** actor identity, operations performed, resources touched, cost
incurred, and:

```jsonc
{ "redaction": { "applied": true,
                 "classes": ["env-values", "command-output"],
                 "note": "names and counts retained, values withheld" } }
```

`redaction` is **required**. Any implementation recording what an agent did is building the most
attractive secret store in its own perimeter — command output and environment values leak
passwords, tokens and private keys as a matter of course. The schema forces the implementer to
state what was withheld, so a reader can tell a redacted record from a complete one.

---

## 3. MAY operations

Absence is conformant. A client MUST tolerate their absence.

- **`env_quote`** — preflight cost estimate. MUST carry `confidence`, `basis`, and
  `binding: false` unless the provider commits. Optional specifically because **an estimate
  nobody can trust is worse than no estimate**: it invites a decision it cannot support.
- **`env_sleep` / `env_wake`** — idle economics. Optional because cold-start cost can exceed the
  memory saved once the agent's burned tokens are priced in, so it is not universally a win.
- **`env_detach`**.

---

## 4. Stages and transitions

```
             env_create
                 │
                 ▼
          ┌─────────────┐   env_promote    ┌──────────────┐
          │  ephemeral  │ ───────────────▶ │  persistent  │
          └─────────────┘                  └──────────────┘
                 │                                 │
                 └──────── env_destroy ────────────┘
```

Promotion is **one-way** in this version. Demotion is not specified: reversing it would mean
undoing consequences (certificate issuance, DNS propagation, billing transitions) rather than
state, and a profile must not describe an operation its implementations cannot honestly perform.

---

## 5. Errors

MCP tool errors, with a stable machine-readable `code`: `not_found` · `conflict` ·
`quota_exceeded` · `budget_exceeded` · `capability_denied` · `not_ready` · `unsupported` ·
`irreversible_precondition`.

`capability_denied` MUST name the capability from `env_get` that was violated, so a refusal is
attributable rather than mysterious.

---

## 6. Non-goals

Numbered, because publishing them is load-bearing.

1. **Command execution, file transfer, pty, terminal multiplexing.** The profile hands over a
   connection descriptor and gets out of the way. This is also how "the brain stays on the
   customer's machine" is encoded: the agent and its model credentials never transit the
   provider.
2. **Image building and environment definition.** OCI and `devcontainer.json` own this.
3. **Transport, framing, auth.** MCP and OAuth 2.1 own this.
4. **Rollback, checkpoints, reversibility.** Not implementable across a full stack: sent mail,
   webhooks, payments, DNS propagation and certificate issuance do not roll back. Moving state
   back is not moving consequences back.
5. **Data branching and production forks.** A fork of production without a fork of the outside
   world sends real mail to real customers and charges real cards.
6. **Capability enforcement inside the guest.** Declared, never enforced by the profile (§1.1).
7. **Inferring a build spec from a recorded session.** Dies on `curl … | bash`: one line
   recorded, hundreds of filesystem changes made. The interception layer is wrong, and no model
   quality fixes it.
8. **Billing, quotas, abuse policy, isolation technology.** A provider's cost of goods, not a
   product surface.
9. **Model or token access.** Out of scope by principle: the agent is the customer's.
10. **Multi-environment scheduling.**

---

## 7. Conformance

A suite ships with `spec/0.1/`, not with this draft, and is published as a **scoreboard rather
than a certification** — the precedent is Acid2 and Web Platform Tests, which pressured vendors
who never agreed to be tested.

Two rules bind the scoreboard:

1. **We score ourselves and publish our own failures.** A scoreboard whose author wins every row
   is dismissed on sight.
2. **It tests capability, never quality.** "Implementation X does not implement `env_promote`" is
   a fact. "X is worse" is a lawsuit and a bad-faith signal.
