# Git hooks — trunk-based policy

This repo is **trunk-based**: all work lands on `main`. `feat/*` and `feature/*`
branches are **banned**.

## Hooks here

- **pre-commit** — blocks committing while on a `feat/*` or `feature/*` branch.
- **pre-push** — blocks pushing a ref that creates/updates a `feat/*` or
  `feature/*` branch (deletes are allowed, so you can clean old ones up).

## Enable (every clone)

`core.hooksPath` is not auto-applied on clone (git security). Run once:

```sh
git config core.hooksPath .githooks
```

Server-side enforcement (the real gate) is a GitHub repo ruleset that blocks
creating refs matching `feat/**` and `feature/**`. The hooks here are the local
fast-fail so you find out before you push.
