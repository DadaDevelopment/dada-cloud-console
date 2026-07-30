# SEO operating plan — cloud.dada-tuda.ru

Baseline pull: `tasks/seo/2026-07-30.md` / `.json` (Webmaster v4 + Metrika stat v1, window 2026-06-30..2026-07-30).
Re-run weekly: `YANDEX_OAUTH_TOKEN=$(kubectl get secret -n crossplane-system yandex-metrica-credentials -o jsonpath='{.data.token}' | base64 -d) python3 scripts/seo-weekly.py`

## Baseline numbers (API, not screenshots)

| metric | value |
|---|---|
| pages in search | 16 (sitemap has 84) |
| shows / clicks / CTR | 97 / 11 / 11.3% |
| removals in window | 14 — 13 `LOW_QUALITY`, 1 `NOTHING_FOUND` |
| Metrika sessions from Yandex | 84 (10 users) |
| Metrika sessions from Bing | 3 |
| Metrika sessions from Google | 0 |
| goal "переход на /register" | 21 total, 7 from landings |
| goal "регистрация завершена" | 5, none attributed to a landing |
| SQI | 0 |

## Demand shape (all 97 shows classified)

| intent cluster | shows | clicks | avg pos | page that targets it |
|---|---:|---:|---:|---|
| "оплата/оплатить X из России" | 31 (32%) | 0 | 8-12 | none |
| "аналог(и) X" | 11 | 4 (36% CTR) | 2-9 | `/analog-*` |
| "работает ли X в России" | 10 | 0 | 5-12 | none |
| bot hosting | 5 | 0 | 1-13 | `/hosting-telegram-bot` |
| brand "dada cloud" | 3 | 2 | 1.0 | `/` |
| vibe-coding / lovable | 1 | 0 | 9 | `/deploy-vibe-coding` |

Read: the `/analog-*` bet is validated on conversion (36% CTR whenever position <= 4.5) but the
cluster is small. The largest unserved demand is the payment-block intent, where the site is
force-matched at position 8-12 with a page that answers a different question, hence 0 clicks.

## Blockers, ranked

1. **`LOW_QUALITY` churn.** Yandex admits pages then drops them. Every dropped page is short:
   `/storage` 208 words, `/databases` 267, `/developer` 319, `/pricing` 359, `/cloud-servers` 372.
   Every survivor is long: `/analog-vercel` 866, `/hosting-telegram-bot` 825, `/analog-railway` 760, `/` 692.
   The six EN thin pages that entered on 07-29 are predicted to be dropped next cycle — that
   prediction is the first check of the weekly loop.
2. **Orphan pages.** `/deploy-without-git`, `/hosting-fastapi`, `/hosting-flask`, `/hosting-django`,
   `/hosting-streamlit`, `/hosting-vk-bot` have zero inbound internal links; they are not in
   `components/marketing/footer.tsx` nor on the homepage. 12 URLs with ru+en.
3. **Crawl waste.** Four renamed slugs still 404 for Yandex (`/telegram-bot-hosting`,
   `/vibe-coding-deploy`, both ru+en); `/en/kubernetes` 404. No 301s.
4. **503 to Yandexbot** on `/pricing` and `/analog-vercel` at 2026-07-20 — deploy window served
   errors to the crawler.
5. **`NO_REGIONS` + `NOT_IN_SPRAV`** flagged by Webmaster diagnostics. Region is unset, which
   caps RU commercial ranking.
6. **Apex `dada-tuda.ru` serves the ingress fake certificate** — TLS error on the brand domain,
   no 301 to the marketing host.
7. **No signup attribution in the product.** `users` has no source column; `?utm_source=pseo_*`
   is dropped at registration. Attribution exists only in Metrika.

## Allocation

**Cut** — stop producing new `/analog-*` pages. Seven exist; the cluster yields ~11 shows/month.

**Fix before adding anything** (new thin pages would be dropped the same way):
- [x] expand the ten sub-500-word pages past ~900 words of page-specific content — shipped 07-31
- [x] link the six orphans from footer + a homepage hub block, and cross-link topical siblings
- [x] 301 the five dead slugs (`next.config.ts` `RENAMED_LANDINGS`, ru+en, statusCode 301)
- [x] make deploys not serve 5xx to crawlers — `URL_ALERT_5XX` now `ABSENT` in diagnostics
- [x] issue a cert for the apex and 301 it to `cloud.dada-tuda.ru` — `https://dada-tuda.ru/` 301s, TLS valid
- [x] set the region in Webmaster, register in Yandex Business — owner did it in the UI on 07-31.
  Webmaster diagnostics still show `NO_REGIONS` and `NOT_IN_SPRAV` as `PRESENT`, but
  `last_state_update` is still `2026-07-30T13:35`, so the block has not been re-evaluated since;
  a Business listing also needs moderation before the region attaches. **Verdict deferred to the
  08-06 pull** — if the two recommendations are still `PRESENT` with a refreshed timestamp then,
  the listing did not take and it goes back to the owner.

### What the expansion pass actually changed (07-31)

The root cause was not only short copy. `FaqList` mounted the answer paragraph only for the open
accordion item, so every landing's FAQ prose lived in React state and never in the DOM — a
crawler saw the questions and nothing else. Rendering the answer always and hiding it with a
class lifted every FAQ-bearing page at once: `/analog-vercel` 700 -> 1137, `/hosting-telegram-bot`
-> 1043, `/storage` 910 -> 1216.

On top of that: `servers` / `databases` / `storage` gained how-to steps, use cases and a limits
block; `pricing` and `storage` gained FAQ; `developer` gained an intro; the five comparison
landings moved to the new `LandingGuide` shape (how-to, what-does-not-port, a six-row concept
mapping table, four extra Q&A). `HowTo` and `FAQ` JSON-LD follow the visible content.

Verified live after the roll (94 sitemap URLs, all 200):

- the two new payment-cluster pages: `/oplatit-vercel-iz-rossii` 1308, `/rabotaet-li-vercel-v-rossii`
  1112, `/en/*` 1482 and 1293
- `/analog-heroku` 967, and every other landing above the ~900 line
- IndexNow submitted all 94 URLs — Yandex 202, Bing 200

**Correction to the pre-deploy measurement.** The local sweep reported 62 routes with ten under
700 words. That was wrong: it never walked `/developer/*`, which `sitemap.ts` enumerates from the
content directory rather than listing statically. The real live figure is 94 URLs with 38 under
700 words, and 32 of those 38 are the `/developer/*` docs pages (442-668 words each, ru+en).
Only six are the legal/status/hub pages that genuinely should stay short (`/terms` 415,
`/status` 581, `/accept-payments` 594, `/developer` 623, `/pricing` 660, `/migrate-vercel` 697).

**The docs cluster is not being expanded on spec.** None of those 32 pages are in the index yet,
so there is no evidence they will be dropped, and rewriting 32 guides to chase a threshold
inferred from six landings is the kind of work that looks productive and proves nothing. It is a
watch item instead: if the weekly pull shows `/developer/*` URLs admitted and then removed as
`LOW_QUALITY`, they get expanded or dropped from the sitemap under the existing rule.

**Multiply** — the payment-block cluster, starting with Vercel (about 21 of 97 shows concern
Vercel payment/availability):
- `/oplatit-vercel-iz-rossii` — how to pay, why each route breaks, what to do instead
- `/rabotaet-li-vercel-v-rossii` — diagnostic intent
Both at 900+ words, cross-linked to `/analog-vercel`, CTA to register. Then Heroku, Netlify,
Railway if the pair works.

**Measure** — declare each new page won or lost 14 days after it enters the index, on shows,
average position, and goal reaches from the weekly pull.

## Standing predictions

| made | prediction | verdict due | how it is judged |
|---|---|---|---|
| 07-30 | the six thin EN pages admitted on 07-29 get dropped as `LOW_QUALITY` | 08-06 pull | Webmaster removal reasons |
| 07-31 | the ten expanded pages re-enter the index and stay | 14 days after they next appear | pages-in-search count, no new `LOW_QUALITY` for those URLs |
| 07-31 | `/oplatit-vercel-iz-rossii` and `/rabotaet-li-vercel-v-rossii` take the payment-block cluster (31 shows/mo at pos 8-12, 0 clicks today) and convert it | 14 days after each enters the index | shows on those URLs, average position <= 5, clicks > 0, goal "переход на /register" attributed to them |
| 07-31 | the 32 thin `/developer/*` docs pages are NOT dropped as `LOW_QUALITY` — the threshold behaves differently for docs than for landings | first pull in which any of them is indexed | Webmaster removal reasons for `/developer/*` URLs |
| 07-31 | Google indexes pages now that it crawls, but delivers close to zero clicks without backlinks: >= 20 URLs indexed and < 5 clicks | 08-14 | GSC Indexing report + Performance; Metrika sessions from Google |
| 07-31 | the Yandex region attaches — `NO_REGIONS` and `NOT_IN_SPRAV` both flip to `ABSENT` with a refreshed `last_state_update` | 08-06 pull | Webmaster diagnostics |

A prediction that is wrong is recorded as wrong in the weekly file — the point is calibration,
not a scoreboard.

## Weekly loop

1. Run `scripts/seo-weekly.py`; it diffs against the previous snapshot.
2. Check the standing prediction from last week (first one: do the thin EN pages get dropped).
3. Any page dropped as `LOW_QUALITY` is either expanded or deleted from the sitemap — never left.
4. Any query cluster with shows and no clicks gets a page or a title rewrite.
5. Ship at most two new pages; record the prediction for them.

## Google: discovery is unblocked, measured 07-31

Search Console is verified and `/sitemap.xml` was submitted on 07-30 — processed `Успешно`,
88 URLs discovered (the sitemap has since grown to 94 with the two new payment pages).

**Real Googlebot now fetches the site.** ingress-nginx-pub access logs, 48h window:

| bot | fetches |
|---|---:|
| YandexBot | 166 |
| Googlebot | 18 |
| bingbot | 13 |

The Googlebot traffic changes character exactly at the submission. On 29 July it is still the
spoofed-scanner pattern (`/config.json`, `/.continue/config.json`). At **30 July 17:17** a real
crawl starts: `GET /` 200 (71810 B from the frontend upstream), `GET /analog-railway` 200,
`robots.txt`, the icons, and a `_next/static` CSS chunk — fetching the stylesheet means the
rendering service, not just the raw fetcher. That falsifies the 07-29 finding that real Googlebot
had never touched the host; the cause was discovery, and Search Console fixed it.

**So the 0 clicks is not yet a signal.** The Performance chart's last point is 28 July, two days
before the sitemap was submitted, and the Indexing report still says data is processing. Crawled
is not indexed and indexed is not ranked; a fresh third-level domain with no backlinks takes
weeks. First honest read of Google is the 08-14 pull.

**The next Google lever is backlinks, not more pages.** Nothing else moves a domain with zero of
them.

Defect found while verifying the above and fixed in `frontend/next.config.ts`: the apex ingress
301s to a literal URL, so `dada-tuda.ru/robots.txt` — Googlebot's first apex request — answered
with the homepage's HTML, and any future inbound link to an apex path would land on `/`. Next now
redirects on a host condition and keeps the path. The ingress repoint that activates it is
tracked below.

## Google and Bing

Google delivered 0 sessions in 30 days and Bing 3. IndexNow reaches Yandex and Bing only —
Google ignores it. The first two steps are now done (Search Console verified, sitemap submitted
and processed), and Googlebot arrived within hours. What remains is links: a fresh third-level
domain with no backlinks will not rank on merit. Bing already crawls via IndexNow; adding Bing
Webmaster only buys reporting. Treat Google as a 3-6 month play and keep the RU budget on Yandex.

Open, in order:
- [ ] repoint the apex ingress at the frontend service so the path-preserving redirect in
      `next.config.ts` takes over from `permanent-redirect`
      (argo-infra `clusters/beget-prod/.../cloud-console/resources.values.yaml`)
- [ ] earn the first backlinks — this is the only remaining Google lever
