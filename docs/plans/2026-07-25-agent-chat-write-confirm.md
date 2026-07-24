# Agent chat phase 3 — write-tools with confirm-cards

Date: 2026-07-25. Status: spec (grounded, not implemented). Parent: docs/plans/2026-07-24-agent-chat-mvp.md (phase 2 live, backend e75973d).

## Grounded facts (do not re-derive)

- Toolset registration: `backend/internal/agentchat/toolset.go` — `BuildToolset` (toolset.go:40-69) uses an explicit keep-allowlist (18 read tools + `submitFeedback` renamed `create_support_ticket`). New API operations do NOT leak in automatically. Secret-GET deny-list (toolset.go:24-29) wins over keep.
- Execution: `Toolset.Execute` (toolset.go:76-105) self-proxies via loopback with the caller's bearer — RBAC/Keycloak middleware runs normally. This holds for write tools too: agent can never do more than the user.
- Limits: `MaxToolCallsPerTurn = 10`, `maxRounds = 14` (loop.go:10-12); daily cap `AgentChatDailyMsgCap` checked in agent_chat.go:214-222.
- SSE events today: `token`, `tool_call`, `error` (not_configured/daily_cap/upstream), `done`, `heartbeat` (agent_chat.go writeSSEEvent :31-34).
- Frontend: `frontend/components/agent-chat-panel.tsx` — manual fetch+getReader SSE parser (streamChat :55-106), flat `ChatMessage[]` with `kind: message|tool_call|error`.
- Transcript: `agent_chat_messages` (mig 046) — role TEXT free-form, only final assistant text + tool rows persisted; tool rows are FILTERED OUT of history reconstruction.
- KEY CONSTRAINT: ReAct loop is stateless per HTTP request; `agentChatHistory` (agent_chat.go:91-119) rebuilds from last 20 user/assistant rows only. A confirm arriving as a separate request CANNOT recover in-flight tool_calls/results from history. Pending state must be persisted explicitly with a messages snapshot.

## Design

Turn interrupts at the first write-tool call; state freezes to DB; confirm is a separate POST that resumes the same turn.

1. Toolset split: `readTools` (silent, unchanged) + `writeKeepTools` — new explicit allowlist in toolset.go (GET-heuristic is unreliable both ways, toolgen.go:60). Phase-3 write list (low/mid risk only):
   - restartApp, triggerBuild, deployTrigger, cancelBuild, retryOperation
   - setEnvVar, deleteEnvVar
   - rollbackApp, rollbackDeployment, promoteDeployment, updateAppImage
   - updateAppProfile, updateAppStorage
   Excluded permanently from agent (destructive/irreversible): deleteProject, deleteApp, deleteDatabase, deleteModel, deleteAppServer, deletePreviewEnvironment, restoreDatabase, deleteManagedRecord, deleteMonitoring*, all secret-reveal GETs. Add read-only impact endpoints (deleteAppImpact, deleteProjectImpact, moveAppImpact) to the READ list so the agent can explain consequences without being able to act.

2. Pending persistence — new table (mig next):
   ```
   agent_chat_pending_actions(
     id UUID PK, user_sub TEXT, org_id TEXT, project_id UUID, env_id UUID,
     tool_name TEXT, args_json JSONB, tool_call_id TEXT,
     messages_snapshot JSONB,          -- full messages[] up to the write call
     status TEXT DEFAULT 'pending',    -- pending|approved|rejected|expired
     created_at, expires_at            -- TTL ~5 min
   )
   ```
   Snapshot required because history reconstruction is lossy (see key constraint). Tool results already truncated to 2000 chars (agent_chat.go:22) — snapshot size bounded by maxRounds.

3. Loop change (loop.go): on write-tool call → do NOT execute; persist pending row; emit SSE `confirm_request {action_id, tool_name, args, summary}`; end stream with `done {ok:true, awaiting_confirm:true}`.

4. New endpoint `POST /api/v1/agent/chat/confirm {action_id, decision: approve|reject}` — SSE, same machinery:
   - approve → Execute write tool under the CONFIRM request's bearer (fresh token; pending TTL < session lifetime makes staleness a non-issue), append tool result to snapshot, resume RunTurn to final answer.
   - reject → append tool message "user declined", resume loop (agent offers alternative).
   - Mark pending consumed; idempotent by action_id; 409/410 on consumed/expired.

5. Frontend: `kind: "confirm"` in ChatMessage; card renders tool_name + pretty args + Approve/Reject; click → /agent/chat/confirm via same streamChat reader; textarea blocked while a confirm is open (`awaiting_confirm` state).

6. Guardrails: max 3 write-calls/turn (sub-limit of 10); one confirm card at a time (interrupt at FIRST write call, no batching); pending TTL 5 min; RBAC re-checked at Execute time by construction (self-proxy); transcript rows for confirm_request/confirm_result (new roles, no schema change needed for transcript itself).

## M2 gate (mandatory before "done")

Real prod flow under a real bearer: ask agent to set an env var → confirm_request SSE event → pending row in DB → approve → env var actually set (authoritative: GET env var / DB row, not transport 200) → transcript rows present → reject path leaves state untouched. Plus TTL expiry path.

## Open questions

- deleteApp/deleteProject forever-excluded vs gated via existing admin-approval infra (api/approvals.go pattern) — owner call; default = excluded.
- Batch confirms — rejected for now (one card at a time).
