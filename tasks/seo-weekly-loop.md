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
