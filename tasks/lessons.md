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

## 2026-07-27 — Two different columns both loosely called "cloud task id" burned a debug cycle

Mistake: queried `agent_token_usage WHERE platform_request_id LIKE 'ct-<X>-%'` using console's
`cloud_tasks.id` primary-key UUID as `<X>`, got 0 rows, nearly wrote up a NEW ledger-insert bug.
Real key is hub's own `meta["cloud_task_id"]` string (which equals console's `cloud_tasks.intent_id`
column, a DIFFERENT column than `cloud_tasks.id`). My own prior-session memory paraphrase said
"correlation=cloud_task_id else intent_id" and I read "cloud_task_id" as "the id column" without
checking which variable the source code actually meant — it was a hub-side dict key, not a
console-side DB column, and the two systems use overlapping names for different fields.

Rule: when a memory/summary names a correlation/join key by a short label ("cloud_task_id",
"run_id", etc), do not assume the label maps 1:1 onto a same-named DB column before building a
query — grep the SOURCE that actually constructs the value (here: `platform_request_id = f"ct-
{correlation}-..."` in `cloud_task_runner.py`) and confirm which upstream field feeds it. A 0-row
result against a self-derived key is not evidence of a missing row; it's evidence the key might
be wrong — check the key before writing up the absence as a bug.

## 2026-07-27 — "Green build" and "config file updated" both lied; only the pod's own start time was true

Three near-misses in one migration, all the same shape: a signal that LOOKS authoritative but sits
one layer away from the thing it supposedly proves.

1. Jenkins build SUCCESS for telemost-bot while the gitops write-back stage printed
   "деплой в Argo пропущен" and `exit 0` — `pythonPipeline(infra:true)` defaults `infraProject` to
   `example-project` and the app had moved to `internal`, so it looked for a values.yaml that does
   not exist. The app had not been auto-deployed since the move; every build stayed green.
   → A green build proves the pipeline ran, not that it deployed. Check for the tag-bump commit in
     argo-infra, not the build result.

2. `grep ai-gateway-service /app/.env` inside the pod returned a match, so I called the env applied.
   Wrong: `.env` is mounted from a ConfigMap with no checksum annotation, so it updates IN PLACE on
   a live pod while the process keeps the env it read at import. Comparing `pod.status.startTime`
   (19:01:04Z) against the values push (19:04:49Z) showed the process predated the config.
   → File content ≠ process state. For anything read at startup, the authoritative check is the pod
     start time vs the config's commit time — then restart and re-verify.

3. A chat completion through the gateway returned 200, which I nearly took as "the app works".
   The same key then failed embeddings with `missing scope ai:embeddings` (gateway maps call_type →
   scope). One working call path says nothing about the others.
   → Exercise each distinct call type the app actually uses, in the pod, not one representative.

Bonus, same family: a NetworkPolicy egress rule naming the SERVICE port (80) matched nothing,
because policy is evaluated after the Service DNAT — the packet's destination is the POD port
(4000). NetworkPolicy denies by silent drop, so this presents as a TCP timeout indistinguishable
from the destination being down, with no "policy" string in any log. Isolate it by probing from a
FRESHLY created pod (rules out conntrack) and directly against the pod IP (separates DNAT from
policy).

Rule: for every "it works now" claim, name the layer the evidence came from and ask what sits
between that layer and the behaviour. Build result → deploy commit. Config file → process start
time. One endpoint → every endpoint the app calls.

## 2026-07-28 — A config key present in the pod's .env is not a config key the app read

Migrating profi-backend onto the AI Gateway, I changed `OPENAI_BASE_URL` in the app's
`envFileValue` and treated that as the redirect being done. It was not. `app/config.py`
declares its pydantic-settings model with `extra="ignore"` and never declares
`OPENAI_BASE_URL`, so the key was loaded out of `.env` and thrown away, and the code's
`getattr(settings, "OPENAI_BASE_URL", None)` could only ever return `None` — `ChatOpenAI`
then resolved the openai SDK default. Shipped alongside a new platform key, this would
have sent an `sk-dada-` token to `api.openai.com` and 500'd every KP-from-brief call. A
pod did go Ready in that state before I caught it.

What made it invisible: every proxy signal agreed with me. The values commit was pushed,
Argo synced, the ConfigMap contained the key, `grep OPENAI_BASE_URL /app/.env` in the pod
matched. Four green lights, and the process still had `None`.

Rule: for anything consumed through a settings/config layer, the authoritative check is
reading the value back **through that layer inside the process** — `hasattr(settings, X)`
and the effective value on the constructed client — not the presence of the key in the
file the layer read. Config layers with `extra="ignore"` (pydantic-settings, viper, most
schema-validated loaders) silently discard undeclared keys, so "the key is in .env" and
"the app is configured" are different claims. Ask which layer the evidence came from and
what sits between it and the behaviour: file → loader → object → client.

Corollary, same session: one 200 is not coverage. A plain chat completion through the
gateway succeeded while the app's real traffic is tool-calls plus structured output. Both
had to be exercised through the app's own `KPAgent._build_llm()` in the pod before the
migration could be called verified — and the authoritative signal was the
`agent_token_usage` row, not the HTTP status.

## 2026-07-28 — "Same egress as the gateway" can't carry Instagram: TSPU SNI-throttle lives on the cluster leg, not the proxy
Context: reels-tracker Instagram broke with `407 Proxy Authentication Required` (old proxy 161.0.5.119:8000 dead). Owner asked to point it at the SAME proxy ai-gateway uses (tinyproxy 83.222.23.85:8888). Before swapping, probed from the ai-gateway pod: generic HTTPS (google) 200; through the proxy `CONNECT` tunnel establishes in 0.4-0.7s for EVERY host, but the inner TLS handshake stalls to a 20-45s timeout for `www.instagram.com` + `scontent.cdninstagram.com` while `facebook.com`/`graph.facebook.com`/`instagram.com` apex complete in 0.4s. Same VM curls Instagram directly in 0.96s and via its own local tinyproxy in 0.84s.
Root cause [live]: NOT proxy auth, NOT PMTU/size (large Meta facebook certs pass), NOT egress IP (VM and WG both SNAT to 89.169.36.109). It is SNI-specific DPI/throttle (Russian TSPU) on the CLUSTER->proxy leg: the HTTP proxy relays the ClientHello SNI in cleartext, so the throttle fires on `www.instagram`/`cdninstagram` regardless of proxy destination. The VM's own egress isn't throttled, so VM-local works.
Consequence: swapping to the gateway's proxy would turn a 407 into a silent 20s timeout — a non-fix that would have "passed" a naive `curl google` smoke test. The gateway proxy works for LLM APIs only because those SNIs aren't on the throttle list.
Rule: when an HTTP(S)-proxy "works for some sites, hangs for others," stage-split the probe (CONNECT-tunnel time vs inner-TLS-handshake time) and test a same-size control host (facebook for instagram). If CONNECT is instant but only specific SNIs stall, suspect on-path SNI DPI, not the proxy. Fix requires an ENCRYPTED hop (WireGuard/TLS-fronted proxy) to the egress box so the SNI never crosses the DPI in cleartext — an auth/proxy swap can't fix an SNI throttle. Verify on the authoritative signal (real target host TLS), never on `curl google`.

## 2026-07-29 — Two framework detectors, one product claim: verify the path the user will actually take
Context: writing the `/hosting-streamlit` landing I found `backend/internal/sourcedetect/detect.go` mapping `streamlit` to port 8501 and wrote copy promising "the platform detects Streamlit and picks the port for you." Checking the other side proved it false for the advertised flow: `build-agent/internal/server/server.go` (`detectPythonFramework`, `detectPythonLockfileFramework`, `frameworkDefaultPort`) knows only fastapi/django/flask, and `sourcedetect` has exactly one consumer — `backend/internal/api/uploadsource.go`, the zip-upload path. Git builds, which the landing's own how-to steps describe, never see it.
Root cause: two independent detectors with overlapping names and diverging framework tables. Grepping for a symbol found the one that agreed with the claim I wanted to make.
Rule: before a landing, doc or release note asserts a product behaviour, trace the code path the copy tells the reader to walk, end to end, and confirm the claim on THAT path. Finding one function that supports the claim is not evidence; find the caller. When two code paths disagree, say which is which in the copy ("on zip upload the port is detected; for a git build add a Dockerfile") rather than picking the flattering one.

## 2026-07-30 — Owner killed the email channel: fix the product, do not write the user a letter
Context: over several cycles the routine kept producing drafts of letters to live users (volume filled up, source archive lost) and parking them in `state/drafts/` behind an owner-approval ask. Owner, verbatim: "удали письмо - не надо ничего отправлять / если ui покажет алерт значит покажет / если нет - нет и надо ЧИНИТЬ / хватит долбить письмами."
Root cause: I treated a letter as the deliverable for user feedback. A letter is a one-shot compensation for a product that failed to show the user their own state, and it needs a human to send it, so it also parks the work.
Rule: user feedback is closed by a feature or a fix in the UI, never by correspondence. If the user had to write to us to learn the state of their own app (disk filling, crashloop, failed deploy, expiring plan, lost source), the defect is that the console did not show it. Do not draft letters to live users and do not add owner-actions asking to send one. The one legitimate output is a visible, actionable state in the product.

## 2026-07-31 — The advertised path was never walked: dogfood the landing's exact promise before buying traffic
Context: about to seed paid Telegram posts pointing at `/hosting-telegram-bot`, whose promise is "drop a folder, no git, no Docker." Uploaded a real aiogram bot through the console as an ordinary user would. The build failed outright: `framework '' has no template and repo ships no Dockerfile`. Every archive upload that did not ship its own Dockerfile had been failing.
Root cause: three independent gaps on one path, each fatal alone. build-agent forwarded `detected_framework` to Jenkins only for `provider == "github"` (the detection reads a live GitHub checkout), so the upload-time detection — already persisted in `git_repos.framework_override` — was never used. The two detectors' framework vocabularies had drifted (`next`/`react-scripts` versus the pipeline's `nextjs`/`react`), so even a forwarded Node upload would have failed. And no detector had a name for plain Python, which is the exact shape of a Telegram bot. On top of that, the pipeline's python template hardcoded `python app.py`, so a bot in `bot.py` would have crashlooped after a green build.
Why nothing caught it: the gap lies BETWEEN two repositories (dada-cloud and jenkins-pipelines). No component test on either side can see both, and the July 29 lesson below had already flagged the diverging-detector class — it was recorded as a copy problem and not chased to the build path, so the same class stayed live and turned fatal.
Rule: any landing page that makes a concrete product promise must be exercised end to end, as a user, along exactly the path the copy describes, BEFORE spending on traffic. Paid seeding into an unverified flow pours people through a broken door. And when two repos share a vocabulary with no compiler between them, the divergence surfaces only as a stranger's failed build — pin the contract in a test on the side that owns the names.
