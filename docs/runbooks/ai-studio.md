# AI Studio — production runbook

**Audience:** platform operator turning AI Studio on for a real cluster.
**Code:** v1 — see [ADR-008](../adr/008-kserve.md), plan [`2026-05-22-v2-ai-studio.md`](../plans/2026-05-22-v2-ai-studio.md).

This is a checklist, not a tutorial. Run through it the first time you
deploy a console build that includes AI Studio (commit `8ac2020` and
later) into a cluster that has KServe, MLflow, and an S3-compatible
object store already running.

---

## 1. Cluster prerequisites

The console assumes the following are already installed and reachable
from the workload namespace:

- **KServe** with at least one default `ServingRuntime` per model type
  the platform will accept (`sklearn`, `xgboost`, `lightgbm`, `pytorch`,
  `tensorflow`, `huggingface`, `triton`, `custom`). The console renders
  `InferenceService`-compatible CRs and never touches the runtime
  config; if a runtime is missing, the model gets stuck `Pending` with a
  KServe-side error.
- **Crossplane** with the AIModel XRD installed. The renderer emits the
  CR; Crossplane materialises it into the underlying KServe object.
- **MLflow** reachable in-cluster on a stable URL. Optional — if MLflow
  is absent the registry browser disappears and creates fall back to
  "paste artifactURI" (S3 URI) only.
- **S3-compatible object store** for model artifacts. Each project gets
  one path prefix; the platform never grants per-project IAM credentials.
  See §3.

## 2. Console wiring

All knobs live in `helm/dada-cloud-console/values.yaml`. Override per
environment.

### Backend

```yaml
backend:
  env:
    AI_STUDIO_ENABLED: "true"          # kill-switch; "false" hides routes
    MLFLOW_BASE_URL: "http://mlflow.mlflow.svc.cluster.local:5000"
  secret:
    MLFLOW_AUTH_HEADER: ""             # e.g. "Bearer …" if MLflow is auth'd
```

The backend handles both the registry proxy and the inference proxy.
`AI_STUDIO_ENABLED=false` is a runtime kill-switch — the routes mount
unconditionally but return 404 when this flag is off. Reach for it if a
production model goes haywire and you need to lock the console down
without rolling back the build.

### gitops-agent

```yaml
gitopsAgent:
  env:
    MLFLOW_BASE_URL: ""                # required for MLflow-pinned ops
  secret:
    MLFLOW_AUTH_HEADER: ""
```

The agent resolves `<mlflow_name, mlflow_version>` to an `s3://…` source
URI at render time. Without `MLFLOW_BASE_URL` configured, MLflow-sourced
`CreateAIModel` (or `UpdateAIModelArtifact` / `PinAIModelMlflowVersion`)
operations fail with `MLFLOW_BASE_URL not configured on agent` — the
operation row carries the error, the model is not created, and the user
sees an actionable message in the operations timeline.

S3-sourced and custom-container operations work without MLflow wired.

## 3. Per-project storage prefix

The console enforces D13 path-prefix isolation: every `artifact_uri`
must start with the project's `ai_storage_prefix`. Projects with no
prefix configured cannot create models from S3 (they get a 403 with
"project has no AI storage prefix configured; ask an admin").

```sql
UPDATE projects
SET ai_storage_prefix = 's3://platform-models/<project-slug>/'
WHERE name = '<project-slug>';
```

Trailing slash matters — `s3://platform-models/foo/` accepts
`s3://platform-models/foo/iris/v1/` but rejects `s3://platform-models/foobar/`.

The MLflow registry proxy filters versions whose `source` falls outside
the project's prefix, so MLflow only shows users models they're allowed
to deploy.

## 4. Quotas and the GPU approval gate

Quotas live in `project_quotas`. Defaults if no row exists: 5 CPU
models, 0 GPU models, 100k advisory monthly inference calls.

```sql
INSERT INTO project_quotas (project_id, cpu_model_max, gpu_model_max, monthly_inference_calls)
VALUES ('<project-uuid>', 10, 2, 1000000)
ON CONFLICT (project_id) DO UPDATE
   SET cpu_model_max = EXCLUDED.cpu_model_max,
       gpu_model_max = EXCLUDED.gpu_model_max,
       monthly_inference_calls = EXCLUDED.monthly_inference_calls;
```

`gpu_model_max = 0` is the **GPU approval gate** (D12): non-admin users
who request a `gpu-*` profile land in `WaitingForApproval` instead of
being rejected. A platform-admin approves at `/admin/approvals` and the
operation transitions to `Created`, after which the gitops-agent picks
it up on the next poll.

To raise `gpu_model_max` above 0, you're saying "any developer in this
project can spin up GPUs without asking." Don't do that on a shared
cluster without alerting set up.

## 5. API-key lifecycle

Models with `auth_mode=apikey` (the default) get a fresh argon2id-hashed
key issued by the agent at create time. The plaintext is parked in
`aimodel_api_key_reveals` for **15 minutes** and consumed exactly once
via `GET /api-key?reveal=true`.

After 15 minutes the plaintext is gone and the only path back is to
rotate. There's no rotate endpoint in v1 — you delete and recreate the
model. This is documented in S6 as a known v1 gap; v2 will add a
proper `RotateAPIKey` operation.

If a backend restart loses the in-memory cache between issuance and
reveal, the user sees a 410 with "rotate the key to issue a new one."
Same path: delete + recreate.

## 6. Inference-counter caveats

The advisory monthly counter (`aimodel_inference_counters`) is bumped
**only** by traffic going through `/api/v1/projects/.../models/.../infer`
(playground + any first-party caller). Production traffic flowing
through the embedded `PublicApi` bypasses the counter — D14 chose
transparent passthrough, so we don't intercept ingress traffic.

Treat the displayed monthly call count as a floor, not a ceiling.
Real metering arrives in v2 with the metrics-ingestion pipeline.

## 7. Migrations

`006_ai_studio.sql` is forward-only and additive (new columns + new
tables, all `IF NOT EXISTS`). It depends on `005_grant_dada_user.sql`'s
default-privilege grants, but is otherwise independent of v1 schema.

If you're upgrading a cluster that's been running pre-AI-Studio
console builds, no special steps — apply 006 like any other migration.
The `ai_storage_prefix` column starts NULL, which puts every project
into "ask an admin to set my prefix" mode (the desired default).

## 8. Rollback

Each implementation phase is independently reversible:

- **Operational rollback:** set `AI_STUDIO_ENABLED=false` on the backend
  and redeploy. Routes return 404; no DB changes; no manifests touched.
  Existing AIModel CRs in the cluster keep serving.
- **Code rollback:** revert to the last pre-AI-Studio commit. The 006
  migration stays applied — its tables/columns are safe to leave behind
  since nothing else references them.
- **Wedged operation:** an op stuck in `Failed` can be retried by
  re-submitting the same payload from the UI; the gitops-agent will
  produce a new operation row with a fresh attempt. Do not edit the
  failed row.

## 9. Health checks before declaring "production"

In order, on a real cluster:

1. `helm template` and `helm lint` clean — CI gate handles this.
2. Migration 006 applied; `\dt aimodel_*` shows three new tables and
   `\d projects` shows `ai_storage_prefix`.
3. At least one project has `ai_storage_prefix` set and a row in
   `project_quotas`.
4. Backend logs show `mlflow proxy: …` lines on `/api/v1/mlflow/...`
   requests. Empty `MLFLOW_BASE_URL` returns 503 from the proxy — that's
   fine if MLflow is intentionally absent.
5. End-to-end: deploy a sklearn model from MLflow → wait for the AIModel
   CR to reach Ready → playground call returns predictions → set
   canary 50% → promote → delete. One run from the UI, watching the
   operations timeline for stuck ops.

Once that loop completes, the platform is ready to take real users.

---

**Cross-references**

- [ADR-008 — Adopt KServe as the inference platform](../adr/008-kserve.md)
- [Plan — `2026-05-22-v2-ai-studio.md`](../plans/2026-05-22-v2-ai-studio.md)
- Migration: `backend/migrations/006_ai_studio.sql`
- Renderer: `gitops-agent/internal/renderer/aimodel.go`
- MLflow client (agent): `gitops-agent/internal/mlflow/client.go`
- KServe URL routing: `backend/internal/api/inference.go`
