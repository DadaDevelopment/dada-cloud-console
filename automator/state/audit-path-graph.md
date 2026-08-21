# Audit path graph — new users, last 30 days (2026-07-22 .. 2026-08-21)

Session: sess-0821g. Read-only Postgres analysis, no mutations, no commits.

## Cohort

27 real users (post-exclusion-filter) created in the last 30 days. Zero of the
excluded categories (service accounts, e2e/a5-testuser/sp2verify, dada-tuda.ru
mail) survived the filter, so 27 = the raw count after filtering.

**Farm-wave contamination confirmed**: 18 of the 27 (67%) were created between
2026-08-08 18:28 and 2026-08-09 05:37 UTC — squarely inside the known
signup-farm window (`project_signup_farm_wave_pollutes_funnel.md`). All 18
verified against `users.created_at` individually; every one falls in that
window. Only 9 of the 27 new users are real signups for path-analysis purposes.

## Dead-signup check (strict definition: zero rows everywhere)

- Users with **zero audit_events rows** (excl. SignUp): **0 of 27.** Every
  user has at least one `SessionStart` row, so the strict "zero everywhere"
  definition finds no dead signups this window.
- Cross-check against builds/git_repos/agent_chat_messages/feedback for those
  zero-audit users: N/A (the zero-audit set is empty).
- **Caveat that matters more than the strict-zero result**: 18 of 27 users
  (all farm-wave) have **SessionStart and nothing else, ever** — 1-12 login
  events, zero product actions. This is real product inactivity that the
  strict "zero audit_events" filter cannot see, because `SessionStart` itself
  is an audit row. This is the exact pattern already on record as
  `project_signup_farm_wave_pollutes_funnel.md` (bot/farm accounts that only
  hit the login/session ping, never a real product surface) — confirmed here
  with fresh data, not a new finding.

No true "dead signup" (leak before first action, real user) was found in this
window.

## Instrumentation-gap check (zero in audit_events, nonzero elsewhere)

None. Every one of the 27 users has ≥1 non-SignUp audit_events row, so by the
task's literal definition there are no "zero in audit, nonzero elsewhere"
cases this window.

**A real, narrower gap was found while checking `agent_chat_messages` against
`AgentChatUserMessage` audit rows**, worth recording even though it isn't new
information:

| user | chat_rows (role=user) | AgentChatUserMessage audit rows | all msgs sent before/after 2026-08-14 fix |
|---|---|---|---|
| artempro2021@bk.ru | 33 | 0 | all before (2026-07-29..08-04) |
| good.win2283@gmail.com | 1 | 0 | before (2026-07-30) |
| macmam@atomicmail.io (farm-wave) | 10 | 0 | before (2026-08-08) |
| michaelharlam@yandex.ru | 12 | 12 | after fix (08-13 account, chat activity post-fix) |
| artempro2022@yandex.ru | 9 | 9 | after fix |

Every gap case predates 2026-08-14 (commit `bff73b02`), which is exactly the
fix date recorded in `project_audit_events_silently_drops_rows.md`
(`writeAuditRow` used to swallow non-FK-violation Postgres errors with a bare
`return`, now logs + counts via `dada_audit_write_failures_total`). Every
post-fix case (michaelharlam, artempro2022) is a clean 1:1 match. This is
**not** a live gap — it's corroborating evidence that the already-fixed bug
also cost real (non-farm) users their `AgentChatUserMessage` rows before the
fix landed, not just the farm-wave account (`pjx694168692@gmail.com`) that
memory previously (and correctly) walked back as explained by a DeleteProject
cascade instead. No backlog item filed for this — matches an existing,
already-resolved finding.

## Terminal-action distribution (last audited action per user, 27 users)

| action | users |
|---|---|
| SessionStart | 19 (18 are the farm-wave session-only accounts; 1 real user whose most recent event is a bare re-login after earlier product use) |
| ViewApps | 4 |
| DeployImageVersion | 2 |
| BuildFinished | 1 |
| ViewProject | 1 |

Restricted to the 9 real (non-farm) users, terminal actions are: ViewApps(4),
DeployImageVersion(2), BuildFinished(1), ViewProject(1), SessionStart(1) — no
single action dominates; sample too small (n=9) for a new mass-stall claim.

## First-action-after-signup distribution (27 users)

| action | users |
|---|---|
| SessionStart | 24 |
| CreateApp | 2 |
| RedeemPromo | 1 |

## Top transitions (action_A -> action_B), by transition count

```
SeedDatabaseDSN           -> SeedDatabaseDSN            168  (1 user)
SessionStart              -> ViewProject                139  (7 users)
ViewProject               -> ViewApps                   134  (9 users)
ViewProject               -> ViewProject                 89  (4 users)
ViewApps                  -> SessionStart                64  (6 users)
ViewApps                  -> ViewApp                     64  (5 users)
ViewProject               -> SessionStart                43  (5 users)
ViewApps                  -> ViewProject                 39  (6 users)
SessionStart              -> SessionStart                34  (8 users)
UploadSourceArchive       -> ViewBuildLogs                31  (4 users)
VerifyDomainAuthorization -> VerifyDomainAuthorization    31  (1 user)
ViewBuildLogs             -> BuildFinished                29  (5 users)
ViewApps                  -> ViewApps                     27  (4 users)
ViewApp                   -> ViewApps                     25  (5 users)
BuildFinished             -> DeployImageVersion           24  (4 users)
ViewApp                   -> UploadSourceArchive          24  (2 users)
RevealEnvVar              -> RevealEnvVar                 22  (2 users)
ViewApp                   -> SessionStart                 18  (3 users)
ViewProject               -> ViewApp                      17  (3 users)
SetEnvVar                 -> SetEnvVar                    16  (4 users)
DeployImageVersion        -> ViewProject                  16  (5 users)
DeployImageVersion        -> DeployImageVersion           15  (5 users)
ViewBuildLogs             -> ViewApps                     12  (4 users)
UpdateAppStorage          -> UpdateAppStorage              11  (1 user)
AgentChatUserMessage      -> AgentChatUserMessage          11  (2 users)
DeleteApp                 -> DeleteApp                     11  (2 users)
ConnectGitRepo            -> TriggerBuild                  11  (5 users)
ViewApp                   -> ViewProject                   11  (4 users)
DeployImageVersion        -> SessionStart                  10  (3 users)
TriggerBuild              -> ViewBuildLogs                 10  (6 users)
ViewProject               -> ViewBuildLogs                 10  (2 users)
```
(full 40-row table available in the query output; this is the head)

The graph reads as a normal console-usage loop for real users
(SessionStart -> ViewProject -> ViewApps -> ViewApp -> deploy/build cycle),
dominated by two power users (`4b1b8d89-...` / artempro2021, `d2ac0ab7-...` /
artempro2022) who account for most of the high-count edges.

## Concrete UX finding

**No new finding survives cross-checking against existing memory.** The two
candidate signals both resolve to findings already on record:

1. **67% of this window's "new users" are farm-wave bot signups whose entire
   audit trail is bare SessionStart pings** → matches
   `project_signup_farm_wave_pollutes_funnel.md` exactly (same window,
   confirmed with individual timestamp checks this cycle).
2. **A handful of pre-2026-08-14 chat sessions (including 2 real, non-farm
   users) have `agent_chat_messages` rows with no matching
   `AgentChatUserMessage` audit row** → matches
   `project_audit_events_silently_drops_rows.md` (bug fixed in `bff73b02` on
   2026-08-14; every post-fix case in this cohort is a clean 1:1 match,
   confirming the fix holds).

## Backlog

**Nothing filed.** Both signals found this cycle are corroboration of
existing findings, not new ones:
- Farm-wave dominance of the 30-day new-user window → cite
  `project_signup_farm_wave_pollutes_funnel.md`.
- Pre-fix AgentChatUserMessage audit gaps for 2 real users → cite
  `project_audit_events_silently_drops_rows.md` (already fixed 2026-08-14,
  post-fix data in this same cohort confirms the fix).

## Structural audit gap (task item 5)

No *currently live* structural gap was found for `AgentChatUserMessage` —
every message sent by a real user after the 2026-08-14 fix has a matching
audit row (verified: michaelharlam 12/12, artempro2022 9/9).

The one gap that **is** structural and current by design, not by bug: only
the user's own chat turn is audited (`AgentChatUserMessage`, single call site
`backend/internal/api/agent_chat.go:1374`, immediately after the message
insert at `agent_chat.go:1373`). The assistant's answer and any tool calls
the agent makes in the same turn are written to `agent_chat_messages` (roles
`assistant`, `tool`) but have **no audit_events counterpart at all** — see the
insert call sites at `agent_chat.go:286` (assistant) and `agent_chat.go:284`
(tool), neither of which is followed by any `recordAudit*` call. This is
intentional per the comment at `agent_chat.go:428-434` ("only approve and
decline ever reached audit_events" before this fix, extended to cover the
user's own message but not the model's response). If "did the assistant
actually answer, or error out silently" ever needs to be path-analyzable from
audit_events alone, the insert helper for the terminal `assistant` message at
`agent_chat.go:1435` (and the confirm-flow one at `agent_chat.go:1851`) is
the nearest call site to add a matching audit row — no such row exists today.
