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
