# Members and roles

> **Doc-vs-code note:** confirmed — the console genuinely has no in-UI description of what
> each role can do. The role list below is written from `frontend/lib/rbac.ts` (the actual
> enforcement logic), since the console itself won't tell your teammates this.

## What it's for

Give teammates or clients access to a project without sharing one login. Access is
role-based and project-scoped.

## The roles, what they actually let you do

From most to least privileged — a person's *effective* role in a project is the higher of
their org-wide role and their project-specific role:

- **Owner** — full control: manage members, billing, and everything below.
- **Admin** — same day-to-day capabilities as Owner (see manifests/approve below); the
  practical difference from Owner is mostly organizational.
- **Developer** — can deploy, restart, edit env vars, manage domains, databases, storage,
  monitoring — basically everything needed to ship and operate an app.
- **Read Only** — can view everything, cannot change anything.

More precisely, three capability tiers govern what's visible/clickable:

- **Mutate** (Owner/Admin/Developer) — create, deploy, restart, delete resources.
- **See manifests / edit YAML / approve** (Owner/Admin only) — view raw compose/values YAML,
  edit them directly, see the **Builds** nav item, and approve GPU model requests.
- Everyone with project access can see dashboards, metrics, and logs.

## How to invite someone

1. Go to **Members** → **Add member** (only visible to Owner/Admin).
2. Enter their **Email** and pick a **Role**.
3. Check **Send email invitation** if they don't have an account yet — this sends an actual
   invite email. Leave it unchecked to add an existing platform user to the project
   immediately (no email sent).
4. Submit.

## Changing or removing access

- Change a member's role inline from the dropdown next to their role pill in the Members
  table — takes effect immediately, no confirmation step.
- Click **Remove** on a member's row (confirmation dialog shows their email) to revoke project
  access.

## Gotchas

- **Org-vs-project scope is genuinely confusing**, as the inventory notes: someone's org-wide
  role can grant them more than their project-row shows, because effective role = max(org
  role, project role). If a "Read Only" project member can still edit things, check their org
  role.
- **Builds/manifests visibility (`canSeeManifests`) is Owner/Admin only** — a Developer won't
  see the "Builds" nav item or be able to view/edit raw YAML, even though they can deploy and
  restart apps. This is deliberate, not a bug.
- **Service accounts appear in the Members table (Type column)** but there is no UI to create
  one — they can only show up there through some other process, not through **Add member**.

## Not yet supported

- In-console explanation of what each role can do (this doc exists specifically to fill that
  gap).
- Creating a service account from the UI.
- Scoping a member's access to only some resources within a project (it's all-or-nothing per
  project).
