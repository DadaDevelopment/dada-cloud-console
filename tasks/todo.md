# Gitops Agent Project Sync

- [x] Inspect the repo-local gitops-agent and current state-repo bootstrap behavior
- [x] Add project bootstrap/write support so DB projects are mirrored to `project.yaml` in Git
- [x] Add git→DB handling for `project.yaml` so manual git changes win and sync back into the `projects` table
- [x] Update the state-repo init script and tests so first-start sync covers existing projects
- [x] Verify the gitops-agent package and relevant tests locally
- [x] Push the branch after verification

## Review

Added a project-level GitOps bootstrap/sync path to `gitops-agent`: DB projects now bootstrap to `clusters/beget-prod/projects/<slug>/project.yaml`, git-side `project.yaml` files are parsed back into the `projects` table, and the init script now seeds `client-a/project.yaml` too. Verified with `go test ./...` inside `gitops-agent` and pushed to `main`.

# Build on GitHub

- [x] Reproduce the current GitHub build surface and identify the missing piece
- [x] Add a GitHub Actions workflow that matches the release build path
- [x] Verify backend, frontend, Helm render, and Docker image build steps locally as far as the environment allows
- [x] Confirm the workflow file is present and ready for GitHub to pick up

## Review

Added a GitHub Actions workflow that mirrors the release build path from Jenkins and uploads the releaseable backend/frontend artifacts.

## 2026-05-14 console API base URL fix

- [x] Find why production frontend still targets `localhost:8080`
- [x] Move the local-dev API URL out of the production build path
- [x] Align Helm and CI on `NEXT_PUBLIC_API_URL=/api`
- [x] Render-check the Helm chart and confirm the config now matches the runtime intent

## Review

Production frontend had a build-time env leak: `frontend/.env.local` set `NEXT_PUBLIC_API_URL=http://localhost:8080`, and Next.js inlined that into the client bundle. I moved the local-only value to `frontend/.env.development.local`, set the CI frontend build to `NEXT_PUBLIC_API_URL=/api`, and renamed the Helm value key to `NEXT_PUBLIC_API_URL` so the chart matches the code.

# 2026-05-29 AI Studio hardening + VM track UI slice + prod push

- [x] Inspect current uncommitted AI Studio/probe/migration changes and preserve the valid parts
- [x] Verify current backend/frontend/agent gaps with real local commands and note missing run configurations
- [x] Finish AI Studio hardening slice:
  add tests for quota decision matrix, keep approval semantics correct, wire readiness/liveness/probe behavior, and keep migration 005 role-agnostic
- [x] Finish one VM-track feature slice end-to-end:
  expose environment runtime/app_server fields through the backend and frontend, add App Servers UI + API client/types, and make the project/app flows VM-aware where needed
- [x] Run repo verification gates for touched areas
- [ ] Prepare production push path with exact evidence and document what was or was not actually deployed

## Review

AI Studio hardening now has a pinned quota decision matrix, role-agnostic migration 005 default privileges, and split backend liveness/readiness probes with Helm pointing readiness at `/ready`. VM track now has environment runtime/app_server fields in the project DTO, frontend types/API helpers for AppServers, a project App Servers page with create/delete operation handoff, runtime badges on project/app screens, and an explicit VM-app deployment guard until the Portainer stack worker path is implemented. Verified locally with backend tests/build, gitops-agent tests/build, portainer-agent tests, frontend lint/build, and Helm lint/template.
