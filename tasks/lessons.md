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

## 2026-07-31 — Two thin-content mistakes: measuring the source list, and trusting the editor over the DOM
Context: expanding the pages Yandex was dropping as `LOW_QUALITY`. Two of my own claims were wrong before I checked them against what actually ships.
First, I concluded pages were thin from their source copy. Several were long in the editor and thin to a crawler because `FaqList` mounted the answer paragraph only for the open accordion item — the FAQ prose lived in React state and never entered the DOM. One `hidden`-class change lifted every FAQ-bearing landing at once (`/analog-vercel` 700 -> 1137 words).
Second, I reported "62 sitemap routes, ten under 700 words" from a sweep built off the static path list in `frontend/app/sitemap.ts`. That file also enumerates `/developer/*` from the content directory via `getDocSlugs()`, so the real sitemap is 94 URLs and 38 are under 700 words. I had shipped that wrong number into a commit message before catching it.
Root cause: both are the same error — measuring an input to the artifact instead of the artifact. Source copy is an input to the rendered DOM; a static array is an input to the generated sitemap.
Rule: when the consumer is a crawler, a client, or any external system, measure what that consumer receives — fetch the deployed URL, parse the served HTML, read the generated sitemap. Never derive a content or coverage metric from the source that produces it. A page can be long in the repo and empty on the wire.

## 2026-07-31 — A dashboard reading zero is not a result until you check its date

The Search Console overview said "Всего 0 кликов в веб-поиске" and the honest first
instinct was to treat it as the verdict on the whole Google push. It was not. The chart's
last data point was 28 July; the sitemap was submitted on the 30th. The screenshot could not
have contained the answer, because the period it covers ends before the thing being measured
started.

The actual evidence was in the ingress logs, not the dashboard: Googlebot's traffic changes
character at 30 July 17:17 — before it, only spoofed scanners probing `/config.json`; after
it, real fetches of `/`, `/analog-railway`, `robots.txt` and a `_next/static` CSS chunk. The
stylesheet is the tell: fetching CSS means the rendering service, not the raw fetcher. That
falsified my own earlier finding that real Googlebot had never touched the host.

**Rule.** Before reading a metric as a result, check that its window covers the change. A
number from before the intervention is not a measurement of the intervention. And when a
dashboard and a log disagree, the log is the artifact — it records what happened, the
dashboard records what has been aggregated so far.

**Second rule, same day.** When a fix spans two layers that both redirect, ship them in an
order where they are never both active, and say so in the first commit message. Here the
frontend half went out deliberately inert and the ingress half followed once it was live in
prod — verified by the image tag being a descendant of the fix commit, not by the clock.

## 2026-07-31 — A unit test on a pure selector cannot see a DOM race

The first-deploy onboarding campaign shipped with six unit tests, a cross-language sync test,
and live proof on prod that the API round-trips. All of it green. The tour still never
rendered for a real user: campaign selection ran once, the moment `GET /onboarding` resolved,
and at that instant the deploy hero had not mounted yet — it waits on four project data
calls — while the agent FAB, being part of the shell, was already in the DOM. So the
fallthrough branch that the unit test asserts as correct behavior ("a project that already
has an app falls through to agent") fired on every load, on empty projects too.

The tests were not wrong; they were answering a question that assumes a settled DOM. The bug
lived in *when* the question was asked, and only a browser could show that. I found it two
minutes after the probe page rendered, by reading the tooltip title in a screenshot.

**Rule.** If a feature's behavior depends on an element existing at a particular moment, the
verification must be a real page load, not a function call. `hasTarget: () => true` in a test
is a claim about timing, and the test cannot check it. Ask: *at the instant this code runs,
what is actually in the DOM?* Anything that mounts after data loads is not there yet.

**Second rule.** Ship-verification that only proves the API accepted the write ("POST 200, row
round-trips") proves the backend, not the feature. I wrote "still unverified: the joyride
spotlight itself" in the notes and then treated the feature as shipped. If a gap is worth
writing down, it is worth closing before the next thing — the gap was the whole feature.

## 2026-07-31 — A 404 on a bare brand domain can be a reservation, not a bug

I found `dada-tuda.ru` answering 404 behind the ingress controller's self-signed certificate,
read it as an obvious defect, and claimed the host for the cloud's marketing landing. The
domain was earmarked for a different product. Nobody asked me to consolidate the brand apex;
I inferred it from "the bare domain is broken."

The measurements I took were all correct and all beside the point. Zero pages in Yandex
search, `MAIN_PAGE_ERROR` since 07-22, no other Ingress claiming the host — every one of
those says nothing was *displaced*, which is a different question from whether the host was
*mine to take*. Empty is not the same as unclaimed.

**Rule.** A bare brand domain is a product decision, not an infrastructure one. Subdomains
are fair game inside the platform's own zone; the apex and `www` are the brand's front door.
Before claiming one, ask what is supposed to live there — and if the answer is "nothing
yet," that is still an answer someone else gets to give.

## 2026-08-02 — A page-wide grep is not a deploy check, and a pipe hides the exit code

Two ways I told the user something was true while it was not, in the same hour.

**The deploy marker.** I waited for the RU docs to reach prod by polling
`curl … | grep -q "Объектное хранилище"`. It matched immediately — because that
string is in the nav and the footer of every page, including the old English one.
I announced the deploy was live and started submitting URLs to IndexNow while
prod was still serving `<title>Object Storage (S3-compatible)</title>`. The
marker has to be a string that exists *only* in the new version and only in the
place the change lives: `<title>`, a version stamp, a build id. "The word appears
somewhere on the page" is not evidence that the page changed.

**The swallowed exit code.** `if python3 submit.py … | tail -6; then echo OK; fi`
tests the exit status of `tail`, which is 0 no matter how the script died. It
printed "indexnow OK attempt 1" over a Python traceback that was right there in
the same output. Redirect to a file and test `$?` on the command itself, or set
`pipefail` — never let a formatter be the last stage of a pipeline you branch on.

**Rule.** Both failures share a shape: I checked something adjacent to the claim
instead of the claim. Before trusting a probe, ask what it would print if the
thing had *not* happened. If the answer is "the same thing," it is not a probe.

## 2026-08-02 — The fallback that never fails is the one that stays broken

The docs served English when a Russian translation was missing. Nothing errored,
nothing 404'd, the page looked complete — it was just English prose inside a
`lang="ru"` document, invisible in review and unrankable in the Russian SERP.
That is how the entire `/developer` tree stayed unfound for a query like "s3".

Graceful degradation removes the symptom, so the only thing left that can notice
is a check that knows the rule. Fixed the content, then checked in
`frontend/scripts/check-doc-translations.mjs` wired into `prebuild` (verified in
reverse: hid `ru/builds.md`, got rc=1 naming the file).

**Rule.** When you add a fallback, add the guard in the same change. Otherwise
you have not built resilience, you have built a blind spot with good manners.
Corollary: put the guard where the project's trusted signal already runs — mine
went to `prebuild` rather than `test:unit` because CI builds on node 20, which
has no `--experimental-strip-types` and therefore never runs `test:unit` at all.

## 2026-08-02 — A control loop that only decides has not shipped

The vertical autoscaler measured pressure correctly, picked correct envelopes,
logged them and wrote audit rows. It resized nothing. Nine `AutoscaleApp` audit
rows on prod: two successes from an earlier code path, seven refusals — five
`unsized_app`, then, after the ladder was removed, four starving apps refused
across three ticks. The owner's verdict was one line: "recommender-only —
полная хуета." It was right.

The failure was not in the decision, it was in every step after it. The write
path went through `DeployImageVersion`, which regenerates values.yaml from the
database; for a hand-maintained app the database holds almost nothing the file
holds, so the render dropped env/volumes/serviceDatabase and the agent's clobber
guard refused the operation. Both the decision and the guard were correct, and
the loop still did nothing.

**Rule.** For any actuator, verify the ACT step against production evidence, not
the decide step. "The logs show it chose to grow" is not the same claim as "the
app got bigger" — go find the commit, the rollout, the new limits. Until you
have those, an autoscaler is a logger.

**Corollary.** When an operation must not disturb what it is not changing, patch
the artefact instead of regenerating it. `ResizeApp` rewrites six scalars inside
the existing values.yaml via a yaml-node round-trip; on the real 244-line
internal-prod/gateway file the diff is exactly three lines and every comment and
block scalar survives. Nothing is re-derived, so nothing can be lost, and there
is no guard left to trip.

## 2026-08-02 — Уведомление имеет смысл только там, где от юзера нужно действие

Автоскейлер после каждого резайза слал письмо владельцу: вверх — «увеличили
ресурсы», вниз — «уменьшили ресурсы». Оба письма были написаны аккуратно и оба
были спамом. Вердикт владельца: «для пользователя это должна быть магия, а не
спам писем — дали вам ещё гиг рама».

Ошибка в рассуждении была такая: раз платформа меняет чужой апп, надо об этом
сообщить. Но размер аппа юзер не задаёт, задать не может (пресеты убраны) и
повлиять на решение не может. Письмо про то, на что нельзя повлиять, не
информирует, а тревожит — в первую очередь наводит на мысль про счёт.

**Правило.** Прежде чем слать уведомление, ответь: что получатель сделает,
прочитав его? Если ответ «ничего» — это не уведомление, это шум. Успешная
автоматика молчит: log-строка и audit-запись для нас, тишина для юзера. Письмо
остаётся ровно там, где автоматика сдалась и дальше нужен человек, — у нас это
`notifyCeiling`: апп голодает на потолке, платформа ничего не меняла, чинить в
своём коде.

## 2026-08-02 — «Архитектурно недостижимо» — это гипотеза, пока не проверена на кластере

Автоскейл раздавал ресурсы через коммит в values.yaml. Изменение pod-шаблона
Deployment всегда катит новый ReplicaSet, то есть ответом платформы на «твоё
приложение душит throttling» был рестарт этого приложения под нагрузкой.

В памяти у меня лежала запись, почему иначе нельзя: «ресайз на уровне пода git
вернёт следующей синхронизацией». Записал я её, ни разу не потрогав ни субресурс
`resize`, ни дерево ресурсов Argo. Обе половины оказались ложными: Argo ведёт
Deployment/Service/Ingress/ConfigMap/PublicApi и не ведёт поды, откатывать
нечего; ресайз пода прошёл на живом кластере с тем же uid и restartCount 0.
Проверка стоила двух `kubectl` в одноразовом неймспейсе — меньше, чем ушло на
написание объяснения, почему проверять незачем.

**Правило:** отказ от подхода со словами «в этой модели недостижимо» — гипотеза
и обязан быть помечен как гипотеза, пока рядом не лежит замер. Уровень пруфа для
отказа тот же, что для утверждения, что фича работает. Дороже всего то, что
такая запись живёт в памяти и на следующем витке экономит мне ровно ту проверку,
которая опровергла бы её.

**Второе:** прежде чем писать свой контроллер, проверить не «есть ли CRD», а
крутится ли контроллер. В кластере лежали CRD VPA без единого его пода и без
единого объекта — вид «VPA стоит» при полностью мёртвом VPA.

## 2026-08-02 — Проверять свою правку на живом проде операцией, которая переписывает файл целиком

Проверял ресайз через `PATCH /api/v1/.../profile` на реальном приложении
`internal/telemost-bot`. Эндпоинт ставил в очередь `DeployImageVersion` — ту же
операцию, что и деплой образа. Агент честно перерендерил чарт из
`resource_snapshots.summary_json`, а база знает про приложение ровно три вещи:
образ, порт и ресурсы. `values.yaml` у бота вели руками: 128 строк ужались до
14. Ушли весь блок `.env`, объявление управляемой базы, ссылки на секреты
Postgres и Keycloak, порт 8000, стратегия recreate; `app.yaml` переехал с
`helm/python` на `helm/app`. Argo применил, бот поднялся без окружения.

`guardUnattendedClobber` не сработал — он смотрит только на операции системного
актора (`op.Unattended()`), а тут актор был человек (я). Премисса гарда — «живой
человек нажал Deploy, увидит результат и откатит» — не держится для эндпоинта,
чья работа менять два числа.

Побочный ущерб — самое дорогое: ререндер выпилил CR `ServiceDatabaseV2`. База и
роль выжили только потому, что у composed-ресурсов `deletionPolicy: Orphan`. Но
пересозданные MR-ы нашли роль уже существующей (Observe → up-to-date → Create не
запускается), а connection-secret provider-sql пишет ТОЛЬКО в Create. Итог:
`telemost-bot-db-credentials` с нулём ключей и под в `CreateContainerConfigError`
— «couldn't find key endpoint». Чинится не пересозданием MR (Observe снова
найдёт роль), а руками: `ALTER ROLE ... PASSWORD` от суперюзера +
`kubectl patch secret` четырьмя ключами `endpoint/port/username/password`.

**Правило 1.** Перед проверкой на живом объекте выясни, какую ОПЕРАЦИЮ дёргает
эндпоинт, а не какое поле он меняет. Полный ререндер (`DeployImageVersion`) и
хирургическая правка (`ResizeApp`) выглядят одинаково в API и по-разному в git.

**Правило 2.** Прод-приложение с рукописным `values.yaml` — не полигон. Полигон
— одноразовое приложение в своём неймспейсе; оно было создано и им же надо было
проверять.

**Правило 3.** Orphan спасает данные, но не спасает креды. После любого
воскрешения Crossplane-ресурса проверяй, что connection-secret непустой, —
провайдер уже не запишет его сам.

## 2026-08-03 — реконсайлер минтил креды поверх живых

Фаза 2 ADR-021: фоновый цикл раздаёт каждому k8s-аппу токен в его неймспейс.
Логика была «нет секрета → нет кредов → минти», и это правильно ровно для мира,
которого ещё нет. В сегодняшнем мире у reels-tracker токен лежит литералом в
argo-infra: строка identity живая, секрета нет. Первый же тик после выкладки
отозвал бы токен, которым бот аутентифицируется прямо сейчас, — тот самый слом
2026-08-02, только устроенный тем кодом, который его чинит.

Поймал не тест, а вопрос «а что этот цикл увидит на реальном проде», заданный
ПОСЛЕ пуша и ДО выкладки. Тесты были зелёные: они моделировали только новый мир.

**Правило 4.** Реконсайлер, который выдаёт креды, не имеет права читать «я этого
не вижу» как «этого нет». Отзыв — разрушительная операция; условие для неё
должно быть положительным («живых токенов ноль»), а не отрицательным («секрета
нет»). Невидимый живой токен логируется и не трогается.

**Правило 5.** Прежде чем новый фоновый цикл поедет в прод, выпиши его целевую
выборку запросом к ПРОДОВОЙ базе и глазами прочитай, что он сделает с каждой
строкой. Здесь это был один SELECT: одна строка, reels-tracker, и стало видно
всё.

**Правило 6.** Если окно между «плохой билд собран» и «хороший билд собран»
закрыть нельзя, обезвредь данные, а не код: секрет с ТЕКУЩИМ токеном, созданный
руками заранее, делает опасную версию безопасной — она его усыновляет вместо
минта. Дешевле гонки с Jenkins.

## 2026-08-03 — мой цикл уронил чужой тест, а зелёный прогон это скрыл

Тот же реконсайлер сломал сборки Jenkins #870-873. `TestOpenAPICoverage`
конструирует Handler с `&pgxpool.Pool{}` — пул, который никто не создавал,
внутри держит nil-puddle, и `Acquire` не возвращает ошибку, а падает в segfault.
`NewHandler` запускает фоновые циклы, так что тест, который вообще про
перечисление роутов, начал падать паникой в `runWithAdvisoryLock`.

Локально полный прогон был зелёный. Это ничего не доказывало: падение — гонка
горутины со временем жизни теста, и один зелёный прогон у неё в пределах шума.

**Правило 7.** Горутина, стартующая из конструктора, превращает КАЖДЫЙ тест,
который этот конструктор зовёт, в потенциальное место падения — даже тест, не
знающий о твоей фиче. Заводя новый фоновый цикл, найди всех, кто строит объект,
и проверь, что цикл переживёт их фальшивые зависимости (nil-пул — no-op, а не
паника).

**Правило 8.** Один зелёный прогон не закрывает гонку. Если новый код стартует
горутину, гоняй `-race -count=N` по пакету, где живут конструкторы, прежде чем
считать сборку доказанной.

## 2026-08-03 — «пусто в env» ещё не значит «апп не увидит»

Переводил telemost-bot с литерала на secretKeyRef и проверил результат пробой:
отдельный `python -c` в поде показал `GROQ_BASE_URL` пустым. Выглядело как мой
слом — переменная же была в .env, а я трогал соседнюю строку. На деле пусто было
у ПРОБЫ: апп зовёт `load_dotenv()` на импорте и тем самым сам заливает .env в
`os.environ`, а мой процесс этого не делал.

**Правило 9.** Проверяй так, как читает приложение, а не так, как удобно из
kubectl. Если апп грузит конфиг через load_dotenv/pydantic/Spring profiles,
воспроизведи ровно эту загрузку, иначе получишь ложный отрицательный и пойдёшь
чинить то, что не сломано.

**Правило 10.** У python-dotenv `load_dotenv(override=False)` по умолчанию —
переменная контейнера побеждает файл. Это и делает перевод на secretKeyRef
безопасным для dotenv-приложений, но проверять надо фактом, а не памятью:
обратный дефолт превратил бы тот же коммит в тихий откат на старый ключ.

## 2026-08-04 — «фича дорогая» почти всегда значит «сборщик не дописан»

Владелец получил от аналитика цифру «бокс-пул = 15.6% счёта при нулевом спросе» и
сказал прямо: я не просил жечь кластер ради непроданной фичи. Первым побуждением
было спорить про ценность фичи или резать её функциональность. Правильным
оказалось прочитать, ЧЕМ именно были эти 15.6%: 96% всех отмеренных минут —
`suspended_disk`, то есть диски боксов, которыми никто не пользовался. Это не
цена фичи, это цена невывезенного мусора. Дальше нашёлся один UPDATE:
`executeSuspendBox` ставил `status='Sleeping'` и не ставил `slept_at`, а сборщик
отбирает строки по `slept_at IS NOT NULL` — то есть выключал сам себя на каждом
боксе, который усыплял.

**Правило 11.** Услышав «фича дорого стоит», сначала разложи счёт по видам
расхода и найди тот, который никто не заказывал. Спор о ценности фичи — это
второй разговор, и он бессмыслен, пока в первом лежит утечка.

**Правило 12.** Статус и его отметка времени — один факт, и писать их разными
инструкциями нельзя. `SET status='Sleeping'` без `slept_at` — это не «забыли
поле», это строка, невидимая для всей логики, которая по этому полю фильтрует.
Заводя колонку-часы, найди ВСЕ места, которые ставят соответствующий статус, и
проверь их, а не только то, через которое ходил сам.

**Правило 13.** Сборщик мусора нельзя строить по возрасту и здоровью объектов —
только по владению: множество того, что контрольный слой ещё считает своим, и всё
остальное. И у такого прохода обязана быть блокировка на неполный ответ БД:
пустое множество из-за упавшего postgres неотличимо от пустого неймспейса, а
реакция на «ничего не занято» — снести всё живое.

**Правило 14.** Не вешай сбор мусора внутрь ветки успеха биллинга. Флот, за
который никто не платит, — ровно тот флот, который сильнее всего надо убирать;
связав их, ты превращаешь опечатку в прайс-файле в счёт, который растёт сам.

**Правило 15.** Квоту агенту режут не по действию, а по личности. Гейт на
«создать проект» уже стоял, а проекты всё равно плодились: любой
аутентифицированный вызывающий — неявный Owner орги, названной его же
username, поэтому сервис-аккаунт с грантом на один проект всё равно имел
собственную оргу и в ней полное право. Ограничивая автоматизацию, ищи не
эндпоинт, а неявный грант, который identity получает просто по факту
существования, и снимай его; иначе запрет обходится сменой org_id в теле
запроса.

**Правило 16.** «Уберись в проде» — это два разных дела: удалить мусор и
закрыть кран. Сделав только первое, вернёшься сюда через сутки, потому что
источник — не один забытый скрипт, а каждая новая сессия. Просьбу «свести
тестирование в один проект» выполняй как правило в конфиге агента плюс отказ
на стороне API, а не как разовую чистку.

**Правило 17.** «Консоль слабо знает про свой деплой» — почти никогда не
список багов UI, а один пробел в данных: слой, который единственный видит
правду (status-reconciler в кластере), пишет в снапшот агрегаты и выбрасывает
идентичность — реальные namespace и образы. Всё, что ниже по течению (логи,
метрики, размер, порт), потом честно ищет по неверному ключу и получает ноль,
а UI закрывает ноль дефолтом и выдаёт выдумку за факт. Чини у источника:
сначала запиши наблюдаемое, потом убери дефолты; «—» лучше правдоподобного
числа.

**Правило 18.** Матчер по лейблу, который ставит только твой собственный
чарт, слеп ко всему, что пришло в платформу иначе. Adopted-аппы (ADR-013) не
имеют `dada.io/app` вообще, поэтому логи у них были пусты всегда — включая
деплой самой консоли, то есть баг годами смотрел нам в лицо. Прежде чем
объявлять «логов нет», проверь, каким лейблом реально помечены поды в
индексе, а не каким они должны быть по чарту.

## 2026-08-04 — уборка за тестами удаляла родителя, а дети молча оставались в проде

`/admin/overview` отвечал 500 всем: агрегат сканил `resource_snapshots.phase` в
Go-строку, а одна строка держала NULL. Приехала она не из прода — её оставил
тест-сидер (`volume-export-test-8b671f4e`, 31.07) в ТОЙ ЖЕ базе `cloud-console`,
против которой Jenkins гоняет real-DB тесты.

Уборка выглядела правильной: `t.Cleanup(func() { _, _ = pool.Exec(ctx,
"DELETE FROM projects WHERE id = $1", id) })`. Но `db_backups`, `operations`,
`audit_events` и `resource_snapshots` ссылаются на `projects` через NO ACTION,
Postgres отвечает 23503, а `_, _ =` это глотает. Тест зелёный, строка вечная.

Первая моя правка тоже «сработала» и тоже не работала: я захардкодил список
блокирующих таблиц, а `DELETE FROM operations` сам блокируется
`audit_events.operation_id`, `deployments.operation_id`, `git_commits`,
`domain_hostnames`. Понял это только по счётчикам строк ПОСЛЕ прогона —
suite всё это время был зелёный.

**Правило 21.** Тесты пишут в прод-базу. Уборка, которая может тихо не
сработать, — это не «грязь в тестовой среде», а прод-данные навсегда. Чистить
только через `internal/dbtest` (`DropProject`/`DropUser` читают граф FK из
`pg_constraint` и идут вглубь), никогда — списком таблиц, который устареет на
следующей миграции.

**Правило 22.** Критерий приёмки для правки в уборке — не «suite green», а
одинаковые счётчики строк ПО ВСЕМ таблицам между свежей мигрированной базой и
базой после прогона (`query_to_xml` + `diff`). Именно это сравнение вскрыло
второй слой: 30 осиротевших `users` (их тоже держат `operations.actor_id` и
`audit_events.actor_id`) и таблицы БЕЗ внешних ключей вообще
(`payment_connections`, `payment_oauth_states`, `user_onboarding`,
`agent_chat_*`), до которых обход FK не доходит по определению — их надо
перечислять руками.

## Правило 17 — «противоречие в UI = два разных источника, найди оба, потом ищи, кто их рассинхронил»

Консоль одновременно показывала «1 прил., Ready, 8s ago» и «0 прил., нет
приложений» для одного проекта. Соблазн — объяснить это кешем или устаревшим
снапшотом. На деле оба виджета читали ОДИН эндпоинт, но для РАЗНЫХ окружений:
страница приложений агрегировала все, обзор брал первое `type='prod'` из списка
`ORDER BY name`, и там лежало воскресшее превью `pr-6-*`.

- Сначала выясни, какой запрос стоит за КАЖДОЙ цифрой, и только потом смотри в
  данные. «Стало быть, снапшот протух» — это гипотеза, а не диагноз.
- Выборка сущности по типу (`type === "prod"`) без явного ключа — мина. Если у
  проекта есть авторитетное поле (`default_environment`), бери его.
- Найдя кривую строку в БД, не чини строку — найди писателя. Здесь писателем
  оказался git-watcher, переигравший старые коммиты: гард `syncablePaths`
  выкидывает пути, удалённые В ТОМ ЖЕ коммите, а воскрешение шло через
  коммит-добавление, снос был позже и отдельно.
- Считай радиус: тот же `SELECT` показал ещё три проекта с той же ложью
  (`fin-core` брал `findata` вместо `prod`). Один скриншот — не один баг.

**Правило 19.** Периодическая уборка — это не фикс, это признание, что кран
открыт. Я закрыл P0 с забитым диском почасовым CronJob'ом, который подчищал
сессии Keycloak, и владелец забраковал это одной строкой: «постоянный фикс —
выключить в настройках». Он прав: между прогонами таблица всё равно росла на
160 MB/сутки, то есть я оставил в проде таймер, который надо вечно
обслуживать, вместо того чтобы убрать причину. Если предлагаешь reaper, cron
или ретеншен — сначала честно ответь, почему нельзя просто перестать
производить мусор.

Вторая половина того же правила: выключение фичи и остановка производителя —
это ДВА шага, и первый без второго опаснее исходной болезни. Сессии никуда не
делись от `KC_FEATURES_DISABLED`, они переехали с диска в heap (Keycloak при
этом сам снимает лимит кеша: `Ignoring cache limits to avoid losing
sessions`), и медленное заполнение тома превратилось бы в быстрый OOM
провайдера идентичности. Производителем оказался не человек, а машина:
crossplane-провайдер логинился password grant'ом на каждый reconcile.
Замерено на 26.3.3 — 50 `client_credentials` дают 0 сессий, 50 password grant
дают 50. Отсюда контракт: машина ходит в Keycloak только через service
account, и никогда — паролем.

Третье, попутное: когда чистка таблиц не вернула место (3279 MB при сумме всех
отношений 26 MB), правильная реакция — не «ну, значит, postgres так считает»,
а искать осиротевшие relfilenode после падения по ENOSPC. Авторитетная
проверка — `pg_filenode_relation(0, N)`; наивное сравнение с
`pg_class.relfilenode` врёт на mapped-каталогах, у которых там ноль. Удалять в
два шага: `mv` наружу, проверить живость, потом `rm`.

**Правило 20.** «Алерта на это нет» — это гипотеза, а не факт, и проверяется
одним запросом. Я закрыл разбор P0 фразой «том забился молча» и предложил
поставить сенсор, который уже стоял: правило было включено, метрика
скрапилась, `ALERTS` за окно инцидента показывает warning за 8.4 часа до
падения, а `alertmanager_notifications_failed_total` — ноль при 344
доставленных. Прежде чем добавлять телеметрию, спроси у Prometheus `ALERTS` за
время инцидента и счётчики доставки алертменеджера. Тишина в голове не равна
тишине в системе, и новый дубль в канал, который никто не читает, стоит
дороже, чем ничего: он добавляет шума в ту самую проблему.

Когда сенсор всё-таки оказался живым, вопрос смещается на порог и на внимание.
Порог чинится кодом: апстримный `KubePersistentVolumeFillingUp` требует
одновременно <15% свободного и predict_linear на 4 дня, а 15% от маленького
тома — это часы, а не дни. Внимание кодом не чинится: 121 уведомление в сутки
в один тред означает, что критичное сообщение неотличимо от фона, и решать,
что оттуда выкинуть, — не работа агента.

**Правило 21.** `for:` дебаунсит только восходящий фронт. Спад не дебаунсится
ничем: условие ушло под порог на один скрейп — алерт немедленно резолвится, и
следующее пересечение порога это уже НОВЫЙ алерт, а не повтор старого. Отсюда
два практических следствия. Первое: `repeat_interval` и silence такой поток не
глушат в принципе — они про повторы одного алерта, а здесь их много разных.
Второе: жалоба «спам в канале» почти никогда не решается мутом, и владелец был
прав, потребовав сначала понять флап. Замер делается одним `query_range` по
`ALERTS{alertstate="firing"}`: считаешь разрывы в серии и сравниваешь число
эпизодов с суммарным временем горения. 7 эпизодов на 0.9 часа — флап; 12
эпизодов на 12 серий при 35 часах — непрерывное горение, там демпфер не нужен.
Лечится `keep_firing_for` на уровне правила, ровно у тех, кого замер уличил, а
не веером.

**Правило 22.** Новое поле конфига, прочитанное в горячем пути, ломает не
прод, а тесты соседа. `agentChatModelFor` стал читать `h.cfg.AgentChatModelB`
— в проде `cfg` ставит `NewHandler`, и там всё живо, а шесть литералов
`&Handler{pool: ..., agentChatLLM: ...}` в `agent_chat_confirm_test.go` его не
ставили никогда, и весь `AgentChatConfirm` начал падать в nil pointer на
real-DB прогоне. Локально это не видно: юнит-тесты модели строят Handler с
`cfg`, а confirm-тесты гоняются только там, где есть база. Правило: добавил
чтение `h.cfg.X` в путь, который дергает хендлер целиком — прогреби
`rg '&Handler\{' internal/api` и убедись, что каждый литерал в этом пути несёт
`cfg`. Дешевле, чем узнать об этом из чужого коммита.
