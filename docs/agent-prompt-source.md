# Agent prompt source: prompt and skills from the client git repo

Status: draft PR `agent-prompt-source`, 2026-09-17.

## Why

Today an agent prompt is a 30-50 KB string pasted into `saveAgent`, and skills are
pasted by hand into `agentRuntime.agentSkills.<agent>.<file>.md` of the console
values in argo-infra. Both are unreproducible and both have already caused outages
(a prompt-only save dropped modelConfig and tools; a tool without allowed headers
answered 403). The prompt and the skills now come from one place: a directory in
the client's git repository, in a fixed layout, and the console syncs from it.

## Repository format

```
agents/<agent-name>/core.md            system prompt
agents/<agent-name>/domains/<skill>.md  one skill per file, file stem = skill name
```

Rules the sync enforces (a violation is a sync error shown on the agent page, the
previous synced prompt stays in force):

- `core.md` must exist and be valid UTF-8, 1 byte to 128 KiB (`kagent.MaxPromptBytes`).
- First line of `core.md` is `# <title> <sep> <version>` where `<sep>` is the
  middle dot U+00B7 (the reference repo already uses it). The
  text after the separator is `prompt_version` (`2026-09-16.native.46`). A header
  without the separator gives an empty version and is refused: a prompt that
  cannot be told apart from its predecessor is what made the pasted string
  unreproducible.
- Every `domains/*.md` must be valid UTF-8, 1 to 8192 bytes
  (`agentruntime.MaxSkillContentBytes`). The stem must match
  `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`, the name the runtime accepts in `load_skill`.
- Files outside `core.md` and `domains/` (for example `experiments/`) are ignored.

Reference: https://github.com/DadaDevelopment/tg-agent-tools, `agents/tg-exchange-support`.
The layout is the one `docs/AGENT-REPO-SPEC.md` and `agentkit/skills.py` already
describe; this document fixes what the console checks on it.

## Data model

Migration `156_agent_prompt_sources.sql`, table `agent_prompt_sources`, one row per
`(project_id, environment_id, agent_name)`:

| column | meaning |
|---|---|
| `installation_id` | `git_app_installations.id` that reaches the repo (org-scoped, same resolution as `connectGitRepo`) |
| `repo_full_name`, `ref`, `path` | `owner/name`, branch (default `main`), directory (default `agents/<agent-name>`) |
| `resolved_sha`, `synced_at` | commit the current prompt and skills came from, and when |
| `last_checked_at`, `last_sync_status`, `last_sync_error` | `ok` / `error` / `pending` and the message the UI shows |
| `prompt`, `prompt_title`, `prompt_version` | parsed `core.md` |
| `skills` JSONB | `{"<skill>": "<content>"}` |

The row is the console's copy. Git in the client repo is the source, the
ManagedAgent claim in argo-infra is the delivered artifact. There is no agents
table to extend: an agent is a `resource_snapshots` row mirroring the claim.

## Sync flow

`backend/internal/promptsource` (fetch + parse + validate, no DB) and
`backend/internal/api/agent_prompt_source.go` (handlers + syncer).

1. Mint an installation token with `github.MintInstallTokenForRepos` narrowed to
   the one repo, using the App id and key the backend already holds for cloud
   tasks. Tokens are cached per installation until 5 minutes before expiry.
2. `GET /repos/{repo}/commits/{ref}` gives the head sha. If it equals
   `resolved_sha` and the last sync was `ok`, the sync ends here (idempotent, the
   poller does this every tick and writes only `last_checked_at`).
3. `GET /repos/{repo}/git/trees/{sha}?recursive=1`, keep the entries under
   `path`, `GET /repos/{repo}/git/blobs/{sha}` for `core.md` and `domains/*.md`.
4. Parse and validate (section above). Any problem: row gets `error` and the
   message, nothing else changes, the agent keeps answering with the last good
   prompt.
5. Row gets prompt, version, skills, sha, `synced_at`, `ok`.
6. An `UpdateAgent` operation is enqueued with `name`, `prompt`, `prompt_version`
   only. The gitops-agent renders the claim and `fillUnsaid` carries modelConfig,
   runtime, description, tools, env, memory and the Langfuse project over from
   the claim already in git. This is the same path `saveAgent` uses; the sync
   never restates those fields, so it cannot drop them.
7. Audit row `SyncAgentPromptSource` and a log line with agent, sha, file count
   and bytes.

A source can be attached only to an agent that already exists as a ManagedAgent
claim. Attaching one to a name without a claim would enqueue a create with no
modelConfig, which is the outage class this feature removes.

While a source is attached, `saveAgent` replaces `prompt` and `prompt_version` in
the request with the synced ones: a hand edit cannot desync the claim from the
repo. The MCP `saveAgent` keeps working for tools, env and model.

### Skills delivery

Skills go to the agent runtime through Postgres, not through the values file.
`agentruntime.NewPGDomainProvider` answers `load_skill` and the per-turn skill
refresh from `agent_prompt_sources.skills` when the agent has a synced source, and
falls back to the mounted files otherwise. The agent-runtime already shares the
console database.

Why not a ConfigMap through git: the runtime mounts one volume per agent from
`agentRuntime.agentSkills` keys, so every new agent needs a values change and a
rollout anyway; the ManagedAgent claim lands in the `kagent` namespace while the
runtime reads from its own, so a sibling ConfigMap next to the claim is not
mountable; and extending the claim with `spec.skills` needs an XRD change in
argo-infra, which this PR does not touch. The database copy is live on the next
turn, keeps the 8192 byte cap, and the git history of the client repo is the
audit trail (`resolved_sha` on the row, sha in the operation payload).

The `agentRuntime.agentSkills` values keep working for agents without a source and
can be removed per agent once its source is attached.

## Auto-sync

`AGENT_PROMPT_SOURCE_POLL_INTERVAL_SECS` (default 60, 0 disables). The backend
polls every row: one `commits/{ref}` call per agent per tick, a full fetch only
when the sha moved. Push webhooks arrive at the build-agent, not the backend; a
webhook nudge is not done in this PR (open question below).

## API

All under `/api/v1/projects/{projectId}/environments/{envId}/agents/{name}/prompt-source`,
writer role (GET needs membership). Not on the MCP keep list yet: adding a tool
bumps the advertised tool count in twelve marketing and docs files, which is a
separate change.

- `GET` returns the row with skills as `{name, bytes, content}`.
- `PUT {repo_full_name, ref?, path?, installation_id?}` stores the source and runs
  a first sync synchronously; the response carries the sync result.
- `POST /sync {force?}` runs a sync; `force` re-enqueues the claim even when the
  sha did not move.
- `DELETE` detaches the source; the last synced prompt stays in the claim and the
  prompt field becomes editable again.

## UI

Agent editor on the agents page: a "Prompt source" section. Without a source: a
form (repo, branch, path) and a connect button. With a source: repo, ref, path,
sha, synced-at, status and error; the prompt read-only in monospace with the
parsed version; the skills list with sizes, each expandable; "Sync now" and
"Disconnect". The prompt textarea is hidden, replaced by a note pointing at the
repo path.

## Rollout and rollback

- Migration adds one table; nothing reads it until a source is attached.
- Attaching a source enqueues one claim update, the same as a `saveAgent`.
- Rollback: `DELETE` the source (or delete the row); the claim keeps the last
  synced prompt, the runtime falls back to mounted skills. Setting the poll
  interval to 0 stops auto-sync without detaching sources.

## Not done in this PR

- Push webhook nudge from the build-agent to the backend (poller only).
- MCP tools for the prompt source (`getAgentPromptSource` and friends); the
  MCP `saveAgent` already respects an attached source.
- Removing the existing `agentRuntime.agentSkills` block from argo-infra values.
- GitLab sources; only GitHub App installations are supported.
- A per-agent sync history beyond the audit log and operation rows.
