# Lessons

- When the user points at git watcher / gitops-agent sync, verify the repo-local agent in the current workspace first; do not cross over to similarly named infra repos.
- If the request is about project sync in the UI, treat `projects` table bootstrap and `project.yaml` state-repo bootstrap as first-class sync surfaces, not optional extras.
- When the user flags an infrastructure fallback as suspicious, inspect it as a fail-open risk first; DB errors or token-decrypt failures must not silently reroute writes into a shared default repo.
- For create-project UX, keep the self-service path minimal: one visible name field, personal org implied, and use a clearly readable placeholder/input contrast.
- In this repo, distinguish user source git from the platform GitOps state repo: a project may have no source repo at all and still legitimately write manifests to one shared internal state repo.
- When a hidden backend slug is derived from a visible field, do not gate the submit button on the hidden value; gate only on the visible input and derive the backend-safe slug during submit.
- In multi-account provider flows, do not hide "connect another account" inside the empty state; surface that action on the main picker screen next to the current account summary.

## 2026-07-10 — Verify DB/live state before claiming a resource is "not indexed / invisible"
Context: chased a "buckets invisible in console" разъезд. Asserted TWICE that mimir/opensearch
S3Buckets were "not in resource_snapshots" WITHOUT querying the DB, and shipped a git-watcher
chart-template parser to "fix" it. Then found `worker/discovery.go` already indexes every S3Bucket
XR from the live cluster (resolved values) — the buckets WERE in the DB all along. Had to revert.
Also memory `project_gitops_snapshot_sync` documented discovery.go — I didn't read it first.
Rule: before claiming a resource is missing/invisible/not-indexed, (1) grep the memory index for
the subsystem, (2) query the authoritative store (here: `resource_snapshots` via psql) — a code path
existing ≠ it's the only path, and "I couldn't easily get DB access" is not license to assume.
Tag the claim [live] only after the query, else mark HYPOTHESIS.

## 2026-07-13 — Partial-staged commit must build from the COMMITTED tree, not the dirty worktree

Mistake: committed a multi-file feature (S1 managed-DNS) with explicit `git add <path>`
and left out a co-dependent file (`handler.go` declaring `Handler.pdns` that the
committed `managed_dns.go` references). Working tree built fine, so I called it green
and pushed — but the COMMIT itself did not compile. CI built a broken main.

Root cause: `go build` on the dirty worktree proves the worktree, not the commit.
Explicit-path staging (correct for shared trees, M3) makes it easy to miss a file the
staged files depend on.

Gate (mechanism, not a lesson line): before pushing a partial/explicit-path commit,
verify the COMMITTED tree builds — `git stash --keep-index && go build ./... ; git stash pop`
(or build a fresh `git worktree add` of HEAD). If a concurrent agent owns unstaged files
(can't stash), reason explicitly about whether the omitted files are build-dependencies of
the staged ones. Never infer commit-buildability from a dirty-worktree build.

## 2026-07-25 — react-joyride v3 fires no TOUR_START; Next/Turbopack fetch is unhookable

Mistake: onboarding "seen" persistence was wired to `EVENTS.TOUR_START` in the `onEvent`
callback. In react-joyride 3.2.0 (the v3 rewrite) `onEvent` emits STATUS transitions
(`STATUS.SKIPPED`, `STATUS.FINISHED`) but never delivers a usable `TOUR_START` — the branch
was dead, so "seen" was never recorded and every user got re-nagged. This is a v3-vs-v2 API
drift I adapted wrong: v2 used `callback`; v3 renamed it `onEvent` and changed the emitted
event surface. Fix: record `seen` deterministically at show-time (inside the setTimeout that
flips `run`), not from an event.

Second trap in the same debug session: a `window.fetch` monkeypatch did NOT intercept the
app's own POSTs. Next 16 + Turbopack dev binds `fetch` internally, so app requests bypass the
`window.fetch` you patched — the probe caught your synthetic call while the real POST sailed
past uncaught yet visible in the network panel. Verify app network via the browser network
panel or a source-level hook, never a `window.fetch` shim under Next/Turbopack.

Rule: when adapting a major-version-bumped UI lib, read the new version's event/prop contract
before mapping old handlers — a compiling `EVENTS.X` reference is not proof the event fires.
And under Next/Turbopack, `window.fetch` interception is not a valid verification channel.
