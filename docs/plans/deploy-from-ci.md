# Deploy from CI — plan

## Shipped (this change)

Deploy-hook primitive + GitHub Action + console UI, so a user whose CI already
builds images can deploy prebuilt images without the platform build-agent.

- **Backend** `POST /api/v1/deploy` (token auth, no Keycloak) enqueues the same
  `DeployImageVersion` op as `PATCH .../apps/{app}/image`. Token minted/revoked
  per app via `.../apps/{app}/deploy-hooks`. Only the sha256 hash is stored.
  Migration `039_app_deploy_hooks.sql`.
- **Action** `github-action/dada-deploy/` — composite action + curl fallback.
- **Console** "Deploy from CI" card on the app page (create/copy/revoke token +
  ready-to-paste workflow snippet). Wizard Source step has a CTA to the
  prebuilt-image path.

Nothing here interprets the user's `.github/*.yml`. We never parse or run their
pipeline (that is a full GHA-runner reimplementation — explicitly rejected). We
accept the image they already build and re-pin it.

## Next: detect existing GitHub Actions + offer the agent

### Which wizard step

Detection input (the picked repo) is available after **Section 1 (Source)** —
`pickRepo` already calls `gitApi.detect`. The decision belongs in **Section 2
(Configure)**, which is where the user picks *how* to deploy. So: detect at
repo-pick (piggyback the existing detect call), surface the choice in Configure.

### Detection (small, reuses existing plumbing)

The glob is `.github/workflows/*.yml` and `*.yaml`. Build-agent already has the
primitives — no new GitHub API code:

- `build-agent/internal/server/server.go` `githubListDir(ctx, token, owner, repo, ".github/workflows")`
  with the install token already minted for `handleDetect`.
- Add `CIWorkflows []string` to the detect result:
  - `build-agent` detect response (the `scanFrameworkCandidates`/`handleDetect` path)
  - `backend/internal/buildagent/client.go` `FrameworkDetection` (~:150)
  - `frontend/lib/types.ts` `FrameworkDetection` (~:850)
- Populate it by listing that dir and keeping the `*.yml`/`*.yaml` names. Empty
  slice when the dir 404s.

### UX in Configure (Section 2)

When `detection.ci_workflows` is non-empty, show a callout with two explicit paths:

1. **Собрать у нас** (default, unchanged) — platform build-agent builds from
   source on every push. Keep as the zero-config default.
2. **Деплоить из моего CI** — the user keeps their pipeline; we:
   a. create the app as an image target (no `git_repos` build link),
   b. mint a deploy-hook token,
   c. offer the agent to wire it into their existing workflow (below).

Framing: "У вас уже есть GitHub Actions (N workflows). Собирать самим или
деплоить из вашего пайпа?" This is the honest fork — do not auto-pick.

### The agent offer (option 4)

Reuse the existing cloud-task → DadaAgent → PR substrate. The agent receives a
scoped install token + `repo.full_name` and opens the PR itself; the cloud only
records `pr_url` from the callback.

- Add one `cloudtask.Entry` in `backend/internal/cloudtask/catalog.go`
  (currently one entry, `yandex-metrika-goals`): `task_type:
  "github-actions-deploy-setup"`, `skill_id` of a new agent skill, `ResolveParams`
  passing the app name + the deploy-hook (or a freshly minted token reference).
- New agent-side skill (lives in the agent repo, not this monorepo): reads the
  user's `.github/workflows/*.yml`, appends a `dada-deploy` step (or a new
  `deploy` job gated on the build job) using `secrets.DADA_DEPLOY_TOKEN`, and
  opens a PR. It also emits the reminder to add the repo secret.
- **Feedback loop**: the agent reads the workflow run result (via the GitHub
  API with its install token) after the user merges, and can open a follow-up PR
  if the deploy step failed — the "agentic setup with feedback" path. The
  console chip already renders `pr_url`; extend the cloud-task panel to show the
  follow-up state.

### Sequencing

1. This change: deploy-hook + Action + card + wizard CTA. **done**
2. Detection: `CIWorkflows` field end-to-end + Configure callout. Small.
3. Agent task: new catalog Entry + agent skill + PR flow. Larger, agent-repo work.

Do 2 before 3 — detection is cheap and useful on its own (even just to link the
user to the "Deploy from CI" card). The agent is polish on top, not a prerequisite.
