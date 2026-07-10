# Feature Inventory — dada-cloud Console

**Purpose:** authoritative per-resource map of what a user can actually do today, the
Jobs-To-Be-Done each resource serves, and the gap between what the product *does* and how it
*presents itself*. Drives three workstreams: **Docs** (every resource), **Landing** (only the
best/differentiating), **UI intuitiveness** (every resource).

This is a living document. When a resource ships new capability or a gap closes, update its row.

**Sources:** console audit of `frontend/app/(console)/**`, resource registry
`frontend/lib/resources.ts`, product north-star `docs/product/product-gtm-vision.md`.

---

## Legend

Landing verdict:

- 🟢 **hero-worthy** — differentiator or activation driver, belongs on the main landing.
- 🟡 **feature-block** — mention it, don't over-promise.
- 🔴 **do not surface** — half-built or pure hygiene; surfacing it hurts trust.

Each resource lists: **Insight** (the discovery/usability gap to fix), **JTBD**, **Docs**
(what to write — all resources get docs), **Landing** (verdict + why), **UI** (intuitiveness fix).

---

## 1. Applications — 🟢 landing

- **Insight:** three deploy paths (Docker image / GitHub push-to-deploy / docker-compose) are
  collapsed behind one button with no explanation of the difference. The `Adopt` action has no
  label saying what it does. The live compose editor exists but a GitHub-import user never
  discovers it. Framework auto-detect (20+ presets: Spring / FastAPI / Next / Go / …) is a strong
  feature buried inside import step 3.
- **JTBD:** get from source to a live URL in minutes; every `git push` auto-deploys; pull an
  existing compose stack into the panel.
- **Docs:** GitHub→URL quickstart; deploy a prebuilt Docker image; compose app + live editor;
  env vars (build/runtime/secret scope); endpoints + auth + Swagger.
- **Landing:** 🟢 hero. 3-step flow + the auto-detected stack list as proof "your stack is
  already supported."
- **UI:** split "Deploy" into explicit choices (From GitHub / Docker image / Compose) —
  `components/deploy/deploy-chooser.tsx` is the seed. Rename `Adopt`→"Split stack into apps" +
  tooltip. Validate the Swagger endpoint field.

## 2. App Servers (VM / BYO) — 🟢 landing (primary differentiator)

- **Insight:** the most unique capability was buried under "Advanced" next to `coming soon`
  stubs. You can **order** a VM (Terraform) *and* **bring your own** (SSH → Portainer edge
  agent) — the UI never explains which to pick. Workload discovery + import (adopt already-running
  containers) is a killer feature with zero in-product guidance. `WaitingForAgent` with no timeout
  reads as "stuck". Retry can't change a wrong IP.
- **JTBD:** don't migrate off an existing VPS — connect it; let an agency run a fleet of client
  VMs on one stack with unified monitoring.
- **Docs:** connect your own server over SSH (what gets installed, one-shot key handling);
  adopt an existing compose stack (discovery → import); order a VM; agency fleet playbook.
- **Landing:** 🟢 dedicated "Bring your own server" page — the Coolify replacement + agency
  angle. Currently zero marketing coverage.
- **UI:** promoted into the **primary** nav group (this change lands in `lib/resources.ts`).
  Create modal: two explicit paths with descriptions. `WaitingForAgent`: progress + timeout +
  "what to check". Retry: allow changing the IP. Rewrite the "not enrolled" copy (it's
  backend-driven, not the user's fault).

## 3. Databases (managed Postgres) — 🟡 landing

- **Insight:** backups, **restore, and delete now ship** on the database detail page (Kanister-driven
  restore with a confirm-name gate; commits f4d35b4 / 9369e07 / faaa84f from the parallel DB
  session) — the earlier "back up but can't recover" gap is closed. Remaining gaps: Postgres only
  (marketing still implies MySQL/Redis, which actually live only on VMs via ServiceDatabase — the
  user won't guess that) and no connection metrics.
- **JTBD:** hands-off Postgres; `DATABASE_URL` injected into the service automatically.
- **Docs:** create Postgres + attach to an app; backups (schedule/retention) + restore + delete
  from the detail page; managed DB on a VM (ServiceDatabase, DSN).
- **Landing:** 🟡 feature-block inside the core path (DB next to deploy), but **do not brag about
  PITR/restore** until they exist.
- **UI:** add a restore button OR drop the backup promise from the card. Link "managed
  MySQL/Redis → on a VM". Add delete.

## 4. Object Storage (S3 / Beget) — 🟡 landing

- **Insight:** now usable end-to-end (was the weakest resource). Bucket cards are clickable → a
  detail page that **reveals live endpoint + access key + secret key on demand** and shows an
  `aws-cli` example. The reveal is real: backend `GetS3BucketCredentials`
  (`/s3buckets/:name/credentials?reveal=true`, write-gated + audited) reads the Crossplane
  connection secret; the `s3bucket-beget-terraform` composition defaults to publishing
  `<name>-s3-credentials` into `crossplane-system`, exactly where the backend reads (shipped
  9369e07; verified against live composition + RBAC, not yet exercised with a real console bucket
  since none exist in prod). The reveal now also honors a **git-adopted** bucket's declared
  `spec.connectionSecret` (from its snapshot, guarded to `*-s3-credentials` names) instead of
  assuming the console convention. Note: the console indexes buckets from BOTH sources — the git
  passthrough (`resources.values.yaml`) AND live-cluster discovery (`gitops-agent worker/discovery.go`
  lists every S3Bucket XR each tick with resolved values), so helm-chart-defined infra buckets
  (mimir/opensearch) ARE indexed and visible (verified in `resource_snapshots`). Reveal for those
  still 503s because their connection secret lives in the app namespace (monitoring/opensearch) and
  the backend RBAC only reads crossplane-system — a deliberate scope, not a bug. Remaining gaps:
  ru1 only (no region picker); marketing still implies
  CDN + versioning the product doesn't have; no delete.
- **JTBD:** S3 for media / static / backups.
- **Docs:** create a bucket + reveal S3 keys on the detail page; attach to an app; FTP/SFTP access.
- **Landing:** 🟡 now deliverable (create → reveal keys → use), but keep the CDN/versioning
  over-promise off marketing.
- **UI:** done — clickable cards + detail with reveal + aws-cli. Still add delete + a region picker.

## 5. Domains — 🟢 landing

- **Insight:** the most complete flow (~95%) but split-brain: apex verification lives on the
  Domains page while hostname attach lives in app settings. Users can't find "how do I attach a
  subdomain". Verify is manual (no auto-poll) — feels "stuck".
- **JTBD:** my own domain + HTTPS without cert wrangling.
- **Docs:** custom domain (apex TXT → verify); attach a subdomain to an app (CNAME) + auto-TLS.
- **Landing:** 🟢 feature-block — "domain + HTTPS in the same flow as deploy".
- **UI:** unify — show hostname attach on the Domains page. Auto-poll verify instead of a manual
  button (or "checking every 30s"). Explicit DNS / TLS / live statuses.

## 6. Monitoring — 🟢 landing

- **Insight:** rich (metrics / logs / alerts / channels / Grafana embed) but presented as "wire
  up an OTLP app" — infra language. A GitHub-deploy user doesn't realize their app is already
  monitored. Custom dashboards exist in the API but are disabled in the UI.
- **JTBD:** is my service alive, where's the problem, alert me on failure; for agencies —
  reliability guarantees to clients.
- **Docs:** app metrics + logs; alerts + channels (Telegram/Email/Webhook); monitoring your own
  VM / external app over OTLP.
- **Landing:** 🟢 reliability block — "logs, metrics, alerts out of the box" (outcome language,
  not "OTLP").
- **UI:** auto-attach monitoring to deployed apps (don't force manual create). Hide OTLP jargon
  behind "external source". Enable custom dashboards.

## 7. AI Models / AI-Studio — 🟡 landing (separate track)

- **Insight:** powerful (KServe deploy, canary, GPU approval, MLflow, playground) but this is an
  **expansion bet beyond the core backend-cloud thesis** — the GTM north-star explicitly warns
  against diluting the message. Correctly under Advanced.
- **JTBD:** ship an ML model as an inference endpoint without hand-rolling infra.
- **Docs:** deploy a model (S3/MLflow/docker); canary + promote; GPU quotas + approval;
  playground / inference.
- **Landing:** 🟡 not on the main hero. A dedicated product page for the ML segment — yes, but
  keep it out of the backend-cloud hero.
- **UI:** fine as-is. Visually connect GPU approval to the Approvals queue (currently decoupled).

## 8. Members / Roles — 🔴 landing

- **Insight:** roles Owner/Admin/Developer/ReadOnly + org invite work, but this is hygiene.
  Service accounts appear in the table yet can't be created from the UI. Org-vs-project scope is
  confusing.
- **JTBD:** team / client access by role (agency).
- **Docs:** invite a member, roles and what each can do; give a client access to a project.
- **Landing:** 🔴 hygiene. At most one line in the agency scenario ("role-based access").
- **UI:** explain the roles (what ReadOnly sees). Build service-account UI or drop the column.

## 9. Billing — 🔴 landing

- **Insight:** plans + quotas + consumption exist, but **upgrade = an external link**; there is
  no in-app payment. The landing's "hard limit / price-before-deploy" promise isn't implemented
  as a gate.
- **JTBD:** cost control; price before deploy.
- **Docs:** plans and limits; how to read consumption.
- **Landing:** 🔴 no standalone block (the pricing page already exists). Don't promise a hard cap
  until it actually gates.
- **UI:** a real in-app upgrade flow OR an honest "billing via support". Show a price estimate
  before deploy (as the landing promises).

## 10. AI Models Approvals — 🔴 landing

- **Insight:** the admin queue (GPU deploys) works but is decoupled from where the request is
  born (Models). A user doesn't understand why a deploy is "stuck on approval".
- **JTBD:** control who can spend on GPU.
- **Docs:** approvals — what requires sign-off (GPU), how to approve/reject.
- **Landing:** 🔴.
- **UI:** a direct link from Models/Operations → "waiting for approval → here".

## 11. Builds — 🔴 landing

- **Insight:** Git builds with streamed logs exist, but visible only to `canSeeManifests` roles
  and overlapping with Applications/Deployments — a source of confusion (the code already removed
  "Deployments" from the nav for this reason).
- **JTBD:** "why did my build fail".
- **Docs:** builds — log, cancel, retry; framework detection and override.
- **Landing:** 🔴 (part of the GitHub-deploy story, not standalone).
- **UI:** clarify the Builds-vs-Applications boundary so they don't duplicate.

---

## Removed from UI (not resources)

- **Operations** — the async-operation log. Removed from the nav as a browsable resource
  (per-app deploy history stays reachable from the app detail page). Not a product surface.
- **Redis** — was a `comingSoon` stub. Removed; created false expectation.
- **Message Queues** — was a `comingSoon` stub. Removed.

---

## Landing — shortlist (only the best)

Landing work is **iteration on existing marketing pages**, not from scratch.

1. **Applications / GitHub-deploy** — hero + auto-detected stack list.
2. **App Servers / BYO-VM** — dedicated page, the Coolify replacement + agency fleet angle.
3. **Domains + auto-TLS** — feature-block in the core path.
4. **Monitoring (outcome language)** — reliability block.
5. **Databases** — feature-block, without restore/PITR promises.
6. Separate **AI Models** product page — for the ML segment, outside the backend-cloud hero.

**Also:** mark beta / remove aspirational promises currently on marketing pages that the product
doesn't deliver — managed MySQL/Redis, S3 CDN/versioning, multi-region, PITR. Marketing must not
exceed product.

---

## Docs — all resources, now

Priority order:

- **P1** (core path, highest traffic): Apps quickstart, Databases attach, Domains, Monitoring.
- **P2** (differentiators with zero docs today): BYO-VM connect, adopt a compose stack, managed
  Ingress, ServiceDatabase, agency fleet.
- **P3**: Storage, Roles, Billing, Builds, AI Models, Approvals.

Location: `frontend/content/docs/` (served publicly at https://cloud.dada-tuda.ru/developer).

---

## UI intuitiveness — all resources, top fixes

1. ✅ Promote **App Servers** into the primary nav *(done in `lib/resources.ts`)*.
2. ✅ Explicit deploy paths — the deploy chooser now offers GitHub / Docker image / Compose
   (it already had 2; Compose added).
3. ✅ Databases — backup/recovery badge removed from the **list** card (ambiguous there). Restore
   and delete now live on the **detail** page, shipped by the parallel DB session (backend +
   UI, commits f4d35b4 / 9369e07 / faaa84f) — no longer a gap.
4. 🟡 Storage — bucket cards are now clickable → a detail view with metadata + an `aws-cli`
   example. Endpoint/access-key/secret are only shown *if* the API returns them; today it
   doesn't (backend gap below).
5. ✅ Domains — hostname attach now lives on the Domains page (split-brain killed) + verify
   auto-polls every ~30s.
6. ✅ Monitoring — primary path uses outcome language; OTLP/SDK setup moved behind an
   "Advanced: connect an external source" disclosure.
7. ✅ Removed `Operations` / `Redis` / `Queues` (and `Approvals`) from the nav.
8. ✅ Empty states — explanation + next step + "Learn more" → the matching guide under
   `frontend/content/docs/`.
9. ⬜ Link Approvals ↔ Models so "stuck on approval" is discoverable — **deferred**.

---

## Verified corrections (code beat the audit)

The subagents read the actual code; these correct earlier assumptions in this doc:

- **Apps deploy is not "one ambiguous button"** — `DeployChooser` renders explicit cards.
- **`Adopt` already has a clear label** — "Split into applications" + tooltip, i18n-driven.
- **Git import wizard is 3 sections** (Repository / Configure / Deploy); account-connect is a
  sub-state of section 1, not its own step.
- **Builds has no "retry"** — "Deploy" re-triggers a fresh build off branch HEAD, not the
  failed commit. There is also **no git disconnect/unlink** anywhere in the UI.
- **Storage region** — `ru1` is the *only* option (no picker), not a default.
- **Domains** — a Remove action does exist (detach hostnames first).
- **Billing** — includes a `ConsumptionBreakdown` cost estimate (Apps/DB/Storage subtotals);
  "View plans" is purely an external link (zero in-app plan changes).
- **Approvals** — global page at `/admin/approvals` (account menu, not per-project); reject
  requires a reason, approve has no confirmation.

## Backend gaps blocking a complete UX (roadmap)

These are not UI bugs — the API doesn't support them yet:

1. ~~Database restore + delete~~ — **SHIPPED** by the parallel DB session: `/restore` + `/backups`
   backend routes, `databasesApi.restore()`, and detail-page UI (restore modal with confirm-name,
   delete modal, backups list). Commits f4d35b4 / 9369e07 / faaa84f.
2. ~~Storage credentials in the API~~ — **SHIPPED** (9369e07): reveal endpoint
   `GET /s3buckets/:name/credentials?reveal=true` reads the Crossplane connection secret
   (`<name>-s3-credentials` in crossplane-system, matching the composition default); frontend
   detail page reveals endpoint/access-key/secret on demand. Verified against live
   composition/RBAC; not yet smoke-tested with a real console bucket (none exist in prod).
3. **In-app billing/upgrade + price-before-deploy gate** — upgrade is an external link; no
   payment flow, no hard-limit enforcement. The only remaining backend gap of the three.

## Marketing cleanup shipped

Landing now tells the truth: BYO-VM surfaced as the headline on `/cloud-servers`; managed
MySQL/Redis, S3 CDN/versioning, and PITR/restore promises removed or replaced with honest notes;
the Storage marketing page marked beta. **Open flag:** the Kubernetes marketing page has no
backing console resource — candidate for removal.
