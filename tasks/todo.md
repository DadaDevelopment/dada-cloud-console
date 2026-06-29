# 2026-06-29 Default project + single-project console navigation

User report: "создать проект не работает" (spinner hangs / nothing happens), and a
redesign: there should be ONE default project that exists immediately; if the user
has a single project the console lands INSIDE it (not on the projects overview); the
"create project" action lives in the top-bar dropdown (replacing the "Все проекты →"
link); the flat /projects overview is no longer needed because the dropdown switches.

Decisions (confirmed with user):
- Default project is auto-provisioned on the BACKEND.
- "Create" lives in the dropdown switcher.
- /projects overview is dropped as a landing → redirect into default project.

## Root-cause of "висит"
Manual create + navigate is correct in code for both god and normal users (personal-org
cascade makes the new project visible). The infinite spinner is the SSO token-getter
hanging (token expires between page-load GET and modal POST → silent refresh stuck →
fetch never fires → `submitting` never resets). Fix = hard timeout on apiFetch so any
hang surfaces as an error instead of a forever-spinner, AND route around the empty
state entirely via auto-default-project.

## Plan
- [ ] Backend: idempotent `POST /api/v1/projects/default` — returns the caller's default
      project, creating it (personal org = username, slug `<username>` sanitized, fallback
      `default-<hash>`) when they have zero. Reuses CreateProject insert logic. God users
      with zero projects also get one (so console always has a home).
- [ ] Frontend `apiFetch`: AbortController timeout (~20s) → throws on hang, no infinite spinner.
- [ ] Frontend: extract `CreateProjectModal` into `components/shell/create-project-modal.tsx`
      (shared by switcher + bootstrap).
- [ ] Frontend `ProjectSwitcher`: replace "Все проекты →" footer with "+ Создать проект"
      that opens the modal inline; on create → refetch list + route into new project.
- [ ] Frontend `ProjectProvider`: after list loads, if empty → call bootstrap default and
      route into it; expose `defaultProjectId`.
- [ ] Frontend `/projects` page → redirect into default/first project (overview dropped).
- [ ] Console root entry → land inside default project instead of /projects overview.
- [ ] Verify: create works, no infinite spinner, single project auto-lands, dropdown create works.

## Review
Done & verified (build + go test ./internal/api green; tsc + eslint clean):
- Backend `EnsureDefaultProject` (`POST /projects/default`), idempotent: returns the
  first visible project or provisions a default (personal org = username) when zero.
  Shared `insertProject` helper; `defaultProjectSlug` for a stable per-user slug
  (unit-tested). Route registered; swagger.json regenerated (coverage gate green).
- Frontend `apiFetch`: 30s AbortController timeout → hung request throws instead of
  spinning forever (root-cause mitigation for the "висит" symptom).
- Shared `CreateProjectModal` extracted; `ProjectSwitcher` footer is now
  "+ Создать проект" (was "Все проекты →") and refetches the list on create.
- `ProjectProvider` bootstraps the default project on empty list and routes into it;
  exposes `defaultProjectId` + `refetchProjects`.
- `/projects` overview replaced by a redirect into the default/first project.

Not verified in a live browser: needs Keycloak SSO + Postgres backend, which a local
preview can't faithfully exercise. Backend logic is unit-tested; frontend typechecks.
Remaining (out of scope / infra): the underlying SSO silent-refresh hang, if that was
the true cause, is only mitigated (timeout), not fixed in the auth layer.
