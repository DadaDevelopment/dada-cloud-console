# Lessons

- When the user points at git watcher / gitops-agent sync, verify the repo-local agent in the current workspace first; do not cross over to similarly named infra repos.
- If the request is about project sync in the UI, treat `projects` table bootstrap and `project.yaml` state-repo bootstrap as first-class sync surfaces, not optional extras.
- When the user flags an infrastructure fallback as suspicious, inspect it as a fail-open risk first; DB errors or token-decrypt failures must not silently reroute writes into a shared default repo.
- For create-project UX, keep the self-service path minimal: one visible name field, personal org implied, and use a clearly readable placeholder/input contrast.
- When a hidden backend slug is derived from a visible field, do not gate the submit button on the hidden value; gate only on the visible input and derive the backend-safe slug during submit.
