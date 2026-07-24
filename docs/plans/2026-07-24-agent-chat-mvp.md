# In-console agent chat — MVP design (P1-3d phase 1)

Owner directive 2026-07-23: right collapsible panel ~1/3 screen, user says it in words, agent does it.
Server-side ReAct loop, no LLM keys in browser. This doc is grounded against code and live cluster
state on 2026-07-24; every file:line was read this cycle.

## Architecture (decided)

```
Browser panel (SSE consumer)
  → POST /api/v1/agent/chat (console backend, Gin, streams SSE)
    → ReAct loop in-process:
        LLM: LiteLLM gateway http://ai-gateway-service.argocd-prod.svc.cluster.local
             OpenAI-compatible /v1/chat/completions, model "claude"
        Tools: reused MCP GeneratedTool registry, executed via MakeHandler self-proxy
               with the CALLER'S bearer (user scope, not SA)
```

## Grounded facts the design stands on

### Tool layer (backend/internal/mcp) — reuse as-is
- Tools are generated from the embedded swagger spec: `GenerateTools(ParseSpec(api.EmbeddedSpec()))`
  (server.go:62, toolgen.go:33-54). Each `GeneratedTool` carries a ready JSON-Schema
  `InputSchema map[string]any` (toolgen.go:125-135) → OpenAI function def is a direct field mapping
  `{name, description, parameters}`. Zero schema work.
- Execution: `MakeHandler(g, backendURL, basePath)` (proxy.go:45-117) returns a closure that
  self-proxies over loopback HTTP to `/api/v1/...`, forwarding the bearer from ctx
  (proxy.go:104-106, `mcp.WithBearer` proxy.go:26-28). This is the correct reuse path — the real
  Keycloak verification + RBAC middleware run unchanged on every tool call. Do NOT bypass gin
  middleware with direct function calls.
- `mapResponse` (proxy.go:119-132) already formats results agent-friendly (4xx isError,
  5xx "transient, retry").
- Curation precedent: `default_overrides.yaml` keep-allowlist via `ApplyOverrides`
  (overrides.go:32-50). Chat ships its OWN read-only keep-list the same way.
- ReadOnly flag == method==GET (toolgen.go:60) is NOT a safe filter alone: `revealEnvVar`,
  `getDatabaseCredentials`, `getS3BucketCredentials`, `revealModelApiKey` are GET but
  secret-revealing → explicit deny even in read-only MVP.

MVP read toolset (~16): listProjects, getProject, listApps, getAppState, getAppLogs, getAppMetrics,
listDeployments, listBuilds, getBuild, listEnvVars (names only), listHostnames, listEndpoints,
listDatabases, listOperations/getOperation, searchLogs, getProjectCost, getCurrentUser.

Support ticket: `POST /api/v1/feedback` → `SubmitFeedback` (feedback.go:39-74, mig 040) already
exists as MCP tool `submitFeedback`; rename/annotate to `create_support_ticket` via overrides
(overrides.go:21-26), route field set to `agent-chat`. Caveat: it is a log, not a ticket system —
good enough for MVP (routine reads the table each cycle).

### LLM layer (ADR-015, repo /Users/alex/IdeaProjects/ai-gateway)
- Gateway = LiteLLM 1.92.0, in-cluster `ai-gateway-service.argocd-prod` :80→4000, ingress
  ai.dada-tuda.ru. One model alias `claude` → anthropic/claude-3-5-sonnet.
- Auth inbound: `sk-dada-*` key introspected via user-service, needs `ai:chat` scope. No master key.
- Provider creds are BYOK per project: encrypted in console DB, injected per request via backend
  `/internal/ai/credential/get` (ai_credentials.go, X-Internal-Token).
- **[live] Gateway is DOWN: deploy ai-gateway-deploy replicas 0/0 since ~07-22 (memory headroom
  scale-down), ingress 503.** Phase-2 prerequisite: scale back up via argo-infra values (not kubectl).
- Backend has NO LLM client and NO streaming endpoint today (no http.Flusher in prod code) — the
  chat endpoint is the first. inference.go (KServe playground proxy) is the wrong shape per ADR-015.

Open question (recommendation, not resolved): chat is a PLATFORM feature — per-project BYOK doesn't
fit. Options: (a) platform Anthropic key stored as credential for a reserved platform org and the
chat endpoint mints/holds one platform `sk-dada-*` key with ai:chat scope server-side; (b) bypass
gateway, backend calls provider directly. Recommend (a): keeps ADR-015 as the single LLM data plane,
cost-caps and ledger land in one place. Needs: platform provider key (owner may need to supply, or
reuse the key DadaAgent hub already runs on — verify where hub's key lives), credential/set for the
platform org, one minted key in backend env.

### Frontend (frontend/)
- Attach point: `app/(console)/layout.tsx:45` flex row — add right `<aside>` sibling after `<main>`
  (line 64). Panel then exists on every console page; providers (ProjectProvider, i18n) already wrap
  it (lines 102-108). Mobile: copy the navOpen drawer pattern in the same file (lines 21, 48-58).
- No Sheet/Drawer component exists; house style = hand-rolled Tailwind (components/ui/modal.tsx).
  Collapsible precedent: components/app-preview-pane.tsx (defaultOpen, chevron toggle).
- `apiFetch` (lib/api.ts:124) has a hard 30s AbortController timeout (line 136) → chat streaming
  must NOT go through apiFetch. Use raw `fetch` + `res.body.getReader()` with bearer from
  `getToken()` (lib/api.ts:98). There is zero SSE/EventSource code in the app today — chat is the
  first consumer.
- i18n: new fragment `lib/i18n/console/messages/agent-chat.ts` (pattern messages/cloud-tasks.ts),
  spread into messages/index.ts; `useT()` from lib/i18n/console/context.tsx:68.
- Context: `useProjectContext()` gives projectId/selectedEnv (lib/project-context.tsx:29-48); app
  name only in URL — parse via usePathname() like projectIdFromPath. Panel sends
  {projectId, envId, appName?} with each message so the agent starts scoped.

## Guardrails (day one, from owner brief + engine audit)
- Loop iteration limit (e.g. 10 tool calls/turn) + per-user daily cost cap (deny with friendly
  message; counter table or in-memory + audit row).
- Read tools silent; WRITE tools = confirm-card in UI (phase 3, not MVP — MVP toolset is read-only
  + create_support_ticket which is also fire-safe).
- Chat transcripts persisted (custdev ore; weekly synthesis by routine). New table
  agent_chat_messages(user_sub, org_id, project_id, role, content, tool_name?, created_at).
- SSE через ingress: backend ingress timeout 60s — chat endpoint needs proxy-read-timeout bump on
  its route (same class as upload 413 fix, do it in the same PR as the endpoint).

## Phases
1. **Design + skeleton** (this doc + panel shell + POST /api/v1/agent/chat echo-stub streaming SSE,
   no LLM): proves layout, streaming through ingress, i18n, context wiring. No gateway dependency.
2. MVP read-only: real ReAct loop against gateway (prereq: scale ai-gateway up via argo-infra,
   platform credential decision above), read toolset + create_support_ticket, transcripts, caps.
3. Write tools with confirm-cards.
4. Support agent-first after 2-3wk parallel log reading.

## M2 gates
- Phase 1: prod console shows panel; SSE stub streams through real ingress (curl + browser) without
  60s cut.
- Phase 2: real user question ("почему мой апп упал?") → agent calls getAppLogs under USER bearer →
  grounded answer in panel; transcript row persisted; cost-cap denial path exercised.
