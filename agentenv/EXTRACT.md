# Extracting this into its own repository

This directory lives inside the product repo while it is being written, so it can be reviewed in
context. It is meant to become a standalone public repository, and nothing in it depends on the
parent repo.

```sh
# from the repo root
git subtree split --prefix=agentenv -b agentenv-split
git push git@github.com:<org>/agentenv.git agentenv-split:main
```

Before that push, three things need a human decision, and none of them are mine to make:

1. **Organisation and visibility.** Public, and under which account.
2. **`LICENSE`.** Apache-2.0 in full — add it with GitHub's licence picker at repository creation
   so the text is the canonical one rather than a copy of a copy.
3. **The strategic fork.** Adapters that make hosting providers interchangeable put this project
   on the anti-lock-in side, which is incompatible with ever selling the same ladder to those
   providers as an OEM component. `GOVERNANCE.md` already commits to deleting the spec if that
   second path is taken — so choose consciously rather than by inheritance.

## What is deliberately not here yet

The CLI. Its engine is a working local runtime being built in the product repo, and it will be
extracted once it runs — not written twice and not written speculatively. A reference client that
cannot be run is a museum piece, and shipping one would contradict the discipline the spec is
about.

The normative `spec/0.1/` and the conformance suite are gated on the client having users who came
back. That gate is in `GOVERNANCE.md` with a date attached.
