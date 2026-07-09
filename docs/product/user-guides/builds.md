# Builds and deployments

> **Doc-vs-code note:** the inventory's claim of "log, cancel, retry" is basically right, but
> "retry" isn't a dedicated button — see Gotchas. Also confirmed directly in code: streamed
> build logs are real (a WebSocket log viewer), and there's no disconnect/unlink action
> anywhere for a git connection once it's imported.

## What it's for

Two related but separate things live under "Builds":

1. The project-wide **Builds** nav page (`/git`) — every repo connected to an app, at a glance.
2. Each app's own **Deployments** tab — its actual build history, live logs, and
   rollback/promote controls.

## How to check connected repos (project-wide)

1. Go to **Builds** in the project nav (Owner/Admin only — see Gotchas).
2. Each row shows: provider badge (GitHub/GitLab), the **app name**, **production branch**,
   **root directory**, any **framework override**, and an **auto-deploy** badge if pushes to
   that branch trigger a new build automatically.
3. Click **View builds →** on a row to jump straight to that app's **Deployments** tab.
4. To connect a new repo, use **Import repository** — same flow as
   [Deploy an app from GitHub](applications-deploy-from-github.md).

## How to check an app's build/deploy history

1. Open the app → **Deployments** tab (or **Settings → Git → View deployments**).
2. Top button **Deploy** manually kicks off a fresh build from the tracked branch's latest
   commit — use this to force a rebuild without pushing a new commit.
3. **Deployments** section lists past deployments, newest first, with the live one flagged
   **Current**. From here:
   - **Rollback** any non-current deployment back to live.
   - **Promote** is only offered on the current deployment (used after a canary/manual
     promotion flow, mirrors the app's other promote actions).
4. **Builds** section lists the underlying build jobs (status, commit, branch, trigger,
   commit message). Click any row (or **Logs**) to open the build detail page with a
   **live, streaming log viewer** over WebSocket.
5. On the build detail page, a **Cancel** button appears while the build is still running.

## Gotchas

- **There's no per-build "Retry" button.** To rebuild after a failure, click **Deploy** again
  on the Deployments page (or push a new commit if auto-deploy is on) — it starts a fresh build
  from the branch's current HEAD, not a re-run of the exact failed commit.
- **The "Builds" nav item is Owner/Admin only** (`canSeeManifests`), but the underlying import
  route isn't — a Developer who navigates directly to `/git/import` and deploys a repo will
  successfully create the connection, they just won't see it afterward in the Builds list
  (though they can still see it via the app's own Settings → Git tab, or Deployments).
- **Live logs need the build subsystem configured** — if it isn't, the log viewer shows a calm
  "not available yet" message instead of erroring.
- **No disconnect/unlink action exists anywhere** for a git connection, and there's no UI to
  change the tracked branch/root directory/framework after import — re-run the import flow if
  you need to change these.

## Not yet supported

- Retrying the *exact* failed commit/build (only "start a new build from current HEAD").
- Disconnecting or editing a git connection's branch/root/framework after import.
- Manual project-wide build history (each app's history is separate; there's no
  cross-app build feed).
