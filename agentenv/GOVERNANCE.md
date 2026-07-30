# Governance

One page, and it says the unflattering true thing.

## Now

v0.x is **BDFL**: one company, one maintainer. There is no working group, no technical steering
committee and no RFC process. Inventing them at zero implementations is standards-body cosplay,
and it reads as fake to exactly the audience that matters.

## Versioning

Semantic versioning, one directory per minor version. Pre-1.0, breaking changes land on minor
bumps. Servers declare the versions they support through their capabilities.

`IMPLEMENTATIONS.md` lists every known implementation with a truthful count, including which of
them we wrote ourselves.

## One forward commitment

**At three independent implementations, the spec moves to a neutral home.** That single
falsifiable sentence is worth more than a fabricated committee.

## Kill criteria

Written down in advance, because a project that cannot say when it failed will simply reframe.

- **Day 30 — the client has no users.** Fewer than 10 distinct users who ran it in two separate
  weeks. This **blocks** promoting the draft to a normative spec: writing a standard on top of an
  unused tool is how you get forty stars and no implementers.
- **Day 90 — no external implementation.** Zero merged pull requests from outside the company
  adding an adapter or fixing the spec, **and** zero third-party implementations. Then: delete
  `spec/` and `conformance/`, keep the client if it has users, retitle the repository "client and
  adapters", and say so publicly in one paragraph.
- **Month 6 — the only implementations are ours.** Adapters we wrote do not count as adoption, no
  matter how many. Downgrade the repository permanently and stop describing it as a standard.
- **Any time — the company takes a strategy that makes this artefact hostile to its own
  partners.** Then delete the spec that week. Holding two incompatible strategies is how focus
  dies.
