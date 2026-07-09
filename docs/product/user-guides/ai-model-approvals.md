# AI model approvals (GPU approval queue)

## What it's for

A guardrail so GPU-backed models (`gpu-t4`, `gpu-a100`) don't silently burn expensive quota —
Owner/Admin review and approve or reject each GPU deploy request before it goes live.

## How to review pending approvals

1. Click your avatar (top-right) → **Approvals**. This is a global, cross-project page — it's
   not in any per-project nav, only in the account menu, and only appears there if you're
   Owner/Admin on at least one project.
2. Each row shows: **Project**, **Resource** (kind + name, e.g. `AIModel` / `iris-classifier`),
   **Action** (e.g. `CreateAIModel`), **Requested by**, **Age**, and a short **Summary**
   (model type, profile, and source for model-deploy requests).
3. Click **Approve** to let it proceed — takes effect immediately, no confirmation step.
4. Click **Reject** to open a modal — you **must** type a **Reason** (the submit button stays
   disabled until you do) before it will let you reject.
5. Use the search box to filter by project/resource/action/requester, and **Refresh** to pull
   the latest queue.

## Gotchas

- **This page only exists for people who are Owner/Admin on at least one project** — everyone
  else who somehow reaches `/admin/approvals` gets an access-denied message, not a 404.
- **A rejection reason is mandatory** — there's no "reject with no explanation" path, by design.
- **Approve has no confirmation dialog** — double-check the row (project + resource + summary)
  before clicking; there's no "undo" button once you approve (you'd have to have the requester
  delete/redeploy the model instead).
- Today this queue is effectively GPU-model-request-only in practice: the **Summary** column
  only knows how to describe `CreateAIModel` payloads — other resource kinds would show a blank
  summary if the underlying approval system is later extended to gate anything else.

## Not yet supported

- Notifications when a request needs approval or gets a decision — you have to visit this page
  to know something's waiting.
- Bulk approve/reject.
- Any visibility into *why* a request required approval beyond "GPU profile + project GPU quota
  was 0 at request time" (see [AI Models](ai-models.md)).
