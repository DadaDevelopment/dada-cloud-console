# Feature-Onboarding Engine — Design

Date: 2026-07-25
Status: approved-for-planning
Author: console team

## 1. Goal & context

Features ship constantly; users do not discover them. Build a reusable engine that:

1. Tracks, per user, which onboardings they have completed / skipped.
2. On rollout of a new feature, gently spotlights it once (dimmed screen + focus on the target element + short copy), with a mandatory **Skip** button and a **docs link** for those who would rather read than click.
3. Persists the outcome so a given user is never re-shown a campaign they finished or skipped.

First campaign to ship this way: **the AI agent** (the Bot FAB in the console shell).
Next campaign (later, not in this build): **billing / payment nudge** — billing itself is still WIP.

The engine is generic. Shipping a new onboarding = add one entry to a code-side campaign registry + (for the target) one `data-onboarding` attribute. No schema change, no backend change per campaign.

## 2. Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Completion storage | **Server DB** (`user_onboarding` table) | Cross-device, survives cache-clear, and — decisive — makes the onboarding funnel measurable in SQL (`% seen / skipped / done` per key). localStorage is blind on metrics and per-browser. Billing-nudge later also needs server truth. |
| Spotlight renderer | **react-joyride 3.2.0** | Owner choice. Verified viable: v3.2.0 (published 2026-07-09) dropped `react-floater`/`findDOMNode` (which React 19 removed) and now uses `@floating-ui/react-dom`; peerDeps `react: "16.8 - 19"` — satisfied by this repo's React 19.2.4 / Next 16.2.7. Gives spotlight + tooltip + Skip/step controls out of the box. |
| Audience | **All users who have not yet seen the campaign** | Simplest correct rule; covers old and new users. No `created_at` cohorting. "Gentle" is enforced by delay + one-campaign-per-session, so even a brand-new user is not bombarded. |
| Campaign definitions | **Code-side registry** (TS array in frontend, key-whitelist mirrored in backend) | Versioned via git, no admin CMS. YAGNI on a campaign-management UI. |
| Re-trigger a changed onboarding | **Ship a new `onboarding_key`** | No `version` column needed. |

## 3. Data model — migration `backend/migrations/049_user_onboarding.sql`

Uses `user_sub TEXT` (the Keycloak subject, stored as its UUID string), matching the existing per-user tables `agent_chat_messages` (046) and `feedback` (040). No FK to the legacy local-auth `users` table (that table is bcrypt-era local auth, not how console users are identified).

```sql
CREATE TABLE IF NOT EXISTS user_onboarding (
    user_sub        TEXT        NOT NULL,
    onboarding_key  TEXT        NOT NULL,
    status          TEXT        NOT NULL CHECK (status IN ('seen', 'skipped', 'done')),
    step_reached    INT         NOT NULL DEFAULT 0,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_sub, onboarding_key)
);
```

Status semantics:
- `seen` — campaign started (spotlight shown) but neither finished nor explicitly skipped. Also set when the user opens the docs link. Terminal-for-display: a `seen` row still counts as shown, so we do not re-nag mid-session, but it distinguishes "started but dropped" from "finished" in the funnel.
- `skipped` — user pressed Skip.
- `done` — user completed the last step.

Once a row exists with `skipped` or `done`, the campaign is never shown again. A `seen` row is not re-shown in the same session (one-per-session guard) and is treated as already-shown on later loads (we do not re-spotlight; the feature is now visible to them). Upsert is monotonic: never downgrade `done`/`skipped` back to `seen`.

## 4. Backend — 2 endpoints

Registered in `backend/internal/api/router.go` inside the existing authed group `api := r.Group("/api/v1", authMW)` (router.go:263), next to the agent routes (483-484):

```go
api.GET("/onboarding", h.GetOnboarding)
api.POST("/onboarding/:key", h.PostOnboarding)
```

Handler file: `backend/internal/api/onboarding.go`. User identity via the established pattern:

```go
claims, ok := auth.GetClaims(c)
// ...
userSub := claims.UserID.String()
```

### GET /api/v1/onboarding
Returns the current user's onboarding state as a key→status map:
```json
{ "agent": "done" }
```
Empty object if the user has no rows. Frontend diffs this against the local campaign registry to find pending campaigns.

### POST /api/v1/onboarding/:key
Body: `{ "status": "seen" | "skipped" | "done", "step": <int> }` (bound via `c.ShouldBindJSON`).
Upsert on `(user_sub, onboarding_key)`:
```sql
INSERT INTO user_onboarding (user_sub, onboarding_key, status, step_reached)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_sub, onboarding_key) DO UPDATE
SET status       = EXCLUDED.status,
    step_reached = GREATEST(user_onboarding.step_reached, EXCLUDED.step_reached),
    updated_at   = NOW()
WHERE user_onboarding.status NOT IN ('done', 'skipped');  -- monotonic: never downgrade a terminal state
```

`:key` is validated against a backend whitelist (a small `map[string]bool` / slice of known keys, kept in sync with the frontend registry) so arbitrary keys cannot be written. Unknown key → 400.

OpenAPI: both routes need doc comments so `TestOpenAPICoverage` passes; run `swag init -o internal/api/docs` after adding them (repo CI gate).

## 5. Frontend engine

### 5.1 OnboardingProvider
New: `frontend/components/onboarding/onboarding-provider.tsx`, mounted inside `ConsoleShell` in `frontend/app/(console)/layout.tsx` (alongside `AgentChatPanel`). Client component.

Flow on mount:
1. `GET /api/v1/onboarding` → status map.
2. Diff against the local **campaign registry**; compute the ordered list of *pending* campaigns (no `done`/`skipped`/`seen` row).
3. Pick the first pending campaign whose target element currently exists in the DOM and whose optional `route` predicate matches the current path.
4. After `delayMs` (default ~3000 ms) and only if no suppressor is active, render one `<Joyride>` for it.
5. On Joyride callback:
   - tour start (first step becomes visible) → `POST {status:'seen', step:0}` (fire-and-forget). This is emitted **immediately on show**, not on advance, so a user who views then closes the tab is still recorded as `seen` (funnel-correct, and it will not re-fire next session). Critical for single-step campaigns where there is no "advance".
   - step advance → `POST {status:'seen', step:n}`.
   - Skip → `POST {status:'skipped'}`.
   - docs link click → `POST {status:'seen'}` then open `docsUrl`.
   - finished (last step) → `POST {status:'done'}`.

State is fetched once per console session; after a POST we optimistically mark the campaign resolved in memory so it will not re-fire this session even before the server round-trips.

### 5.2 Campaign registry
New: `frontend/lib/onboarding/campaigns.ts`:

```ts
export type OnboardingStep = {
  target: string;        // CSS selector, e.g. '[data-onboarding="agent-fab"]'
  titleKey: string;      // i18n key
  bodyKey: string;       // i18n key
};

export type OnboardingCampaign = {
  key: string;                       // matches backend whitelist, e.g. 'agent'
  steps: OnboardingStep[];
  docsUrl: string;
  delayMs?: number;                  // default 3000
  route?: (pathname: string) => boolean;  // default: always (target-existence is the real gate)
};

export const ONBOARDING_CAMPAIGNS: OnboardingCampaign[] = [ /* agent, ... */ ];
```

The same key list is the source for the backend whitelist (kept in sync manually; a tiny list).

### 5.3 Targeting
Target elements carry a stable `data-onboarding="<name>"` attribute, decoupled from styling classes. For campaign #1, the agent FAB button in `layout.tsx` (the `<button>` at layout.tsx:91-101) gets `data-onboarding="agent-fab"`.

### 5.4 Gentle / non-annoying rules
- At most **one** campaign shown per console session.
- `delayMs` (~3 s) after mount before the spotlight appears — no jarring instant overlay.
- Suppressed while the agent chat panel is open, the command palette is open, or the mobile nav drawer is open (these own the screen).
- A campaign only fires where its target element actually exists — no spotlight pointing at nothing.
- Skip and docs-link are always present in the tooltip (owner mandate).

### 5.5 Joyride theming
Joyride `styles` wired to the app's dark/light theme (the repo is theme-aware). Overlay dim, spotlight radius/padding tuned for a floating FAB, `spotlightClicks` so the user can still click the highlighted control. Tooltip renders custom footer: **Skip** (left) + **Читать в доке / Read docs** (link) + **Понятно / Got it** (finish).

## 6. i18n
New message fragment `frontend/lib/i18n/console/messages/onboarding.ts` (ru/en), following the existing fragment pattern, consumed via `useT()`. Keys for agent campaign title/body, plus shared button labels: `onboarding.skip`, `onboarding.readDocs`, `onboarding.gotIt`.

## 7. Campaign #1 — Agent (concrete)

```ts
{
  key: 'agent',
  docsUrl: '/developer/agent',   // or the real agent docs path
  steps: [
    {
      target: '[data-onboarding="agent-fab"]',
      titleKey: 'onboarding.agent.title',   // ru: "Познакомься с AI-агентом"
      bodyKey:  'onboarding.agent.body',     // ru: "Чинит билды, деплоит, ставит env-переменные. Просто напиши задачу."
    },
  ],
}
```

Single-step spotlight on the Bot FAB. Extensible later to a step-2 inside the opened panel without schema change.

## 8. Analytics (no build, SQL only for v1)

Funnel is queryable directly:
```sql
SELECT onboarding_key, status, COUNT(*)
FROM user_onboarding
GROUP BY onboarding_key, status
ORDER BY onboarding_key, status;
```
An admin overview card is a possible phase-2 follow-up (the codebase already surfaces business numbers in admin), out of scope here.

## 9. Scope

**Build now:**
- Migration 049 (`user_onboarding`).
- Backend `onboarding.go` + 2 routes + OpenAPI docs + key whitelist.
- Frontend `OnboardingProvider` + campaign registry + Joyride integration + i18n fragment.
- `react-joyride` dependency (3.2.0).
- Agent campaign (1 step) + `data-onboarding="agent-fab"` on the FAB.

**Not now (YAGNI / out of scope):**
- Billing / payment nudge campaign — add a registry entry when billing ships.
- Admin analytics card.
- Multi-step agent tour.
- Campaign CMS / admin UI.
- `created_at` audience cohorting.

## 10. Testing
- Backend: handler test for GET (empty + populated) and POST (insert, idempotent upsert, monotonic no-downgrade of `done`→`seen`, unknown-key 400). Follow the `agent_chat_confirm_test.go` DB-integration pattern (skips when `TEST_DATABASE_URL` unset).
- Frontend: unit test the pending-campaign selection (diff registry vs status map; respects `done`/`skipped`/`seen`; one-per-session). Joyride render is thin; smoke-test the provider picks the agent campaign when status map is empty and the target exists.
- Manual/live: log in as a user with no `user_onboarding` rows → agent spotlight appears after ~3 s → Skip writes `skipped` → reload → no spotlight. Repeat asserting `done` via finish.

## 11. Risks / open
- **react-joyride + Next 16 App Router / React 19**: v3.2.0 declares React 19 support and uses `@floating-ui` (no `findDOMNode`). Still bleeding-edge — first integration must be smoke-tested live in the console shell before calling it done. If Joyride misbehaves under Next 16 RSC/StrictMode, fallback is a ~150-line custom overlay (same data model + endpoints, only the renderer swaps) — the engine is decoupled from the renderer, so this is a contained risk.
- **Whitelist drift**: backend key whitelist and frontend registry are kept in sync by hand. Small list; a test asserting the two match would harden it (nice-to-have).
- **FAB always present**: agent FAB lives in the console shell on all routes, so the agent campaign can fire on any console page — acceptable and desired.
```
