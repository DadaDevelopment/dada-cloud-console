# Audit path graph — users created last 30d / 7d

Source: `audit_events` LEFT JOIN `users`/`builds`/`git_repos`/`agent_chat_messages`/`feedback`,
**live psql** this cycle (sess-0821, prev 4 cycles were network-blackout `unmeasured` — see history
below). `eval "$(bash state/ensure-proxy.sh)"` → `DIRECT-OK`, `kubectl -n databases port-forward
pod/pg-shard-0-postgresql-0 15432:5432`, creds from secret `argocd-prod/dada-cloud-console-backend`
(`DB_URL`). Window: `now()=2026-08-21 11:11 UTC`.

## 0. Volume [live psql]

| metric | 30d | 7d |
|---|---|---|
| new users (`users.created_at`) | 29 | 2 |
| audit_events rows | 5782 | 2902 |

29→2 is not a funnel signal by itself — see §1, most of the 30d cohort is a single-day bot wave.

## 1. New users, ordered chains [live psql]

Full per-user chain pulled for all 29 (30d) and both 7d users. Two representative chains, in full:

**kkartov@yandex.ru** (signed up 08-17 19:46, `yandex.ru` source) — real dev session, then a
concrete failure-triggered drop:
```
08-17 19:46:33 SignUp -> SessionStart -> CreateProject -> ViewProject -> ViewApps
08-17 20:46-20:53 second session: StartGitAppInstall(github) -> ConnectGitRepo(instatic) ->
  TriggerBuild -> ViewBuildLogs -> ... (build path continues, 11 builds total, live loop-11)
08-18 21:09:07 InstallSolution(instatic-il1cvo) success
08-19 04:10:54 InstallSolution(homepage-9v9zuk) FAILURE metadata {"reason":"env_failed","status":500}
08-19 04:11:29 InstallSolution(homepage-rkbt3o) FAILURE {"reason":"env_failed","status":500}
08-19 04:11:38 InstallSolution(homepage-ts9on6) FAILURE {"reason":"env_failed","status":500}
08-19 19:26:37 SessionStart -> ViewProject -> ViewApps   <- LAST EVENT, ~40h ago, no return since
```
Three `InstallSolution` failures 44 seconds apart, same `env_failed`/500 reason, then user comes
back once more just to look (`ViewApps`) and never triggers another action. Terminal = passive
look after a broken feature, not an explicit quit.

**lifecoachrussia@yandex.ru** (signed up 08-19 09:37, `yandex.ru` source) — struggled but
succeeded:
```
09:37:20 SignUp -> SessionStart -> CreateProject(pending) -> ViewProject -> ViewApps
09:38-09:39 StartGitAppInstall -> FinishGitAppInstall -> CreateProject(success) ->
  ConnectGitRepo(gulyaev-ai-core) -> TriggerBuild
09:39:48 BuildFinished FAILURE
09:40:05 TriggerBuild -> 09:40:23 BuildFinished FAILURE
09:40:28 TriggerBuild -> 09:40:43 BuildFinished FAILURE
09:40:57 ViewApp -> 09:41:19 TriggerAutofix -> 09:41:32 TriggerBuild -> 09:41:48 BuildFinished FAILURE
11:26:50 BuildFinished SUCCESS -> CreateApp SUCCESS
08-20 07:16:40 BuildFinished SUCCESS -> DeployImageVersion SUCCESS   <- last event
```
4 consecutive build failures, one `TriggerAutofix`, then success ~1h45m after signup, and a
second deploy the next morning. This is the one live "made it through onboarding" story in the
7d window.

Remaining 27 chains in the 30d cohort: see §2 — 17 of them are a single-action bot wave, not
real usage.

## 2. Zero-activity / instrumentation-gap cross-check [live psql]

Cross-checked every 30d user with `audit_rows <= 1` against `agent_chat_messages.user_sub`
(= `users.id`, per known trap) and `feedback.user_sub` (= `users.keycloak_sub`, different column
semantics, checked separately):

| email | audit rows | chat msgs | feedback | verdict |
|---|---|---|---|---|
| `17ffb57d-...@keycloak.local` | 1 (`AgentChatActionDeclined`) | **179** | 0 | **instrumentation gap** |
| bestmanskyline@gmail.com, chenlikun.18@gmail.com, clikuoo@gmail.com, dmimuser@outlook.com, dsoftru@yandex.ru, game@016818.xyz, grwang1201@outlook.com, langhakka9527@gmail.com, mail@ynotu.top, oddessc@outlook.com, zengqcyxx@gmail.com, zhisibi@163.com | 1 (`SessionStart` only) | 0 | 0 | **dead signup, confirmed** |

The 12 "dead signup" accounts are real: zero rows anywhere across all four tables, single
`SessionStart` action, e-mail domains scream throwaway (163.com, atomicmail.io, random top-level
domains) — this is the 2026-08-08 19:49-22:56 wave (17 accounts total, 35 `SessionStart` rows,
0 builds/repos across the whole wave) already flagged in memory
`project_signup_farm_wave_pollutes_funnel.md`. Confirmed again live, numbers unchanged in kind.

The one instrumentation-gap case is new and concrete: user `17ffb57d` ran **179 agent-chat
messages in a 90-second window** (08-03 21:56:03 → 21:57:33 — one intensive agentic build
session) and it produced exactly **one** `audit_events` row (`AgentChatActionDeclined`,
resource `connectGitRepo`). Everything else the agent did in that session — tool calls, file
edits, whatever it attempted — left no audit trail at all. This is not a dead user; it's audit
coverage that stops at the agent-chat boundary.

## 3. Transition graph, first/terminal action [live psql]

First action after signup (30d cohort, distinct-on-min):

| action | users |
|---|---|
| SessionStart | 19 |
| SignUp | 5 |
| CreateApp | 2 |
| AgentChatActionDeclined | 1 |
| CreateServiceDatabase | 1 |
| RedeemPromo | 1 |

Terminal action (last audit row overall, same cohort):

| action | users |
|---|---|
| SessionStart | 18 |
| ViewApps | 6 |
| DeployImageVersion | 3 |
| AgentChatActionDeclined | 1 |
| ViewProject | 1 |

18/29 (62%) terminal at bare `SessionStart` — mostly the farm wave (§2). Excluding those 12
confirmed-dead accounts, terminal state is healthier: 6 stopped at `ViewApps` (browsing, no
action — includes kkartov post-failure), 3 reached `DeployImageVersion` (shipped something).

Top action_A → action_B edges, 30d cohort, all events (`n`=edge count, `distinct_users`=how many
different users produced this edge):

| action_A | action_B | n | users |
|---|---|---|---|
| SessionStart | ViewProject | 174 | 9 |
| SeedDatabaseDSN | SeedDatabaseDSN | 168 | **1** |
| ViewProject | ViewApps | 161 | 10 |
| ViewApps | ViewApp | 68 | 6 |
| UploadSourceArchive | ViewBuildLogs | 31 | 4 |
| ViewBuildLogs | BuildFinished | 29 | 5 |
| BuildFinished | DeployImageVersion | 24 | 4 |
| ViewApp | UploadSourceArchive | 24 | 2 |
| RevealEnvVar | RevealEnvVar | 22 | 2 |

`SeedDatabaseDSN → SeedDatabaseDSN` ×168 for a single user is a retry-burst signature (same
shape memory already warns about for build success-rate: "raw rate inflated by onboarding
retry-bursts" — this is that pattern showing up in a different action). Not investigated further
this cycle; flagging so a future cycle doesn't re-discover it as new.

## 4. Money path since checkout fix 3d6379f9 (08-15) [live psql]

```sql
select id, org_id, status, amount_value, created_by_sub, created_at, paid_at, customer_email
from payments where created_at >= '2026-08-15' order by created_at;
```

| org_id | status | amount | created_at | customer_email |
|---|---|---|---|---|
| artempro2021@bk.ru | canceled | 2900 | 08-15 21:45 | artempro2021@bk.ru |
| dada | canceled | 990 | 08-18 12:42 | sandbox-test@dada-tuda.ru |
| dada | canceled | 990 | 08-18 13:24 | alexkekiy@dada-tuda.ru |
| dada | canceled | 990 | 08-18 13:24 | alexkekiy@dada-tuda.ru |
| dada | pending | 2900 | 08-19 21:01 | (empty) |

**Zero `succeeded` rows since the fix.** 4 canceled, 1 still pending. Two of the five are the
owner's own org (`dada`) doing sandbox/test checkouts, not real customers. One (`artempro2021`)
is a genuine external signed-up user's attempt — canceled, not completed.

Whole-table check (all time, not just post-fix): payments ever = 9 rows (7 canceled, 1 pending,
**1 succeeded**). The one success is dated **2026-07-25, before the fix**, org_id `dada` (the
owner's own org) — i.e. the metric-#1 answer is unchanged from before this cycle: **no external
customer has ever completed a payment**, fix or no fix.

Note: `payments.created_by_sub` is empty string `''` on **all 9 rows ever**, not just the recent
ones — this column is never populated by the checkout code path, so attribution by sub is
structurally impossible from this table; `customer_email`/`org_id` are the only usable keys.
That's a pre-existing gap, not a post-fix regression — flagging so it's not miscounted as new
breakage next cycle.

## 5. Backlog-relevant conclusions (each with an evidence-backed verdict)

1. **`InstallSolution` (template/solution gallery install) fails 73% of the time, 30d: 11
   failures / 4 successes.** Reason breakdown: `storage_quota_exceeded` ×5 (client-blocked,
   status 0), `env_failed` ×3 (server 500), `malformed_body` ×3 (client 400). kkartov hit the
   `env_failed`/500 variant three times in 44 seconds and never came back except to passively
   look. This is the single clearest "product broke, user left" chain in the whole window.
   Backlog candidate (≤100 chars): **"InstallSolution fails 11/15 (30d): quota/env_failed/malformed_body — user leaves after 3x"**

2. **Agent-chat sessions are invisible to `audit_events` except at the decision boundary.**
   User `17ffb57d` ran 179 chat messages in 90 seconds, audit recorded 1 row. Reinforces the
   standing `frontend/components/agent-chat-panel.tsx:796` finding from the prior cycle (bare
   "Rejected" label, no follow-up cue) with a concrete volume number this time. Backlog
   candidate: **"Agent-chat actions (179 msgs) leave ~1 audit row — coverage stops at decision events, not tool calls"**

3. **Money metric #1 is still zero.** No external customer has ever completed a payment,
   before or after the 08-15 checkout fix (1 succeeded row ever, and it's the owner's own org,
   pre-fix). Confirms the standing hypothesis that checkout completion, not just the "pending
   row without payment" bug, is the open gate — the fix closed one failure mode but conversion
   is still 0/9 lifetime, 0/5 since. This is a **kill/adjust signal for any backlog item that
   assumes the checkout fix alone unblocks revenue** — it removed a false-negative, it did not
   yet produce a true-positive.

4. **The farm wave (08-08, 17 accounts, 35 `SessionStart` rows, 0 downstream activity) is
   confirmed again, unchanged in shape from the prior sighting.** No new action needed beyond
   what memory `project_signup_farm_wave_pollutes_funnel.md` already recommends (exclude from
   funnel denominators).

## Network status this cycle

Live, not blocked. `route -n get 155.212.223.198` showed `utun6` in path (VPN active),
`ensure-proxy.sh` returned `DIRECT-OK` (default route reaches the API without the bypass
proxy), `kubectl get nodes` returned 4/4 Ready immediately. Port-forward to
`pg-shard-0-postgresql-0` (ns `databases`) plus `DB_URL` from secret
`argocd-prod/dada-cloud-console-backend` gave direct psql access to `cloud-console`. Prior 4
consecutive blackout cycles (sess-0820j/k/m/that-run) are preserved below for continuity.

---

## Prior cycle (sess-0820, unmeasured — network blackout, kept for history)

Source intended: same as above. That cycle's DB was unreachable — 4th consecutive attempt that
day. `route -n get` showed `en0` direct (no utun), `ensure-proxy.sh` refused ("ОБА ПУТИ МЕРТВЫ"),
TCP to prod LB/both k8s APIs all timed out while `1.1.1.1` control target was open, and
`probe-external.sh` confirmed prod alive from 6 external vantage points. Diagnosed as
local-machine-to-RU-hosted-infra routing failure, not a product incident. Remote MCP
(`listProjects`) worked as a partial substitute but could not answer any of the five audit
questions (single action type, no sequence data) — explicitly not counted as a result then.
Backlog candidates raised that cycle (still open, not re-verified this cycle beyond what's
in §5 above):
- "Rejected label gives no cue a follow-up message is coming — user thinks turn ended" (agent-chat-panel.tsx:796, untouched since 08-07)
- "No SQL fallback when agent's RU-egress dies; MCP-only duties survive, audit-analysis can't"
