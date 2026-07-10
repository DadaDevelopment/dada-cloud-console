# AI Models (model serving)

## What it's for

Deploy a trained model (from S3, an MLflow registry, or your own container image) as a live
inference endpoint — with canary rollout, an API key, and a built-in playground to test it —
without hand-writing Kubernetes/KServe manifests.

## How to deploy a model

1. Go to **Models** in the project nav → **Deploy model**.
2. Fill in:
   - **Name** (lowercase/digits/hyphens, e.g. `iris-classifier`).
   - **Model type** — sklearn / xgboost / lightgbm / pytorch / tensorflow / triton /
     huggingface / custom.
   - **Source**:
     - **S3** — an **Artifact URI** (e.g. `s3://platform-models/<project>/iris/v1`).
     - **MLflow** — a registered model **Name** + **Version**.
     - **Custom** — a **Container image** you built yourself.
   - **Profile** — `cpu-small` / `cpu-medium` / `gpu-t4` / `gpu-a100` (shows CPU/RAM/GPU count
     per option).
   - **Auth mode** — `apikey` / `jwt` / `public`.
   - **Attached app** and **Version label** (both optional).
3. If you pick a **GPU** profile and your project's GPU quota is currently `0`, the form warns
   you the deploy will need admin approval — see
   [AI model approvals](ai-model-approvals.md).
4. Submit — you're redirected to Operations to watch it deploy.

## Deploying straight from your MLflow registry

Instead of typing a name/version by hand, go to the account menu (top-right avatar) →
**AI Studio**. This is a cross-project registry browser: pick a project, see every registered
MLflow model with its latest version/stage/updated time, and click **Deploy vN** — it opens the
Models page with the create form pre-filled (source, name, version) for that exact version.

## Managing a deployed model

Open a model card to reach its detail page — tabs **Overview / Versions / Access / Playground /
Operations**, plus **Manifests** for Owner/Admin.

- **Set Canary** (top of the page) — drag 0–100% to send a slice of traffic to a newly pinned
  version. Once canary is above 0%, a **Promote** button appears to make it the stable 100%
  version.
- **Versions** tab — push a new artifact without redeploying: paste a new S3 URI, or pin a
  different MLflow version.
- **Access** tab — see the auth mode and (for `apikey` mode) click **Reveal** to show the full
  API key in a highlighted box with a "save it now" warning.
- **Playground** tab — send a live test request to the model straight from the console.
- **Delete** (top of the page) — has a **Force** checkbox for models stuck in a bad state.

## Gotchas

- **GPU profiles can silently need approval.** If your project's GPU quota is `0`, deploying on
  `gpu-t4`/`gpu-a100` creates a request that sits pending until an Owner/Admin approves it in
  [AI model approvals](ai-model-approvals.md) — the deploy doesn't fail, it just doesn't go live
  yet.
- **Revealing the API key is not visually marked one-time-only** — the button can be clicked
  again in a fresh page load; still treat it as sensitive and copy it immediately rather than
  leaving the tab open.
- **Manifests tab is Owner/Admin only** — Developers can deploy, canary, promote, and delete,
  but can't see the raw resolved KServe spec or GitOps commit/Argo Application details.
- The **inference call counter** on the project's quota cards is advisory only (no hard cutoff
  shown in the UI) — CPU/GPU model *count* quotas are the ones that actually block deploys.

## Not yet supported

- Editing model type/profile/auth mode after creation (only artifact/version updates, canary
  %, and delete).
- A/B testing beyond simple canary percentage (no multi-version traffic splitting).
- Deleting without at least considering **Force** for stuck models — there's no separate
  "diagnose why it's stuck" tool in the UI.
