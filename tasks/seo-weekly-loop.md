# SEO weekly loop — baseline 2026-07-29

## Measured baseline (evidence)

| Metric | Value | Source |
|---|---|---|
| URLs in sitemap | 84 (42 paths x ru/en) | `curl /sitemap.xml` [live] |
| Pages in Yandex index | 12 (+50% w/w) | Webmaster screenshot [owner] |
| Index coverage | 14% | derived |
| Impressions (week) | ~36 across top pages | Webmaster [owner] |
| Clicks (week) | 3 | Webmaster [owner] |
| CTR on ranking pages | /analog-vercel 2/28 = 7%, /analog-railway 1/8 = 12% | Webmaster [owner] |
| Google presence | none found | `site:` search returned nothing on-domain [weak] |
| Users total | 29 | DB `users` [live] |
| Signups by week | 07-13: 14, 07-20: 5, 07-27: 1 | DB [live] |
| Projects total | 27 | DB [live] |

## Defects found (blocking indexation, not content)

1. **6 landings are orphans** — zero inbound internal links from any sampled page:
   `/deploy-without-git`, `/hosting-fastapi`, `/hosting-flask`, `/hosting-django`,
   `/hosting-streamlit`, `/hosting-vk-bot` (12 URLs with `/en` mirrors = 14% of sitemap).
   They are absent from `lib/i18n/dict.ts` footer lists and from `MARKETING_PATHS`
   in `components/marketing/footer.tsx`. Sitemap-only URLs on a zero-authority host
   rarely get crawled.
2. **Apex `dada-tuda.ru` + `www` serve the ingress fake certificate** — TLS error for
   humans and crawlers, no redirect to `cloud.dada-tuda.ru`. Brand queries land on a
   browser warning; link equity to the apex is lost.
3. **Thin-page cliff.** Pages producing impressions are 750-930 words; every page with
   no measured impressions is 300-360 words. Cross-page boilerplate is only 109
   shingles, so the problem is thinness, not duplication.
4. **Attribution blind.** `/register?utm_source=pseo_*` is set on landings, but the utm
   is never persisted (`users` table has no source column) and no Metrika goal fires on
   the landing CTA. Per-page signup attribution is currently impossible outside Metrika.
5. **No `google-site-verification` meta.** IndexNow reaches Yandex + Bing only; Google
   has no submission path configured.

## Segment / JTBD hypotheses

| # | Segment | JTBD | Query cluster | Evidence | Verdict |
|---|---|---|---|---|---|
| S1 | Beginner bot builder | "get my bot running 24/7 without a server" | хостинг телеграм бота, бот 24/7, aiogram деплой | `/hosting-telegram-bot` is 825w and indexed; matches the custdev pivot finding | MULTIPLY |
| S2 | Vibe-coder with AI-generated app | "publish what the model wrote, no git" | как выложить сайт, деплой без git, залить проект | `/deploy-without-git` + `/deploy-vibe-coding` exist; the first is orphaned | FIX then test |
| S3 | Dev migrating off blocked foreign PaaS | "replace Vercel/Railway, pay in rubles" | аналог X, миграция с X | 7 pages live; best page = 28 impressions/week | STOP expanding |
| S4 | Python dev needing cheap RU hosting | "host Django/FastAPI cheaply" | хостинг django/fastapi/flask | 4 pages, all thin and orphaned | FIX, do not add siblings yet |

## Investment decision

- **Cut:** no new `analog-*` pages. Cluster ceiling is demonstrated — the best-ranking
  page in it earns 28 impressions/week.
- **Fix (free, highest leverage):** link the 6 orphans from footer + homepage hub;
  expand them from ~330w to 750w+ to match the profile of pages that actually rank.
- **Fix (infra):** issue a cert for the apex and 301 `dada-tuda.ru` + `www` →
  `https://cloud.dada-tuda.ru`.
- **Multiply:** build out the S1 bot cluster — the only page with both product fit and
  a real query volume signal. Two new pages per week, maximum.
- **Instrument:** Metrika goal on every landing CTA + persist `utm_source` at signup.

## Weekly practice (the loop)

Every Monday:
1. Pull Webmaster: pages in index, per-query impressions/clicks/position, crawl errors.
2. Pull Metrika: visits by landing page, goal completions by source.
3. Diff vs prior week. Classify each page: `ranking` / `indexed-no-traffic` /
   `not-indexed`.
4. Act: expand `indexed-no-traffic` pages, debug `not-indexed` ones (links first),
   clone the pattern of `ranking` ones into 2 adjacent queries.
5. Re-check the prior week's predictions — record hit/miss so the model of what works
   is calibrated, not vibes.

**Blocker for automation:** needs one Yandex OAuth token with `metrika:read` +
Webmaster read scopes. Without it every number above stays a manual screenshot.
Once supplied, `scripts/seo-weekly.py` writes `tasks/seo/<date>.md` automatically.

## Google / Bing

- Google ignores IndexNow. The only path is Search Console: verify the host, submit
  `sitemap.xml`, watch Coverage. Nothing else moves it.
- Bing Webmaster Tools imports from GSC in one click, and IndexNow already feeds Bing.
- Ranking in Google additionally needs external links; the apex-domain fix is a
  prerequisite for any link pointing at the brand root resolving cleanly.

### Why Google is at zero — measured, not assumed

Ingress access logs over the retained window (`ingress-nginx-pub-controller`, both
replicas) show the crawl split plainly:

| Crawler | Requests | What it fetched |
|---|---|---|
| YandexBot | 33 | `/`, `/developer`, `/en/status`, `/storage`, `/sitemap.xml`, `/robots.txt` |
| bingbot | 12 | `/robots.txt`, `/en/developer/*` |
| Googlebot (UA) | 6 | `/wp-config.php.bak`, `/.env.backup`, `/config.json`, `/signup` — all 404 |

Every request carrying a Googlebot user agent is a spoofed vulnerability scanner.
**Real Googlebot has never fetched this host.** `robots.txt` already advertises the
sitemap and allows everything, so this is not a blocking problem — it is a discovery
problem. Google finds new hosts through links, and there are effectively none.

That leaves exactly two levers, and only one is free:

1. **Search Console verification** (free, immediate). The verification meta tag now
   ships behind `GOOGLE_SITE_VERIFICATION`, so claiming the property is an env change
   on the deployment, no rebuild. The public sitemap ping endpoint Google used to
   accept was retired in 2023 — Search Console is the only remaining submission path.
2. **External links** (slow, manual). Nothing technical substitutes for this.

## Review — shipped 2026-07-29/30

| Defect | State | Evidence |
|---|---|---|
| 1. Six orphan landings | fixed | footer "Хостинг и деплой" column renders 18 landing links on live `/` and `/en` |
| 2. Apex serves fake cert | fixed | `dada-tuda.ru` + `www`, http and https, all 301 → `https://cloud.dada-tuda.ru`, terminal 200; cert `CN=dada-tuda.ru`, issuer Let's Encrypt YR1, valid to 2026-10-27 |
| 3. Thin-page cliff | fixed | live word counts 736-903 across ru/en (was ~330); FAQPage + HowTo JSON-LD on every page |
| 4. Attribution blind | partly fixed | `landing_cta_click` fires with `source` + `placement` (verified against a stubbed counter: `source=pseo_discord_bot`, `placement=hero`/`band`); `/register` fires `signup_started` and writes the `dada_src` cookie. **Not** persisted to the `users` table — still no source column. |
| 5. No Google submission path | unblocked | `GOOGLE_SITE_VERIFICATION` renders `<meta name="google-site-verification">` in SSR; token still needs minting in GSC |

New pages, live and in the sitemap (88 URLs, was 84): `/hosting-discord-bot`,
`/deploy-aiogram-bot` and their `/en` mirrors. Both submitted to Yandex (202) and
Bing (200) via `scripts/indexnow-submit.py`.

The Discord landing's core promise was measured before it was written: from the prod
cluster, `discord.com/api/v10/gateway` returns 200 and a real websocket handshake to
`wss://gateway.discord.gg` returns `op=10 heartbeat=41250`. The page says so.

### Predictions to grade next Monday

Recorded now so the next iteration is scored, not re-argued:

- **P1.** Pages in Yandex index rises from 12 to ≥ 20. Mechanism: the orphans are now
  internally linked and were pushed via IndexNow. *Falsified if still < 16.*
- **P2.** At least one of `/hosting-fastapi`, `/hosting-django`, `/hosting-flask`
  earns its first impression. Mechanism: 330w → 750w+ crossed the threshold every
  currently-ranking page sits above. *Falsified if all three stay at zero.*
- **P3.** `analog-*` stays flat (≤ 35 impressions/week total). This is the control —
  it received no changes. If it moves, the cause is sitewide (apex fix, crawl budget),
  not the content work, and P1/P2 must be re-attributed.
- **P4.** Google stays at zero until the GSC property is verified. *Falsified by any
  real Googlebot hit in the ingress logs* — check with the crawler count above.

## Correction — 2026-07-30, first automated pull (`tasks/seo/2026-07-30.md`)

A Yandex OAuth token arrived and `scripts/seo-weekly.py` produced the first real
pull (window 06-30..07-30). It contradicts parts of the analysis above, which was
built on a screenshot. The screenshot-derived rows in the baseline table are
superseded by these:

| Metric | Screenshot estimate | Measured |
|---|---|---|
| Pages in search | 12 | **16** (`searchable_pages_count`) |
| Excluded pages | unknown | **0** |
| Impressions | ~36/week | **97/30d** |
| Clicks | 3/week | **11/30d**, CTR 11.3% |
| SQI | unknown | **0**, flat for the whole window |

### What the query data actually says

The demand is **not** "аналог X". It is *payment and access*:

| Intent cluster | Impressions | Clicks | Avg position |
|---|---:|---:|---|
| "оплата / оплатить {vercel, netlify, heroku, railway}" | ~28 | **0** | 6-12 |
| "работает ли {netlify, vercel, railway} в России" | ~10 | **0** | 5-12 |
| "аналог(и) {vercel, railway, heroku}" | ~10 | 1 | 3-9 |
| brand ("dada cloud", "как загрузить телеграмм бота на dada console") | 6 | 2 | 1.0-1.3 |
| bot hosting ("дешевый хостинг для тг бота" etc.) | 2 | 0 | 8-13 |

**39% of all search demand is payment/access intent converting at 0%.** The
`analog-*` pages rank for it — they are stuffed with "оплата рублями" (23-28
occurrences each) — but their titles answer a different question than the one
typed. Someone searching "оплата vercel для россиян" wants to pay Vercel; the
snippet offers to replace it. Position 8 with a zero click-through is an intent
mismatch, not a ranking problem.

This **overturns the "STOP expanding analog-*" verdict above.** The cluster is not
at its ceiling; it is mis-targeted. The correct next move is a page that answers
the payment/access question directly and offers the alternative at the bottom —
not another "аналог X" page, and not abandoning the cluster.

### Three defects the pull exposed that the screenshot could not

1. **Renamed slugs 404 instead of redirecting.** Yandex still crawls
   `/telegram-bot-hosting` and `/vibe-coding-deploy` (+ `/en`) — the pre-rename
   slugs — and gets 404. Fixed: 301s in `next.config.ts`, verified locally.
2. **`/analog-vercel` and `/pricing` served Yandex a 503 on 2026-07-20.** The best
   performing page in the whole site returned an error to the crawler. Worth
   correlating with the console rollout schedule.
3. **`NO_REGIONS` and `NOT_IN_SPRAV` are the only open Webmaster problems.** The
   site has no region assigned, which suppresses geo-relevant ranking for a
   Russia-targeted site. Free to fix, owner-side, in the Webmaster UI.

### The RU/EN asymmetry — open question, not a diagnosis

Of the 16 URLs in search, the `/en` mirrors of five "orphan" landings are indexed
(`/en/hosting-flask`, `/en/hosting-fastapi`, `/en/hosting-django`,
`/en/hosting-streamlit`, `/en/hosting-vk-bot`, `/en/deploy-without-git`) while
their RU originals are not — despite both being crawled 200 on the same day
(2026-07-22) and carrying correct self-canonicals and reciprocal hreflang.

This weakens defect 1's stated mechanism: crawl reached the orphans fine, so
internal linking was not what kept them out. The fix was still worth making, but
the cause of the RU/EN split is unexplained. `NO_REGIONS` is the leading suspect
and is testable — assign the region, then re-check whether RU pages enter.

### The funnel leak is bigger than any SEO gain

Metrika goals over the same window: "Регистрация (переход на /register)" = 21
reaches (14 from `/`, 3 `/analog-vercel`, 2 `/analog-railway`, 2
`/deploy-vibe-coding`), "Регистрация: завершена (JS)" = 5. **A 76% drop between
starting and finishing signup.** Total search clicks for the month were 11. No
realistic content win moves the top of this funnel as much as recovering the
people already in it. The `signup_started` goal shipped tonight makes the drop
measurable per source next week; that number should be the first thing checked.

### Owner actions that unblock the rest

1. Create the `cloud.dada-tuda.ru` property in Google Search Console, paste the token
   into `GOOGLE_SITE_VERIFICATION` on the cloud-console deployment, then submit
   `sitemap.xml`. This is the entire Google story.
2. Register `landing_cta_click` and `signup_started` in Metrika counter 110158915 as
   JavaScript-event goals — the calls fire, but reports stay empty until the goals
   exist.
3. ~~Supply one Yandex OAuth token~~ — done, first automated pull is
   `tasks/seo/2026-07-30.md`.
4. Assign the site region in Yandex Webmaster (`NO_REGIONS` is open). Free, and it
   is the leading suspect for the RU/EN indexation split.

### Next iteration's build queue, in priority order

1. Investigate the 21 → 5 signup drop. Highest expected value in the file.
2. One page serving payment/access intent ("как оплатить Vercel/Netlify/Heroku из
   России", "работает ли X в России") — ~38 impressions/month currently converting
   at zero. Needs research before it is written: it makes factual claims about
   third-party availability that age fast.
3. Only then more bot-cluster pages. Bot demand in Yandex is currently 2
   impressions/month; the cluster is a product-fit bet, not yet a measured one.

## Correction 2 — 2026-07-30, the "76% signup drop" is a measurement artifact

Queue item 1 said "investigate the 21 → 5 signup drop. Highest expected value in the
file." Investigated. The drop is not real, and the shape of the funnel is different
from what the Metrika goals imply.

### What the goals actually measure

`Регистрация (переход на /register)` is a **url-contains-`register`** goal, so it
counts visits that touched the page, not people. `Регистрация: завершена (JS)` fires
only from `/callback`, and only when `startRegister()` wrote the
`dada_pending_registration` marker within the last 30 minutes
(`frontend/app/callback/page.tsx:51`). Anyone who signs up by clicking Keycloak's own
"Register" link from `/login` — 65 pageviews vs `/register`'s 14 in the same window —
creates a real account and never trips the goal. The two numbers were never a funnel.

### The real numbers, from Keycloak and the console DB

| Stage | Count (2026-06-30..07-30) | Source |
|---|---:|---|
| Keycloak accounts created | 22 | admin API `createdTimestamp` |
| — minus e2e/test/internal | **16 real signups** | excludes `*@dada-tuda.ru`, `sp2-verify`, UUID-named |
| Started at least one build | **7** | `builds` join `environments` join `projects` |
| Reached a successful build | **7** | `status='success'` |
| Never created an app at all | **8** | zero `resource_snapshots` of kind App |

So signup completes fine — roughly 16 accounts against 25 `/register` goal reaches.
**The leak is activation: 9 of 16 real signups (56%) never triggered a single build.**

Two things fall out of the same query:

- **Everyone who started a build got a green one.** with_builds = 7, activated = 7.
  Build reliability is not the constraint right now; pressing the button is.
- **The drop-off users left no trace.** 8 of the 9 have zero rows in `audit_events`;
  the ninth (`top.decker@yandex.ru`) created an app and never built it. The project
  each of them owns is the one the backend auto-provisions on first login, not
  something they made.

### Why they stall — the one structural cause visible in code

`ONBOARDING_CAMPAIGNS` (`frontend/lib/onboarding/campaigns.ts`) contains exactly one
campaign, `agent`, pointing at the agent-chat FAB. There is no first-deploy campaign.
`user_onboarding` holds 6 rows total, all `agent`, all from internal users — including
for the two accounts created on 07-29, after the onboarding engine shipped on 07-25.
A new user lands in an auto-provisioned empty project with nothing directing them at
a first deploy.

This is product work, not SEO, and it is deliberately left queued rather than shipped
tonight: the fix is an onboarding path, and its shape is an owner call. But it is
correctly sized now — 56% of everyone who signs up, against 11 search clicks for the
whole month. It remains item 1.

### Effect on the queue

1. **Activation, not signup.** Build a first-deploy path for the empty project.
   Evidence above. Owner decision on shape.
2. Payment/access-intent landings — in flight in a parallel session
   (`/oplatit-vercel-iz-rossii`, `/rabotaet-li-vercel-v-rossii`, + `/en` mirrors),
   not duplicated here.
3. Bot-cluster pages — unchanged, still a product-fit bet at 2 impressions/month.

Also worth fixing when someone touches Metrika next: rename the url goal to
"Visited /register" so it stops reading as a signup count, and fire
`registration_complete` from the console shell on first authenticated load for a
brand-new `sub` rather than from the marker, so KC-side signups are counted too.

## Verified live — 2026-07-31

Image `e31d207d` (build #661) carries the redirect commit `0de4792`; Argo has it on
prod.

| Check | Result | Source |
|---|---|---|
| `/telegram-bot-hosting`, `/vibe-coding-deploy` (+ `/en`) | 301 → renamed slug, all 4 | `curl -w '%{http_code} %{redirect_url}'` [live] |
| Redirect targets | 200 | same [live] |
| Apex `dada-tuda.ru` | still 301 → `https://cloud.dada-tuda.ru/` | same [live] |
| Frontend replicas | 2/2 ready, on two different nodes | `kubectl get deploy/pods -o wide` [live] |
| IndexNow push of the 8 affected URLs | Yandex 202, Bing 200 | `scripts/indexnow-submit.py` [live] |

Defect 2 (the 07-20 503 on `/analog-vercel` and `/pricing`) is addressed at the cause:
the frontend ran a single replica while also serving the public marketing site, so any
restart or drain was an outage window. Now 2 replicas with `maxUnavailable: 0` and a
soft anti-affinity — chart `050acaa` in dada-cloud, values `c1a738e1` in argo-infra.
Whether the crawler sees another 5xx is itself a prediction to grade next Monday.

## First-deploy onboarding shipped — 2026-07-31

Queue item 1 is done, commit `e84680a` on main. `ONBOARDING_CAMPAIGNS` now leads
with `first-deploy` ahead of `agent`; the anchor is the deploy hero
(`data-onboarding="first-deploy"`) on the project overview and the apps empty
state, which React renders only while the project has zero apps — so "who sees
the tour" needs no route guard and no app count in the provider. The copy names
the two things that skip the GitHub connect gate: one-click starter templates
(~2 min to a running URL) and folder upload.

The failure mode worth remembering: `onboardingKeys` in
`backend/internal/api/onboarding.go` is a whitelist. A campaign key missing from
it 400s every `POST /onboarding/{key}`, so the status map never fills and the
tour re-runs on every page load, forever. `TestOnboardingKeysMatchFrontendRegistry`
now parses `campaigns.ts` and fails on drift in either direction.

**Prediction to grade next Monday (2026-08-03), from the same two sources as the
07-30 measurement:**

| # | Prediction | How to grade |
|---|---|---|
| 1 | ≥60% of accounts created after 07-31 have a `user_onboarding` row for `first-deploy` | `SELECT status, count(*) FROM user_onboarding WHERE onboarding_key='first-deploy' GROUP BY 1` vs KC accounts created in the window |
| 2 | Build-start rate among new signups rises above the 44% (7/16) baseline | KC `createdTimestamp` joined against `builds → environments → projects`, same query as 07-30 |
| 3 | `skipped` stays under half of `first-deploy` rows — if most people dismiss it, the copy is wrong, not the placement | status split in the same table |

Prediction 2 is the one that matters and the one most likely to stay noisy: at
~16 signups a month, a week's cohort is single digits. Read it as directional and
grade it again at 30 days. If the tour is seen (1) but the build rate does not
move (2), the constraint is downstream of the button — that is a different fix
than more nudging, and the `audit_events` trail for those users will say where
they stalled.

Not verified in a browser: the joyride spotlight over the hero grid. The console
route needs a Keycloak session, so placement is unproven visually — the engine
itself is the one already shipped for `agent` on 07-25.

### Verified live on prod — 2026-07-31

Jenkins #674 → image tag `84278750` (= commit `842787504d64`, whose parent is the
onboarding commit `e84680a`), auto-pinned in argo-infra, `cloud-console-prod` Synced.

| Check | Result | Source |
|---|---|---|
| Frontend rolled | `frontend:84278750`, 2/2 Ready on two nodes | `kubectl get deploy/pods -o wide` [live] |
| Backend rolled | `backend:84278750`, 2/2 Ready | same [live] |
| Campaign is in the served bundle | `first-deploy` and the RU copy found in `.next/static` + `.next/server` chunks inside the running pod | `kubectl exec … grep -rl` [live] |
| Whitelist accepts the key | `POST /api/v1/onboarding/first-deploy` → 200 `{"status":"ok"}` | curl with a KC service-account bearer [live] |
| Whitelist still rejects unknown keys | `POST /api/v1/onboarding/definitely-not-a-campaign` → 400 `unknown onboarding key` | same [live] |
| The report actually persisted | `GET /api/v1/onboarding` → `{"first-deploy":"seen"}`, and the row was visible in `user_onboarding` | curl + psql [live] |

The last two rows are the ones that matter: a 200 on the POST alone would not have
proved anything, since the infinite-tour failure mode is precisely a report that
never lands. The synthetic row written by that probe was deleted afterwards
(`DELETE 1`, count for `first-deploy` back to 0), so Monday's cohort count starts
from a clean baseline.

Still unverified: the joyride spotlight itself. The bundle contains the campaign
and the API accepts it, but nobody has seen the tooltip render over the hero grid —
that needs a Keycloak session in a browser.

### Browser probe closed that gap — and the tour was broken — 2026-07-31

Ran the console against a local mock API (probe worktree, dev server on :3077,
local-mode auth, no real credential in the page) and loaded a zero-app project.
The tooltip that rendered was **the agent one**, not first-deploy.

Cause: `OnboardingProvider` picked a campaign exactly once, when `GET /onboarding`
resolved. `[data-onboarding="agent-fab"]` is part of the console shell and exists
at first paint; `[data-onboarding="first-deploy"]` is the deploy hero, which mounts
only after the overview's four data calls return. The status fetch is one fast call
and always won, so `selectPendingCampaign` saw no first-deploy target and returned
`agent` — on every load, for every new user. The campaign shipped in `e84680a` never
rendered once in production.

Fix `e59137e`: selection is now a pure function `selectCampaignToFire(campaigns,
statusMap, ctx, elapsedMs)` polled every 250ms for up to 10s. The top pending
campaign fires as soon as its own `delayMs` elapses; a lower-priority campaign holds
4s first, giving a page-level anchor time to mount. Five new tests cover the race,
the grace window, the delay floor and the window close (19 pass).

Browser evidence after the fix, same probe: the first-deploy tooltip renders anchored
to the deploy hero — "Первый деплой — в один клик" over the template + upload grid —
and "Понятно" POSTs `seen` then `done` to `/onboarding/first-deploy` and tears the
overlay down clean (no residual `.react-joyride__overlay`).

This moves the 07-31 predictions' start date: nothing could have been seen before
`e59137e` reaches prod, so grade tour-seen rate from that rollout, not from `e84680a`.
