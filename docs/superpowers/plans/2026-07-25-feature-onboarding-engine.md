# Feature-Onboarding Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Track per-user which feature-onboardings they finished/skipped, and on rollout gently spotlight a new feature once (dim + focus + Skip + docs link), starting with the AI agent.

**Architecture:** Server-side truth in a new `user_onboarding` table (keyed by Keycloak `user_sub`), two thin authed endpoints, and a generic frontend `OnboardingProvider` that diffs a code-side campaign registry against the user's status map and renders one `react-joyride` spotlight. Adding a future onboarding = one registry entry + one `data-onboarding` attribute; no schema or backend change.

**Tech Stack:** Go + gin + pgx (backend), Postgres, Next 16 App Router + React 19 + TypeScript (frontend), `react-joyride` 3.2.0.

## Global Constraints

- React 19.2.4 / Next 16.2.7 / react-dom 19.2.4 — do not downgrade. `react-joyride` MUST be `3.2.0` (first version with React-19 support; earlier versions pull `react-floater`/`findDOMNode` which React 19 removed).
- **No inline code comments** (`//`, `#`) in source files — house rule. Use JSDoc/doc-comment blocks or Go doc comments only.
- **No U+2011 (non-breaking hyphen) or U+00A0 (NBSP)** anywhere — plain ASCII `-` and plain space. Applies to Go, TS, SQL, and ru/en i18n strings.
- User identity column is `user_sub TEXT` (the Keycloak subject as its UUID string), matching `agent_chat_messages` (046) and `feedback` (040). **No FK** to the legacy local-auth `users` table.
- Every new backend route needs swagger doc-comments; regenerate docs with `swag init` — `TestOpenAPICoverage` gates CI.
- i18n: every user-facing string has ru + en. Fragment file shape is `Record<string, {ru,en}>` merged in `frontend/lib/i18n/console/messages/index.ts`.
- Trunk-based: commit on `main`, push after each commit. Before every push: `git fetch origin main` and confirm `git log --oneline origin/main..HEAD` carries only your commits (shared working tree with other sessions). Stage explicit paths, never `git add -A`.
- Frontend has **no JS unit-test runner** (only Playwright e2e). Frontend automated gate = `npx tsc --noEmit` + `npx eslint`. Behavior is proven by the live browser smoke in Task 5.
- `respondError(c, status, msg)` is the backend error helper; `auth.GetClaims(c)` returns `(*auth.Claims, bool)`; `claims.UserID.String()` is the `user_sub`.

---

### Task 1: Backend — table, endpoints, tests

**Files:**
- Create: `backend/migrations/049_user_onboarding.sql`
- Create: `backend/internal/api/onboarding.go`
- Create: `backend/internal/api/onboarding_test.go`
- Modify: `backend/internal/api/router.go` (add 2 routes in the `api := r.Group("/api/v1", authMW)` group, next to lines 483-484)
- Regen: `backend/internal/api/docs/*` via `swag init`

**Interfaces:**
- Produces (frontend Task 2 mirrors these):
  - `GET /api/v1/onboarding` → `200 {"<key>":"<status>", ...}` (empty object if no rows).
  - `POST /api/v1/onboarding/:key` body `{"status":"seen"|"skipped"|"done","step":<int>}` → `200 {"status":"ok"}`; unknown key or bad status → `400`.
  - Known-key whitelist: `onboardingKeys = {"agent": true}` in `onboarding.go`.

- [ ] **Step 1: Write the migration**

Create `backend/migrations/049_user_onboarding.sql`:

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

- [ ] **Step 2: Apply the migration to your test DB**

Run (whatever the repo uses to apply `backend/migrations/*` against `TEST_DATABASE_URL`; the same schema the agent-chat tests assume):

```bash
psql "$TEST_DATABASE_URL" -f backend/migrations/049_user_onboarding.sql
```

Expected: `CREATE TABLE`.

- [ ] **Step 3: Write the failing handler tests**

Create `backend/internal/api/onboarding_test.go`. Follows the `agent_chat_confirm_test.go` harness (skips when `TEST_DATABASE_URL` unset; `auth.SetClaims` to inject identity).

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
)

func testOnboardingPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping onboarding DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newOnboardingCtx(t *testing.T, method, target, body string) (*gin.Context, *httptest.ResponseRecorder, uuid.UUID) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	c.Request = httptest.NewRequest(method, target, reader)
	c.Request.Header.Set("Content-Type", "application/json")
	uid := uuid.New()
	auth.SetClaims(c, &auth.Claims{UserID: uid})
	return c, rec, uid
}

func TestOnboarding_GetEmpty(t *testing.T) {
	pool := testOnboardingPool(t)
	h := &Handler{pool: pool}
	c, rec, _ := newOnboardingCtx(t, http.MethodGet, "/api/v1/onboarding", "")
	h.GetOnboarding(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty map for fresh user, got %v", got)
	}
}

func TestOnboarding_PostThenGet(t *testing.T) {
	pool := testOnboardingPool(t)
	h := &Handler{pool: pool}

	cPost, recPost, uid := newOnboardingCtx(t, http.MethodPost, "/api/v1/onboarding/agent", `{"status":"seen","step":0}`)
	cPost.Params = gin.Params{{Key: "key", Value: "agent"}}
	h.PostOnboarding(cPost)
	if recPost.Code != http.StatusOK {
		t.Fatalf("post seen: want 200, got %d: %s", recPost.Code, recPost.Body.String())
	}

	cGet, recGet, _ := newOnboardingCtx(t, http.MethodGet, "/api/v1/onboarding", "")
	auth.SetClaims(cGet, &auth.Claims{UserID: uid})
	h.GetOnboarding(cGet)
	var got map[string]string
	_ = json.Unmarshal(recGet.Body.Bytes(), &got)
	if got["agent"] != "seen" {
		t.Fatalf("want agent=seen, got %v", got)
	}
}

func TestOnboarding_MonotonicNoDowngrade(t *testing.T) {
	pool := testOnboardingPool(t)
	h := &Handler{pool: pool}

	cDone, _, uid := newOnboardingCtx(t, http.MethodPost, "/api/v1/onboarding/agent", `{"status":"done","step":1}`)
	cDone.Params = gin.Params{{Key: "key", Value: "agent"}}
	h.PostOnboarding(cDone)

	cSeen, recSeen, _ := newOnboardingCtx(t, http.MethodPost, "/api/v1/onboarding/agent", `{"status":"seen","step":0}`)
	auth.SetClaims(cSeen, &auth.Claims{UserID: uid})
	cSeen.Params = gin.Params{{Key: "key", Value: "agent"}}
	h.PostOnboarding(cSeen)
	if recSeen.Code != http.StatusOK {
		t.Fatalf("post seen after done: want 200, got %d", recSeen.Code)
	}

	var status string
	_ = pool.QueryRow(context.Background(),
		`SELECT status FROM user_onboarding WHERE user_sub=$1 AND onboarding_key='agent'`, uid.String()).Scan(&status)
	if status != "done" {
		t.Fatalf("done must not downgrade to seen, got %q", status)
	}
}

func TestOnboarding_UnknownKey400(t *testing.T) {
	pool := testOnboardingPool(t)
	h := &Handler{pool: pool}
	c, rec, _ := newOnboardingCtx(t, http.MethodPost, "/api/v1/onboarding/nope", `{"status":"seen","step":0}`)
	c.Params = gin.Params{{Key: "key", Value: "nope"}}
	h.PostOnboarding(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown key: want 400, got %d", rec.Code)
	}
}
```

Add `"strings"` to the import block (used by `newOnboardingCtx`).

- [ ] **Step 4: Run tests, verify they fail to compile**

Run:
```bash
cd backend && go test ./internal/api/ -run TestOnboarding -v
```
Expected: FAIL — `h.GetOnboarding`, `h.PostOnboarding` undefined.

- [ ] **Step 5: Write the handlers**

Create `backend/internal/api/onboarding.go`:

```go
package api

import (
	"net/http"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
)

/*
onboardingKeys is the whitelist of known onboarding campaign keys. It MUST stay
in sync with the frontend campaign registry in
frontend/lib/onboarding/campaigns.ts. Adding a campaign = add its key here.
*/
var onboardingKeys = map[string]bool{
	"agent": true,
}

var onboardingStatuses = map[string]bool{
	"seen":    true,
	"skipped": true,
	"done":    true,
}

type reportOnboardingRequest struct {
	Status string `json:"status"`
	Step   int    `json:"step"`
}

// @ID          getOnboarding
// @Summary     Get the caller's onboarding status map
// @Description Returns a map of onboarding_key to status ("seen"|"skipped"|"done") for the authenticated user. Empty object if none.
// @Tags        onboarding
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]string
// @Router      /onboarding [get]
func (h *Handler) GetOnboarding(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT onboarding_key, status FROM user_onboarding WHERE user_sub = $1`,
		claims.UserID.String())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read onboarding")
		return
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, status string
		if err := rows.Scan(&key, &status); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan onboarding")
			return
		}
		out[key] = status
	}
	c.JSON(http.StatusOK, out)
}

// @ID          reportOnboarding
// @Summary     Report onboarding progress for a campaign
// @Description Upserts the caller's status for one onboarding key. Monotonic: a "done" or "skipped" state is never downgraded.
// @Tags        onboarding
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       key  path     string                  true "Onboarding campaign key"
// @Param       body body     reportOnboardingRequest true "Progress"
// @Success     200  {object} map[string]string
// @Failure     400  {object} map[string]string
// @Router      /onboarding/{key} [post]
func (h *Handler) PostOnboarding(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthenticated")
		return
	}
	key := c.Param("key")
	if !onboardingKeys[key] {
		respondError(c, http.StatusBadRequest, "unknown onboarding key")
		return
	}
	var req reportOnboardingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if !onboardingStatuses[req.Status] {
		respondError(c, http.StatusBadRequest, "invalid status")
		return
	}
	if _, err := h.pool.Exec(c.Request.Context(),
		`INSERT INTO user_onboarding (user_sub, onboarding_key, status, step_reached)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_sub, onboarding_key) DO UPDATE
		 SET status       = EXCLUDED.status,
		     step_reached = GREATEST(user_onboarding.step_reached, EXCLUDED.step_reached),
		     updated_at   = NOW()
		 WHERE user_onboarding.status NOT IN ('done', 'skipped')`,
		claims.UserID.String(), key, req.Status, req.Step,
	); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to record onboarding")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
```

- [ ] **Step 6: Register the routes**

In `backend/internal/api/router.go`, right after the agent routes (lines 483-484 `api.POST("/agent/chat", ...)`), add:

```go
		api.GET("/onboarding", h.GetOnboarding)
		api.POST("/onboarding/:key", h.PostOnboarding)
```

- [ ] **Step 7: Run tests, verify pass**

Run:
```bash
cd backend && go test ./internal/api/ -run TestOnboarding -v
```
Expected: PASS (all four). If `TEST_DATABASE_URL` is unset they SKIP — set it and re-run; do not commit unverified.

- [ ] **Step 8: Regenerate OpenAPI docs + verify coverage**

Run:
```bash
cd backend && swag init -o internal/api/docs && go test ./internal/api/ -run TestOpenAPICoverage -v
```
Expected: PASS (both new routes documented).

- [ ] **Step 9: Commit**

```bash
cd /Users/alex/IdeaProjects/dada-cloud
git add backend/migrations/049_user_onboarding.sql backend/internal/api/onboarding.go backend/internal/api/onboarding_test.go backend/internal/api/router.go backend/internal/api/docs
git commit -m "feat(onboarding): per-user onboarding tracking endpoints + table"
git fetch origin main && git log --oneline origin/main..HEAD && git push origin HEAD:main
```

---

### Task 2: Frontend — dependency, api client, registry, pure selector

**Files:**
- Modify: `frontend/package.json` (+ lockfile) — add `react-joyride@3.2.0`
- Modify: `frontend/lib/api.ts` (add `onboarding` client object)
- Create: `frontend/lib/onboarding/types.ts`
- Create: `frontend/lib/onboarding/campaigns.ts`
- Create: `frontend/lib/onboarding/select.ts`

**Interfaces:**
- Consumes: `GET /api/v1/onboarding`, `POST /api/v1/onboarding/:key` from Task 1.
- Produces (Task 3 consumes):
  - `api.onboarding.status(): Promise<Record<string,string>>`
  - `api.onboarding.report(key: string, body: {status: OnboardingStatus; step: number}): Promise<{status:string}>`
  - Types `OnboardingStatus`, `OnboardingStep`, `OnboardingCampaign`
  - `ONBOARDING_CAMPAIGNS: OnboardingCampaign[]`
  - `selectPendingCampaign(campaigns, statusMap, ctx): OnboardingCampaign | null` where `ctx = { pathname: string; hasTarget: (sel: string) => boolean }`

- [ ] **Step 1: Install react-joyride 3.2.0**

Run:
```bash
cd frontend && npm install react-joyride@3.2.0 --save-exact
```
Expected: adds `"react-joyride": "3.2.0"` to dependencies; no peer-dep error against React 19.

- [ ] **Step 2: Verify it installed at the pinned version**

Run:
```bash
cd frontend && node -e "console.log(require('react-joyride/package.json').version)"
```
Expected: `3.2.0`.

- [ ] **Step 3: Add the types**

Create `frontend/lib/onboarding/types.ts`:

```ts
export type OnboardingStatus = "seen" | "skipped" | "done";

export interface OnboardingStep {
  target: string;
  titleKey: string;
  bodyKey: string;
}

export interface OnboardingCampaign {
  key: string;
  steps: OnboardingStep[];
  docsUrl: string;
  delayMs?: number;
  route?: (pathname: string) => boolean;
}
```

- [ ] **Step 4: Add the campaign registry (agent)**

Create `frontend/lib/onboarding/campaigns.ts`:

```ts
import type { OnboardingCampaign } from "./types";

/**
 * Code-side registry of onboarding campaigns. Order = priority; the first
 * pending campaign whose target exists wins. Keys MUST match the backend
 * whitelist in backend/internal/api/onboarding.go (onboardingKeys). Ship a new
 * feature = add an entry here; re-run a changed onboarding = use a new key.
 */
export const ONBOARDING_CAMPAIGNS: OnboardingCampaign[] = [
  {
    key: "agent",
    docsUrl: "/developer/agent",
    delayMs: 3000,
    steps: [
      {
        target: '[data-onboarding="agent-fab"]',
        titleKey: "onboarding.agent.title",
        bodyKey: "onboarding.agent.body",
      },
    ],
  },
];
```

- [ ] **Step 5: Add the pure selector**

Create `frontend/lib/onboarding/select.ts`:

```ts
import type { OnboardingCampaign } from "./types";

export interface SelectContext {
  pathname: string;
  hasTarget: (selector: string) => boolean;
}

/**
 * Returns the first campaign the user should be shown, or null. A campaign is
 * pending when the status map has NO entry for its key (any of seen/skipped/done
 * means already resolved -- we never re-nag). The campaign must also match its
 * optional route predicate and have its first-step target present in the DOM.
 */
export function selectPendingCampaign(
  campaigns: OnboardingCampaign[],
  statusMap: Record<string, string>,
  ctx: SelectContext
): OnboardingCampaign | null {
  for (const campaign of campaigns) {
    if (statusMap[campaign.key]) continue;
    if (campaign.route && !campaign.route(ctx.pathname)) continue;
    const first = campaign.steps[0];
    if (!first || !ctx.hasTarget(first.target)) continue;
    return campaign;
  }
  return null;
}
```

- [ ] **Step 6: Add the api client methods**

In `frontend/lib/api.ts`, add an `onboarding` object to the exported `api` (mirror the `feedback` entry near line 249). Import the type at the top of the file:

```ts
import type { OnboardingStatus } from "./onboarding/types";
```

Add inside `export const api = { ... }`:

```ts
  onboarding: {
    status: () => apiFetch<Record<string, string>>("/api/v1/onboarding"),
    report: (key: string, body: { status: OnboardingStatus; step: number }) =>
      apiFetch<{ status: string }>(`/api/v1/onboarding/${key}`, { method: "POST", body }),
  },
```

- [ ] **Step 7: Typecheck**

Run:
```bash
cd frontend && npx tsc --noEmit
```
Expected: no errors from the new files.

- [ ] **Step 8: Commit**

```bash
cd /Users/alex/IdeaProjects/dada-cloud
git add frontend/package.json frontend/package-lock.json frontend/lib/api.ts frontend/lib/onboarding
git commit -m "feat(onboarding): react-joyride dep, api client, campaign registry, selector"
git fetch origin main && git log --oneline origin/main..HEAD && git push origin HEAD:main
```

---

### Task 3: Frontend — i18n + OnboardingProvider + custom tooltip

**Files:**
- Create: `frontend/lib/i18n/console/messages/onboarding.ts`
- Modify: `frontend/lib/i18n/console/messages/index.ts` (import + spread `onboarding`)
- Create: `frontend/components/onboarding/onboarding-tooltip.tsx`
- Create: `frontend/components/onboarding/onboarding-provider.tsx`

**Interfaces:**
- Consumes: `ONBOARDING_CAMPAIGNS`, `selectPendingCampaign`, `api.onboarding` (Task 2); `useT()` from `@/lib/i18n/console/context`.
- Produces (Task 4 consumes): `OnboardingProvider` React component with prop `{ suppressed: boolean }`.

- [ ] **Step 1: Add the i18n fragment**

Create `frontend/lib/i18n/console/messages/onboarding.ts`:

```ts
import type { Messages } from "./common";

/** Feature-onboarding spotlights (react-joyride tooltips + controls). */
export const onboarding: Messages = {
  "onboarding.skip": { ru: "Пропустить", en: "Skip" },
  "onboarding.readDocs": { ru: "Читать в доке", en: "Read the docs" },
  "onboarding.gotIt": { ru: "Понятно", en: "Got it" },

  "onboarding.agent.title": { ru: "Познакомься с AI-агентом", en: "Meet the AI agent" },
  "onboarding.agent.body": {
    ru: "Чинит билды, деплоит, ставит переменные окружения. Просто опиши задачу словами.",
    en: "It fixes builds, deploys, and sets env variables. Just describe the task in words.",
  },
};
```

- [ ] **Step 2: Register the fragment**

In `frontend/lib/i18n/console/messages/index.ts`, add the import (with the others) and the spread (in the `messages` object):

```ts
import { onboarding } from "./onboarding";
```
```ts
  ...onboarding,
```

- [ ] **Step 3: Build the custom tooltip**

Create `frontend/components/onboarding/onboarding-tooltip.tsx`. Renders title + body + a docs link + Skip + primary button, all i18n. Uses react-joyride's `TooltipRenderProps`.

```tsx
"use client";
import type { TooltipRenderProps } from "react-joyride";
import { useT } from "@/lib/i18n/console/context";

export interface OnboardingTooltipExtra {
  docsUrl: string;
  onDocs: () => void;
}

export function makeOnboardingTooltip({ docsUrl, onDocs }: OnboardingTooltipExtra) {
  return function OnboardingTooltip(props: TooltipRenderProps) {
    const { step, tooltipProps, primaryProps, skipProps, isLastStep } = props;
    const { t } = useT();
    return (
      <div
        {...tooltipProps}
        className="max-w-xs rounded-lg bg-white p-4 shadow-2xl dark:bg-gray-800 dark:text-gray-100"
      >
        {step.title ? <div className="mb-1 text-sm font-semibold">{step.title}</div> : null}
        <div className="mb-3 text-sm text-gray-600 dark:text-gray-300">{step.content}</div>
        <div className="flex items-center justify-between gap-3">
          <button
            {...skipProps}
            type="button"
            className="text-xs text-gray-500 hover:underline dark:text-gray-400"
          >
            {t("onboarding.skip")}
          </button>
          <div className="flex items-center gap-3">
            <a
              href={docsUrl}
              onClick={onDocs}
              className="text-xs text-blue-600 hover:underline dark:text-blue-400"
            >
              {t("onboarding.readDocs")}
            </a>
            <button
              {...primaryProps}
              type="button"
              className="rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700"
            >
              {t("onboarding.gotIt")}
            </button>
          </div>
        </div>
      </div>
    );
  };
}
```

Note: `isLastStep` is destructured for API completeness; the agent campaign is single-step so the primary button always finishes. Keep it — future multi-step campaigns use it to switch the primary label.

- [ ] **Step 4: Build the provider**

Create `frontend/components/onboarding/onboarding-provider.tsx`:

```tsx
"use client";
import { useEffect, useRef, useState } from "react";
import { usePathname } from "next/navigation";
import Joyride, { STATUS, EVENTS, type CallBackProps, type Step } from "react-joyride";
import { ONBOARDING_CAMPAIGNS } from "@/lib/onboarding/campaigns";
import { selectPendingCampaign } from "@/lib/onboarding/select";
import type { OnboardingCampaign, OnboardingStatus } from "@/lib/onboarding/types";
import { api } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";
import { makeOnboardingTooltip } from "./onboarding-tooltip";

function isDark(): boolean {
  return typeof document !== "undefined" && document.documentElement.classList.contains("dark");
}

export function OnboardingProvider({ suppressed }: { suppressed: boolean }) {
  const pathname = usePathname();
  const { t } = useT();
  const [statusMap, setStatusMap] = useState<Record<string, string> | null>(null);
  const [active, setActive] = useState<OnboardingCampaign | null>(null);
  const [run, setRun] = useState(false);
  const firedRef = useRef(false);

  useEffect(() => {
    let alive = true;
    api.onboarding
      .status()
      .then((m) => {
        if (alive) setStatusMap(m);
      })
      .catch(() => {
        if (alive) setStatusMap({});
      });
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    if (statusMap === null || suppressed || firedRef.current) return;
    const campaign = selectPendingCampaign(ONBOARDING_CAMPAIGNS, statusMap, {
      pathname,
      hasTarget: (sel) => !!document.querySelector(sel),
    });
    if (!campaign) return;
    const timer = setTimeout(() => {
      if (firedRef.current || suppressed) return;
      const first = campaign.steps[0];
      if (!document.querySelector(first.target)) return;
      firedRef.current = true;
      setActive(campaign);
      setRun(true);
    }, campaign.delayMs ?? 3000);
    return () => clearTimeout(timer);
  }, [statusMap, suppressed, pathname]);

  function report(key: string, status: OnboardingStatus, step: number) {
    setStatusMap((prev) => ({ ...(prev ?? {}), [key]: status }));
    void api.onboarding.report(key, { status, step }).catch(() => {});
  }

  if (!active) return null;

  const steps: Step[] = active.steps.map((s) => ({
    target: s.target,
    title: t(s.titleKey),
    content: t(s.bodyKey),
    disableBeacon: true,
  }));

  function handleCallback(data: CallBackProps) {
    const { status, type, index } = data;
    if (type === EVENTS.TOUR_START) {
      report(active!.key, "seen", 0);
      return;
    }
    if (status === STATUS.SKIPPED) {
      report(active!.key, "skipped", index);
      setRun(false);
      return;
    }
    if (status === STATUS.FINISHED) {
      report(active!.key, "done", active!.steps.length);
      setRun(false);
    }
  }

  return (
    <Joyride
      steps={steps}
      run={run}
      continuous
      showSkipButton
      disableScrolling
      spotlightClicks
      callback={handleCallback}
      tooltipComponent={makeOnboardingTooltip({
        docsUrl: active.docsUrl,
        onDocs: () => report(active!.key, "seen", 0),
      })}
      styles={{
        options: {
          zIndex: 60,
          arrowColor: isDark() ? "#1f2937" : "#ffffff",
          overlayColor: "rgba(0,0,0,0.55)",
        },
      }}
    />
  );
}
```

- [ ] **Step 5: Typecheck**

Run:
```bash
cd frontend && npx tsc --noEmit
```
Expected: no errors. If react-joyride's exported member names differ under 3.2.0 (`STATUS`/`EVENTS`/`TooltipRenderProps`/`Step`/`CallBackProps`), fix the imports to match `node_modules/react-joyride/types` — do not guess; read the shipped `.d.ts`.

- [ ] **Step 6: Lint**

Run:
```bash
cd frontend && npx eslint components/onboarding lib/onboarding lib/i18n/console/messages/onboarding.ts
```
Expected: clean.

- [ ] **Step 7: Commit**

```bash
cd /Users/alex/IdeaProjects/dada-cloud
git add frontend/lib/i18n/console/messages/onboarding.ts frontend/lib/i18n/console/messages/index.ts frontend/components/onboarding
git commit -m "feat(onboarding): i18n, provider, and custom joyride tooltip"
git fetch origin main && git log --oneline origin/main..HEAD && git push origin HEAD:main
```

---

### Task 4: Wire into the console shell

**Files:**
- Modify: `frontend/app/(console)/layout.tsx` (add `data-onboarding="agent-fab"` to the FAB; mount `OnboardingProvider` inside `ConsoleShell` with the suppression signal)

**Interfaces:**
- Consumes: `OnboardingProvider` (Task 3). Suppression = agent chat panel open OR mobile nav drawer open (`chatOpen || navOpen`), both already in `ConsoleShell` scope.

- [ ] **Step 1: Tag the agent FAB as the onboarding target**

In `frontend/app/(console)/layout.tsx`, on the FAB `<button>` (currently lines 91-101, `onClick={() => setChatOpen(true)}`), add the attribute:

```tsx
            data-onboarding="agent-fab"
```

- [ ] **Step 2: Import and mount the provider**

Add the import near the other component imports (line 15 area):

```tsx
import { OnboardingProvider } from "@/components/onboarding/onboarding-provider";
```

Inside `ConsoleShell`, mount it just before the closing of the shell (next to `<AgentChatPanel .../>`), passing the suppression signal:

```tsx
        <OnboardingProvider suppressed={chatOpen || navOpen} />
```

- [ ] **Step 3: Typecheck + lint + build**

Run:
```bash
cd frontend && npx tsc --noEmit && npx eslint "app/(console)/layout.tsx" && npx next build
```
Expected: typecheck clean, lint clean, build succeeds (proves react-joyride imports resolve under Next 16 production build).

- [ ] **Step 4: Commit**

```bash
cd /Users/alex/IdeaProjects/dada-cloud
git add "frontend/app/(console)/layout.tsx"
git commit -m "feat(onboarding): mount provider in console shell, tag agent FAB target"
git fetch origin main && git log --oneline origin/main..HEAD && git push origin HEAD:main
```

---

### Task 5: Live browser smoke (the real gate)

Frontend has no unit runner and the "spotlight appears once" behavior is stateful, so it is proven live. This is mandatory before calling the feature done.

**Files:** none (verification only).

- [ ] **Step 1: Start the console dev server**

Use the preview tooling (not raw shell) to start the frontend dev server (`.claude/launch.json` console entry, or create one running `npm run dev` in `frontend` on its port). Open the console and log in with a real Keycloak account.

- [ ] **Step 2: Reset the agent onboarding row for your user**

So the spotlight can fire. Get your `user_sub` (the Keycloak subject UUID) and run:

```bash
psql "$DATABASE_URL" -c "DELETE FROM user_onboarding WHERE onboarding_key='agent' AND user_sub='<your-sub>';"
```

- [ ] **Step 3: Verify the spotlight appears**

Reload the console. After ~3s the agent FAB (bottom-right) is spotlighted with the tooltip (title + body + Skip + "Читать в доке" + "Понятно"). Take a screenshot. Confirm a `seen` row now exists:

```bash
psql "$DATABASE_URL" -c "SELECT status,step_reached FROM user_onboarding WHERE onboarding_key='agent' AND user_sub='<your-sub>';"
```
Expected: `seen`.

- [ ] **Step 4: Verify Skip writes skipped and stops re-firing**

Reset the row (Step 2), reload, wait for the spotlight, click **Skip**. Confirm:
```bash
psql "$DATABASE_URL" -c "SELECT status FROM user_onboarding WHERE onboarding_key='agent' AND user_sub='<your-sub>';"
```
Expected: `skipped`. Reload again — spotlight must NOT reappear.

- [ ] **Step 5: Verify finish writes done**

Reset the row, reload, wait for spotlight, click **Понятно** (primary). Confirm status = `done`. Reload — no spotlight.

- [ ] **Step 6: Verify suppression**

Reset the row, reload, and immediately open the agent chat panel (click the FAB) before ~3s elapse. The spotlight must not appear while the panel is open.

- [ ] **Step 7: Record the outcome**

Note the funnel query works:
```bash
psql "$DATABASE_URL" -c "SELECT onboarding_key,status,COUNT(*) FROM user_onboarding GROUP BY 1,2 ORDER BY 1,2;"
```
If everything passes, the feature is done. If Joyride misbehaves under Next 16 (RSC/StrictMode double-mount, portal/z-index, target not found), the fallback is the ~150-line custom overlay from the spec section 11 — same data model, same endpoints, swap only the renderer inside `OnboardingProvider`.

---

## Notes for the implementer
- Suppression uses `chatOpen || navOpen`. The spec also listed the command palette, but it is a transient signal-counter (`paletteOpenSignal`), not a boolean in scope, and it is only opened by an explicit user action (never present on a fresh console load), so palette suppression is omitted as YAGNI. If it ever matters, lift the palette-open boolean into `ConsoleShell` and OR it in.
- Backend tests require `TEST_DATABASE_URL` with migrations applied (same convention as `agent_chat_confirm_test.go`). Unset = SKIP, which is not proof — set it.
- Keep `onboardingKeys` (backend) and `ONBOARDING_CAMPAIGNS` (frontend) key-sets identical.
- `react-joyride` is client-only; every file importing it starts with `"use client"`.
- Do not add inline `//` comments to source (house rule) — the JSDoc/Go-doc blocks shown are fine.
