# Console e2e (Playwright Test)

Browser end-to-end tests on the official [`@playwright/test`](https://playwright.dev) runner. No custom framework on top — projects, fixtures, `storageState`, and `webServer` are Playwright's own.

## Layout

- `*.smoke.ts` — unauthenticated. Proves the target is reachable and the shell renders. Runs anywhere.
- `auth.setup.ts` — logs into Keycloak once and saves the session to `e2e/.auth/state.json` (git-ignored).
- `*.authed.ts` — reuses that session for flows that need a login.

## Run

```bash
# Smoke against a running target (public landing or console):
E2E_BASE_URL=https://cloud.dada-tuda.ru npm run e2e:smoke

# Everything against the local dev server (Playwright boots `next dev`):
npm run e2e

# Authenticated + mutating flows against a disposable console + project:
E2E_BASE_URL=https://console.dada-tuda.ru \
E2E_USER=e2e@dada.local E2E_PASS=... \
E2E_PROJECT_ID=<disposable-project-uuid> E2E_MUTATE=1 \
npm run e2e
```

Install browsers once: `npx playwright install chromium`.

## Env

| var | purpose |
|---|---|
| `E2E_BASE_URL` | target URL; unset → local `next dev` on :3000 |
| `E2E_USER` / `E2E_PASS` | dedicated Keycloak e2e user for `auth.setup.ts` |
| `E2E_PROJECT_ID` | disposable project for mutating specs |
| `E2E_MUTATE=1` | opt-in gate; mutating specs (real DB provision) skip without it |

`optimistic-create.authed.ts` provisions a real managed Postgres — only ever point it at a disposable project, never a customer one.

## Design targets (next)

- Provision a dedicated e2e Keycloak user + disposable project, wire `E2E_*` as Jenkins credentials, add an e2e stage post-deploy.
- Grow authed specs to cover the other optimistic creates (app, bucket, model, endpoint) — same pattern.
