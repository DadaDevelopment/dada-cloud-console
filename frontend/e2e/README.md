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

## Jenkins

The `Jenkinsfile` runs two stages after the deploy write-back (only on push branches):

- **E2E smoke** — always on, `catchError` → UNSTABLE (never fails the deploy). Runs `--project=smoke` against `https://console.dada-tuda.ru`. Note it exercises the *currently-live* console, not the just-built image (Argo syncs asynchronously after the write-back), so it is a continuous smoke gate rather than a per-build acceptance gate.
- **E2E authed** — gated behind the `RUN_E2E_AUTHED` build parameter (default off) and two Jenkins credentials. Provisions a real DB, so it stays opt-in.

## Standing up the authed environment

Already provisioned:

- Disposable project **`dada-e2e-playwright`** — `E2E_PROJECT_ID=10d07d3f-e2fb-4a15-95b3-ae199067db53` (default env `997554fc-b388-4cd8-8fa3-fce1b70e04db`). Safe to trash.

Still to do (needs Keycloak admin + Jenkins admin — an account + password, so done by a human, not the agent):

1. In Keycloak (`https://id.dada-tuda.ru`, realm `master`) create a user `e2e-bot`, set a password, mark the email verified. Grant it access to the disposable project (add it as a member, or let it use its own auto-provisioned project and use that id instead).
2. In Jenkins add credentials:
   - `e2e-console-user` — *Username/password* = the `e2e-bot` login.
   - `e2e-project-id` — *Secret text* = `10d07d3f-e2fb-4a15-95b3-ae199067db53`.
3. Run the job with **Build with Parameters → `RUN_E2E_AUTHED` = true**.

## Next

- Grow authed specs to cover the other optimistic creates (app, bucket, model, endpoint) — same pattern.
- Once a rollout-wait (Argo image tag == built tag) is wired, promote smoke from UNSTABLE-only to a per-build acceptance gate on the just-built image.
