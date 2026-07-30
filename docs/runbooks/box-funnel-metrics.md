# Dada Box fake-door funnel — the four metrics, as SQL

The brief (`docs/product/box-product-brief.md` §7) names four numbers that decide
whether Box is built, and the plan (`docs/plans/2026-07-29-box-test-and-measurement.md`
§6) explains why they were previously unmeasurable. This file pins the exact query
for each, so everyone computes the same number the same way and the discussion is
about the number rather than about the definition.

Run everything over psql against the console database. All the queries below are
copy-pasteable and were executed against a real Postgres 16 with the schema from
`backend/migrations/060_box_leads.sql`.

**Status summary — read this before quoting any number:**

| # | Metric | Computable today? |
|---|---|---|
| 1 | view → request conversion | **Yes** (in our tables). Cross-door comparison: partly — see the caveat |
| 2 | share of leads with `crystallize_intent` | **Yes** |
| 3 | share who used a box again within a week | **NO. No data source exists yet** |
| 4 | stated willingness to pay | **Yes** for the stated figure; the cost side is not built |

---

## Where the data lives

| Relation | What it holds |
|---|---|
| `box_funnel_events` | every event: `page_view`, `demo_run`, `box_requested`, `crystallize_intent` |
| `box_leads` | one row per door submission, with the email the person typed |
| `box_grants` | the concierge's write-back: which claim got which box |
| `box_repeat_use_7d` | the pinned repeat-use definition as a view (**gated**, see metric 3) |

Two identifiers do all the joining:

- **`vid`** — the opaque visitor id from the `dada_vid` cookie (first-party, 400
  days, `SameSite=Lax`, `Secure`, `HttpOnly`, host-only). A UUID and never anything
  else: the ingest endpoint rejects a `vid` that does not parse as a UUID, so the
  column cannot hold an address. See `docs/architecture/yandex-metrika-uid-cookie.md`
  for why `dada_uid` could not be reused — it is only ever set for an authenticated
  console user, and this traffic is anonymous.
- **`claim`** — the `BOX-XXXX-XXXX` code the visitor is shown, minted by the
  landing's route handler so the person still gets one when the store is down.

### The blind spot, stated up front

A visitor who blocks cookies has `vid IS NULL`. Those rows are stored but cannot be
attributed to a person, so they are excluded from every `COUNT(DISTINCT vid)` on
both sides of a ratio. Check the size of the blind spot before trusting a
conversion figure:

```sql
SELECT event,
       COUNT(*)                                AS rows,
       COUNT(*) FILTER (WHERE vid IS NULL)     AS without_vid,
       ROUND(100.0 * COUNT(*) FILTER (WHERE vid IS NULL) / NULLIF(COUNT(*), 0), 1) AS pct_without_vid
  FROM box_funnel_events
 WHERE at >= now() - interval '30 days'
 GROUP BY event
 ORDER BY event;
```

If `pct_without_vid` is large, the conversion rate below is a rate over
cookie-accepting visitors and must be quoted that way.

### Funnel at a glance

Events and people side by side, because the difference between the two columns is
the whole reason this table exists — one curious visitor replaying the demo six
times is six events and one person.

```sql
SELECT event,
       COUNT(*)                 AS events,
       COUNT(DISTINCT vid)      AS people
  FROM box_funnel_events
 WHERE at >= now() - interval '30 days'
 GROUP BY event
 ORDER BY CASE event
            WHEN 'page_view' THEN 1
            WHEN 'demo_run' THEN 2
            WHEN 'box_requested' THEN 3
            WHEN 'crystallize_intent' THEN 4
          END;
```

---

## Metric 1 — view → request conversion

`page_view` is recorded server-side, once per session, deduplicated by `vid` inside
a 30-minute window. That is the point: before it existed, the denominator lived in
Yandex Metrika and the numerator in a log line, so the ratio was a comparison
between two systems with no join key and nobody could check it.

```sql
WITH views AS (
    SELECT COALESCE(utm_source, '(none)') AS utm_source,
           COUNT(DISTINCT vid)            AS visitors
      FROM box_funnel_events
     WHERE event = 'page_view'
       AND at >= now() - interval '30 days'
     GROUP BY 1
),
requests AS (
    SELECT COALESCE(utm_source, '(none)') AS utm_source,
           COUNT(DISTINCT vid)            AS requesters
      FROM box_funnel_events
     WHERE event = 'box_requested'
       AND at >= now() - interval '30 days'
     GROUP BY 1
)
SELECT COALESCE(v.utm_source, r.utm_source)  AS utm_source,
       COALESCE(v.visitors, 0)               AS visitors,
       COALESCE(r.requesters, 0)             AS requesters,
       ROUND(100.0 * COALESCE(r.requesters, 0) / NULLIF(v.visitors, 0), 1) AS conversion_pct
  FROM views v
  FULL OUTER JOIN requests r ON r.utm_source = v.utm_source
 ORDER BY visitors DESC NULLS LAST;
```

`utm_source` is `door_box` for anyone who arrived without a tag of their own, and
the inbound tag when they had one — so a Habr wave and the door's own baseline stay
separable.

### Caveat on comparing with the other door tests

The brief asks for this to be compared with the existing `door_*` tests. That
comparison is **not** a single query, and pretending otherwise would be a proxy.

- Our side (`door_box`) is a real ratio in SQL: distinct visitors → distinct
  requesters.
- The other doors (`/deploy-vibe-coding` → `/register?utm_source=door_b`) never
  land their `utm_source` in the database. `frontend/lib/metrika.ts` says so
  explicitly: the tag is dropped at registration, which is why the goal
  `landing_cta_click` exists at all. Their conversion is only available as Yandex
  Metrika goals.

So the honest comparison is: **our page_view → box_requested against their
page_view → `landing_cta_click`**, both read as "share of visitors who took the
page's main action", with the difference in instrumentation named out loud. Do not
compare our SQL conversion against their `signup_started` — a signup is a heavier
action than leaving an email on a preview page, and the gap would be read as a
Box result when it is a definition artefact.

---

## Metric 2 — share of leads that asked for crystallization

The brief calls this the event that validates the ladder: it tests not "is a box
wanted" but "does the graduation path matter". A low number with healthy requests
means a one-off utility was built, and the instruction is to close it rather than
keep tuning.

```sql
SELECT COUNT(*)                                                   AS leads,
       COUNT(ci.claim)                                            AS leads_with_intent,
       ROUND(100.0 * COUNT(ci.claim) / NULLIF(COUNT(*), 0), 1)    AS pct_with_intent
  FROM box_leads l
  LEFT JOIN (
      SELECT DISTINCT claim
        FROM box_funnel_events
       WHERE event = 'crystallize_intent'
         AND claim IS NOT NULL
  ) ci ON ci.claim = l.claim
 WHERE l.created_at >= now() - interval '30 days';
```

Which parts of crystallization they actually need — this is what 8.x gets built
against, so it is worth reading even at single-digit N:

```sql
SELECT want, COUNT(DISTINCT e.claim) AS claims
  FROM box_funnel_events e
  CROSS JOIN LATERAL jsonb_array_elements_text(e.props -> 'wants') AS want
 WHERE e.event = 'crystallize_intent'
   AND e.at >= now() - interval '30 days'
 GROUP BY want
 ORDER BY claims DESC, want;
```

---

## Metric 3 — repeat use within a week (**the headline metric**)

### The definition, pinned

> A **session** is a contiguous run of active minutes for one box; a gap of more
> than 30 minutes starts a new session. A claim counts as **repeat use** if it has
> **≥ 2 sessions whose starts are ≥ 24h apart, both within 7 days of first
> activation**.

Notes that the SQL settles and prose cannot:

- Sessions are cut **per box**; repeat use is judged **per claim**. A person handed
  a second box after the first was reaped is still one person, and their second
  session may live on the other box.
- "Gap of more than 30 minutes" means strictly `> interval '30 minutes'` between
  consecutive active `minute_start` values.
- Two sessions 3 hours apart are **not** repeat use. That is the case the
  definition exists to exclude: coming back the same afternoon is one work session
  with a coffee break in it, not a returning user.
- A session that starts more than 7 days after first activation is outside the
  window and does not count. The window is measured from first activation, not from
  the grant.
- A claim that was granted a box and never activated it does not appear in the view
  at all. Do not read its absence as `repeat_use = false`; measure activation
  separately (query below).
- A claim is only **scored** once its 7-day window has closed. Scoring an open
  window counts "has not come back yet" as "did not come back".

### THIS METRIC CANNOT BE COMPUTED TODAY

There is no active-minute ledger. The box object arrives in ФАЗА 2 and the minute
ledger in ФАЗА 7 of `tasks/box-backlog.md`. Consequently:

- `backend/migrations/060_box_leads.sql` creates the view **only if** a relation
  named `box_usage` already exists, and otherwise raises a NOTICE and skips it. The
  migration deliberately does not invent a substitute: a view returning zero rows
  over a table that does not exist would read as "nobody came back", which is a
  conclusion nobody measured.
- `dada_box_repeat_use_7d_ratio` reports **NaN**, not 0, until the view can supply
  a number. A gauge that reported 0 here would be the same lie in Prometheus form.
- **Until then the only honest source of repeat use is the operator's Telegram
  thread** — i.e. a table with names in it, labelled as a table with names in it.
  Returning page views, a second `demo_run`, and a reply to an email are **not**
  second use. This repository has already paid for that lesson twice
  (`tasks/lessons.md`).

Expected shape of the ledger, which the view is written against:

```
box_usage(box_id UUID, minute_start TIMESTAMPTZ, kind TEXT, ...)
one row per ACTIVE minute per kind; an idle minute produces no row at all
```

When ФАЗА 7 lands `box_usage`, re-run the guarded `DO` block from
`backend/migrations/060_box_leads.sql` (it is idempotent — `CREATE OR REPLACE
VIEW`) as part of that migration. The block is also reproduced there in full so it
cannot be lost.

### The queries, for when the ledger exists

Per-claim detail:

```sql
SELECT claim,
       first_activation_at,
       sessions_within_7d,
       last_session_start_within_7d,
       repeat_use
  FROM box_repeat_use_7d
 ORDER BY first_activation_at;
```

The headline ratio — the same number `dada_box_repeat_use_7d_ratio` publishes:

```sql
SELECT COUNT(*) FILTER (WHERE repeat_use)  AS repeat_claims,
       COUNT(*)                            AS scored_claims,
       ROUND(100.0 * COUNT(*) FILTER (WHERE repeat_use) / NULLIF(COUNT(*), 0), 1) AS repeat_use_pct
  FROM box_repeat_use_7d
 WHERE first_activation_at <= now() - interval '7 days';
```

Activation, reported next to it so "never came even once" is never hidden inside a
repeat-use denominator:

```sql
SELECT COUNT(DISTINCT g.claim)                                     AS granted_claims,
       COUNT(DISTINCT r.claim)                                     AS activated_claims,
       COUNT(DISTINCT g.claim) - COUNT(DISTINCT r.claim)           AS granted_never_activated
  FROM box_grants g
  LEFT JOIN box_repeat_use_7d r ON r.claim = g.claim;
```

And the leads that were never granted anything — a concierge backlog, and the
number that quietly invalidates a funnel if it grows:

```sql
SELECT l.claim, l.created_at, l.locale, l.utm_source
  FROM box_leads l
  LEFT JOIN box_grants g ON g.claim = l.claim
 WHERE g.claim IS NULL
 ORDER BY l.created_at;
```

### Recording a grant

The write-back is mandatory. Without it this metric has no data source at all.
Either the admin endpoint (platform-admin bearer):

```bash
curl -sS -X POST https://console.dada-tuda.ru/api/v1/admin/box/grants \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"claim":"BOX-7F3A-9C21","org_id":"<org>","box_id":"<box uuid>"}'
```

or one insert:

```sql
INSERT INTO box_grants (claim, org_id, box_id, granted_by)
VALUES ('BOX-7F3A-9C21', '<org>', '<box uuid>', '<operator user id>')
ON CONFLICT (claim, box_id) DO UPDATE
  SET granted_at = now(), granted_by = EXCLUDED.granted_by;
```

Granting a **second** box to the same claim adds a row — that is the normal case
once the first box has hit its TTL, and it is why the key is `(claim, box_id)`
rather than `claim`.

---

## Metric 4 — stated willingness to pay

What people said they would pay, straight from the form:

```sql
SELECT COALESCE(price, '(unanswered)') AS price,
       COUNT(*)                        AS leads,
       ROUND(100.0 * COUNT(*) / SUM(COUNT(*)) OVER (), 1) AS pct
  FROM box_leads
 WHERE created_at >= now() - interval '30 days'
 GROUP BY 1
 ORDER BY leads DESC, price;
```

Useful alongside it — parallelism demand, which is the strongest wedge in the
brief's ICP-2:

```sql
SELECT COALESCE(parallel, '(unanswered)') AS parallel_boxes,
       COALESCE(agent, '(unanswered)')    AS agent,
       COUNT(*)                           AS leads
  FROM box_leads
 WHERE created_at >= now() - interval '30 days'
 GROUP BY 1, 2
 ORDER BY leads DESC, parallel_boxes;
```

**The cost side of this metric is not built.** The brief says to reconcile the
stated price against the cost of a box-minute. That cost comes from
`billing/data/box-fleet-cost.yaml` + `costengine.PerMinuteCost`, which are ФАЗА 7
items and do not exist. Until they do, quote the stated figure as a stated figure
and do not describe any margin — an unverified margin is exactly the kind of
comforting number that survives review and then turns out to be wrong.

---

## Prometheus

| Metric | Meaning |
|---|---|
| `dada_box_funnel_events_total{event,locale}` | live counter per event; incremented only when a row was actually written, so a deduplicated `page_view` does not inflate the top of the funnel |
| `dada_box_repeat_use_7d_ratio` | metric 3, refreshed by the state collector. **NaN while the view does not exist** |

Prometheus carries no `claim`, `vid`, `org_id` or `email` label, by design and by
test (`TestBoxMetricSurfaceConventions`): unbounded cardinality on one side and
personal data on the other. Per-person truth is in the tables above.

## Data-protection rules this funnel is built to keep

1. The only personal datum stored is the email/contact the person deliberately
   typed into the form, and it lives in `box_leads` only.
2. `vid` is an opaque UUID. The ingest endpoint rejects anything else, so the
   column cannot hold an address even if a client hand-rolls the request.
3. Nothing personal is logged. The fallback log line in
   `frontend/app/api/box/lead/route.ts` carries `has_email` / `has_contact`
   booleans and a `stored` flag — never the address itself, because stdout ships to
   OpenSearch and is retained for weeks (`docs/runbooks/telemetry-retention.md`).
4. The email reaches the operator through the webhook/Telegram notification, which
   is a direct message to a human who has to answer the lead, not a log sink.
5. No `user_agent` column. It is fingerprintable and adds nothing to these four
   metrics.
