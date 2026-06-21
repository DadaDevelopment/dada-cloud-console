"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { aiModelsApi, projectsApi } from "@/lib/api";
import type {
  ResourceSnapshot, AIModelSummary, AIModelDetailResponse, Operation, MemberRole,
} from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Playground } from "@/components/ai/playground";
import { useProjectContext } from "@/lib/project-context";
import { PhaseBadge } from "@/components/ui/phase-badge";

type Tab = "overview" | "versions" | "access" | "playground" | "operations" | "manifests";

export default function ModelDetailPage() {
  const params = useParams<{ projectId: string; name: string }>();
  const search = useSearchParams();
  const router = useRouter();
  const { projectId, name } = params;
  const { selectedEnv } = useProjectContext();
  const envId = search.get("envId") || selectedEnv?.id || "";
  const initialTab = (search.get("tab") as Tab) ?? "overview";

  const [tab, setTab] = useState<Tab>(initialTab);
  const [detail, setDetail] = useState<AIModelDetailResponse | null>(null);
  const [role, setRole] = useState<MemberRole | null>(null);
  const [operations, setOperations] = useState<Operation[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [isCanaryOpen, setIsCanaryOpen] = useState(false);
  const [canaryPercent, setCanaryPercent] = useState(0);
  const [isCanarySubmitting, setIsCanarySubmitting] = useState(false);
  const [canaryError, setCanaryError] = useState<string | null>(null);

  const [isPromoteOpen, setIsPromoteOpen] = useState(false);
  const [isPromoteSubmitting, setIsPromoteSubmitting] = useState(false);
  const [promoteError, setPromoteError] = useState<string | null>(null);

  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [deleteForce, setDeleteForce] = useState(false);
  const [isDeleteSubmitting, setIsDeleteSubmitting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  useEffect(() => {
    if (!envId) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- guard for a malformed URL; we render an error rather than crashing.
      setError("Missing envId");
      setIsLoading(false);
      return;
    }
    Promise.all([
      aiModelsApi.get(projectId, envId, name),
      projectsApi.get(projectId).then((p) => p.role).catch(() => null),
      projectsApi.operations(projectId).catch(() => ({ operations: [] })),
    ])
      .then(([d, r, ops]) => {
        setDetail(d);
        setRole(r);
        setOperations(
          (ops.operations ?? []).filter(
            (o) => o.resource_kind === "AIModel" && o.resource_name === name
          )
        );
      })
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load model"))
      .finally(() => setIsLoading(false));
  }, [projectId, envId, name]);

  function gotoOp(opId?: string) {
    setTimeout(() => {
      router.push(`/projects/${projectId}/operations${opId ? `?highlight=${opId}` : ""}`);
    }, 1500);
  }

  async function submitCanary(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setCanaryError(null);
    setIsCanarySubmitting(true);
    try {
      const r = await aiModelsApi.setCanary(projectId, envId, name, canaryPercent);
      setIsCanaryOpen(false);
      gotoOp(r.operation?.id);
    } catch (err) {
      setCanaryError(err instanceof Error ? err.message : "Failed to set canary");
    } finally {
      setIsCanarySubmitting(false);
    }
  }

  async function submitPromote(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setPromoteError(null);
    setIsPromoteSubmitting(true);
    try {
      const r = await aiModelsApi.promote(projectId, envId, name);
      setIsPromoteOpen(false);
      gotoOp(r.operation?.id);
    } catch (err) {
      setPromoteError(err instanceof Error ? err.message : "Failed to promote");
    } finally {
      setIsPromoteSubmitting(false);
    }
  }

  async function submitDelete(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setDeleteError(null);
    setIsDeleteSubmitting(true);
    try {
      const r = await aiModelsApi.delete(projectId, envId, name, deleteForce);
      setIsDeleteOpen(false);
      gotoOp(r.operation?.id);
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : "Failed to delete");
    } finally {
      setIsDeleteSubmitting(false);
    }
  }

  if (isLoading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }
  if (error || !detail) {
    return (
      <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        {error ?? "Model not found"}
      </div>
    );
  }

  const model: ResourceSnapshot = detail.model;
  const s = model.summary_json as unknown as AIModelSummary;
  const canaryPct = typeof s.canary_percent === "number" ? s.canary_percent : 0;
  const canaryActive = canaryPct > 0;
  const isAdmin = role === "Owner" || role === "Admin";

  const tabs: { key: Tab; label: string; admin?: boolean }[] = [
    { key: "overview", label: "Overview" },
    { key: "versions", label: "Versions" },
    { key: "access", label: "Access" },
    { key: "playground", label: "Playground" },
    { key: "operations", label: "Operations" },
    { key: "manifests", label: "Manifests", admin: true },
  ];

  return (
    <div>
      <div className="mb-6 flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Link href="/projects" className="hover:text-gray-700">Projects</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}`} className="hover:text-gray-700">Overview</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}/models`} className="hover:text-gray-700">AI Models</Link>
            <span>/</span>
            <span className="font-mono text-gray-900">{name}</span>
          </div>
          <div className="mt-2 flex items-center gap-3">
            <h1 className="font-mono text-2xl font-bold text-gray-900">{name}</h1>
            <PhaseBadge phase={model.phase} />
          </div>
          <p className="mt-1 text-xs text-gray-400">
            {s.model_type} · {s.profile} · v{s.version ?? "—"} · stage {s.stage ?? "—"}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { setCanaryPercent(canaryPct); setIsCanaryOpen(true); }}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:border-purple-300 hover:text-purple-600 transition-colors shadow-sm"
          >
            Set canary
          </button>
          {canaryActive && (
            <button
              onClick={() => setIsPromoteOpen(true)}
              className="inline-flex items-center gap-2 rounded-lg bg-purple-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-purple-700 transition-colors"
            >
              Promote
            </button>
          )}
          <button
            onClick={() => { setDeleteForce(false); setIsDeleteOpen(true); }}
            className="inline-flex items-center gap-2 rounded-lg border border-red-200 bg-white px-3 py-1.5 text-sm font-medium text-red-600 hover:bg-red-50 transition-colors shadow-sm"
          >
            Delete
          </button>
        </div>
      </div>

      <div className="mb-6 border-b border-gray-200">
        <nav className="-mb-px flex gap-6">
          {tabs.map((t) => {
            if (t.admin && !isAdmin) return null;
            const active = tab === t.key;
            return (
              <button
                key={t.key}
                onClick={() => setTab(t.key)}
                className={`border-b-2 px-1 py-3 text-sm font-medium transition-colors ${
                  active
                    ? "border-blue-600 text-blue-600"
                    : "border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700"
                }`}
              >
                {t.label}
              </button>
            );
          })}
        </nav>
      </div>

      {tab === "overview" && (
        <OverviewTab summary={s} model={model} apiKeyPrefix={detail.api_key_prefix} canaryPct={canaryPct} canaryActive={canaryActive} />
      )}
      {tab === "versions" && (
        <VersionsTab projectId={projectId} envId={envId} name={name} summary={s} operations={operations} onOp={gotoOp} />
      )}
      {tab === "access" && (
        <AccessTab projectId={projectId} envId={envId} name={name} summary={s} apiKeyPrefix={detail.api_key_prefix} />
      )}
      {tab === "playground" && (
        <Playground projectId={projectId} envId={envId} name={name} modelType={s.model_type} ready={(model.phase ?? "").toLowerCase() === "ready"} />
      )}
      {tab === "operations" && (
        <OperationsTab operations={operations} projectId={projectId} />
      )}
      {tab === "manifests" && isAdmin && (
        <ManifestsTab summary={s} model={model} operations={operations} />
      )}

      <Modal
        isOpen={isCanaryOpen}
        onClose={() => { setIsCanaryOpen(false); setCanaryError(null); }}
        title="Set canary traffic"
      >
        <form onSubmit={submitCanary} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700">
              Canary traffic percent <span className="font-mono text-purple-700">{canaryPercent}%</span>
            </label>
            <input
              type="range" min={0} max={100} step={5}
              value={canaryPercent}
              onChange={(e) => setCanaryPercent(parseInt(e.target.value, 10))}
              className="mt-2 w-full"
            />
            <p className="mt-1 text-xs text-gray-400">
              0% rolls back to stable. 100% is equivalent to <em>Promote</em>.
            </p>
          </div>
          {canaryError && <ErrorBox text={canaryError} />}
          <ModalFooter onCancel={() => setIsCanaryOpen(false)} submitting={isCanarySubmitting} submitLabel="Apply" />
        </form>
      </Modal>

      <Modal
        isOpen={isPromoteOpen}
        onClose={() => { setIsPromoteOpen(false); setPromoteError(null); }}
        title="Promote canary"
      >
        <form onSubmit={submitPromote} className="space-y-4">
          <p className="text-sm text-gray-600">
            Promotes the canary revision to 100% stable traffic. The previous stable revision becomes inactive.
          </p>
          {promoteError && <ErrorBox text={promoteError} />}
          <ModalFooter onCancel={() => setIsPromoteOpen(false)} submitting={isPromoteSubmitting} submitLabel="Promote" tone="purple" />
        </form>
      </Modal>

      <Modal
        isOpen={isDeleteOpen}
        onClose={() => { setIsDeleteOpen(false); setDeleteError(null); }}
        title="Delete model"
      >
        <form onSubmit={submitDelete} className="space-y-4">
          <p className="text-sm text-gray-600">
            This removes the AIModel manifest from Git. KServe will deprovision the InferenceService on the next Argo sync.
            Any attached App will need to be detached unless <em>force</em> is set.
          </p>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={deleteForce}
              onChange={(e) => setDeleteForce(e.target.checked)}
              className="h-4 w-4 rounded border-gray-300 text-red-600 focus:ring-red-500"
            />
            <span className="text-sm text-gray-700">
              Force — delete even if attached to an App (audited).
            </span>
          </label>
          {deleteError && <ErrorBox text={deleteError} />}
          <ModalFooter onCancel={() => setIsDeleteOpen(false)} submitting={isDeleteSubmitting} submitLabel="Delete" tone="red" />
        </form>
      </Modal>
    </div>
  );
}

function OverviewTab({
  summary: s, model, apiKeyPrefix, canaryPct, canaryActive,
}: {
  summary: AIModelSummary; model: ResourceSnapshot; apiKeyPrefix: string | null;
  canaryPct: number; canaryActive: boolean;
}) {
  return (
    <div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <SpecCard label="Model type" value={s.model_type ?? "—"} />
        <SpecCard label="Profile" value={s.profile ?? "—"} />
        <SpecCard label="Auth mode" value={s.auth_mode ?? "—"} />
        <SpecCard label="Stage" value={s.stage ?? "—"} />
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Source</h2>
          {s.artifact_uri && <Row label="Artifact URI" value={s.artifact_uri} mono />}
          {s.container_image && <Row label="Container image" value={s.container_image} mono />}
          {s.mlflow_name && <Row label="MLflow" value={`${s.mlflow_name} @ v${s.mlflow_version ?? "?"}`} mono />}
          {s.attached_app && <Row label="Attached app" value={s.attached_app} mono />}
        </div>

        <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Traffic</h2>
          {canaryActive ? (
            <>
              <div className="flex items-center justify-between text-sm text-gray-600">
                <span>canary → new revision</span>
                <span className="font-mono font-semibold text-purple-700">{canaryPct}%</span>
              </div>
              <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-gray-100">
                <div className="h-full bg-purple-500" style={{ width: `${canaryPct}%` }} />
              </div>
              <p className="mt-3 text-xs text-gray-400">
                <em>Promote</em> shifts 100% to canary; <em>Set canary</em> updates the split or rolls back to 0%.
              </p>
            </>
          ) : (
            <p className="text-sm text-gray-500">100% stable — no canary active.</p>
          )}
        </div>
      </div>

      <div className="mt-6 rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Status</h2>
        <Row label="Phase" value={model.phase ?? "Unknown"} />
        <Row label="Last synced" value={new Date(model.last_synced_at).toLocaleString()} />
        {s.status && <Row label="Status" value={s.status} />}
        {apiKeyPrefix && <Row label="API key prefix" value={`${apiKeyPrefix}…`} mono />}
      </div>
    </div>
  );
}

function VersionsTab({
  projectId, envId, name, summary: s, operations, onOp,
}: {
  projectId: string; envId: string; name: string;
  summary: AIModelSummary; operations: Operation[]; onOp: (id?: string) => void;
}) {
  const [mode, setMode] = useState<"artifact" | "mlflow">(s.mlflow_name ? "mlflow" : "artifact");
  const [artifactURI, setArtifactURI] = useState(s.artifact_uri ?? "");
  const [mlflowName, setMlflowName] = useState(s.mlflow_name ?? "");
  const [mlflowVersion, setMlflowVersion] = useState(s.mlflow_version ?? "");
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitting(true);
    setErr(null);
    try {
      if (mode === "artifact") {
        const r = await aiModelsApi.updateArtifact(projectId, envId, name, { artifact_uri: artifactURI });
        onOp(r.operation?.id);
      } else {
        const r = await aiModelsApi.pinMlflow(projectId, envId, name, mlflowName, mlflowVersion);
        onOp(r.operation?.id);
      }
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : "Failed to update");
    } finally {
      setSubmitting(false);
    }
  }

  const versionOps = operations.filter((o) =>
    ["UpdateAIModelArtifact", "PinAIModelMlflowVersion", "CreateAIModel"].includes(o.action)
  );

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Update artifact</h2>
        <form onSubmit={submit} className="space-y-4">
          <div className="flex gap-1 rounded-lg border border-gray-200 bg-gray-50 p-1 w-fit">
            {(["artifact", "mlflow"] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setMode(m)}
                className={`rounded-md px-3 py-1 text-xs font-medium transition-colors ${
                  mode === m ? "bg-white text-gray-900 shadow-sm" : "text-gray-500 hover:text-gray-700"
                }`}
              >
                {m === "artifact" ? "S3 URI" : "MLflow pin"}
              </button>
            ))}
          </div>
          {mode === "artifact" ? (
            <div>
              <label className="block text-sm font-medium text-gray-700">New artifact URI</label>
              <input
                type="text" required value={artifactURI}
                onChange={(e) => setArtifactURI(e.target.value)}
                placeholder="s3://platform-models/<project>/<name>/v2"
                className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
              <p className="mt-1 text-xs text-gray-400">Must start with this project&apos;s storage prefix.</p>
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-gray-700">MLflow name</label>
                <input
                  type="text" required value={mlflowName}
                  onChange={(e) => setMlflowName(e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700">Version</label>
                <input
                  type="text" required value={mlflowVersion}
                  onChange={(e) => setMlflowVersion(e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm font-mono text-gray-900 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </div>
          )}
          {err && <ErrorBox text={err} />}
          <div className="flex justify-end">
            <button
              type="submit" disabled={submitting}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
            >
              {submitting ? <><Spinner size="sm" /> Updating...</> : "Update artifact"}
            </button>
          </div>
        </form>
      </div>

      <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Version history</h2>
        {versionOps.length === 0 ? (
          <p className="text-sm text-gray-500">No version-changing operations recorded yet.</p>
        ) : (
          <ul className="divide-y divide-gray-100">
            {versionOps.map((op) => (
              <li key={op.id} className="flex items-center justify-between py-2 text-sm">
                <span className="text-gray-700">{op.action}</span>
                <span className="text-xs text-gray-400">{new Date(op.created_at).toLocaleString()}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function AccessTab({
  projectId, envId, name, summary: s, apiKeyPrefix,
}: {
  projectId: string; envId: string; name: string;
  summary: AIModelSummary; apiKeyPrefix: string | null;
}) {
  const [revealed, setRevealed] = useState<string | null>(null);
  const [revealing, setRevealing] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function reveal() {
    setRevealing(true);
    setErr(null);
    try {
      const r = await aiModelsApi.revealApiKey(projectId, envId, name);
      setRevealed(r.api_key);
    } catch (e) {
      setErr(e instanceof Error ? e.message : "Reveal failed (window may have expired)");
    } finally {
      setRevealing(false);
    }
  }

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Auth</h2>
        <Row label="Auth mode" value={s.auth_mode ?? "—"} />
        {apiKeyPrefix ? (
          <Row label="API key prefix" value={`${apiKeyPrefix}…`} mono />
        ) : (
          <p className="mt-2 text-sm text-gray-500">No API key issued (auth_mode is not <span className="font-mono">apikey</span>).</p>
        )}
      </div>

      {s.auth_mode === "apikey" && (
        <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Reveal API key (one-shot)</h2>
          <p className="text-sm text-gray-600">
            The plaintext key is parked in a 15-minute Postgres row keyed on the create operation.
            Click <em>Reveal</em> to consume the row — the key cannot be recovered after that. Rotate to issue a new one.
          </p>
          {revealed ? (
            <div className="mt-3">
              <pre className="overflow-x-auto rounded-lg border border-amber-200 bg-amber-50 p-3 font-mono text-sm text-amber-900">
                {revealed}
              </pre>
              <p className="mt-2 text-xs text-amber-700">Save this now — it will not be shown again.</p>
            </div>
          ) : (
            <button
              onClick={reveal}
              disabled={revealing}
              className="mt-3 inline-flex items-center gap-2 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50 transition-colors"
            >
              {revealing ? <><Spinner size="sm" /> Revealing...</> : "Reveal API key"}
            </button>
          )}
          {err && <div className="mt-3"><ErrorBox text={err} /></div>}
        </div>
      )}
    </div>
  );
}

function OperationsTab({ operations, projectId }: { operations: Operation[]; projectId: string }) {
  if (operations.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-gray-300 bg-gray-50 py-12 text-center">
        <p className="text-sm text-gray-500">No operations recorded for this model yet.</p>
      </div>
    );
  }
  return (
    <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
      <table className="min-w-full divide-y divide-gray-200">
        <thead className="bg-gray-50">
          <tr>
            <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Action</th>
            <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">Status</th>
            <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500">When</th>
            <th className="px-5 py-3 text-right text-xs font-semibold uppercase tracking-wide text-gray-500">Link</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {operations.map((op) => (
            <tr key={op.id}>
              <td className="px-5 py-3 text-sm text-gray-900">{op.action}</td>
              <td className="px-5 py-3 text-sm">
                <span className="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700">
                  {op.status}
                </span>
              </td>
              <td className="px-5 py-3 text-xs text-gray-400">{new Date(op.created_at).toLocaleString()}</td>
              <td className="px-5 py-3 text-right">
                <Link
                  href={`/projects/${projectId}/operations?highlight=${op.id}`}
                  className="text-xs text-blue-600 hover:text-blue-700"
                >
                  Details →
                </Link>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ManifestsTab({
  summary: s, model, operations,
}: {
  summary: AIModelSummary; model: ResourceSnapshot; operations: Operation[];
}) {
  const lastOp = operations[0];
  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">GitOps</h2>
        {lastOp?.git_path && <Row label="Git path" value={lastOp.git_path} mono />}
        {lastOp?.git_commit && <Row label="Last commit" value={lastOp.git_commit.slice(0, 12)} mono />}
        {lastOp?.argo_application && <Row label="Argo application" value={lastOp.argo_application} mono />}
        <Row label="Crossplane composite" value={`aimodel-${model.name}`} mono />
      </div>

      <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Resolved spec</h2>
        <pre className="max-h-96 overflow-auto rounded-lg border border-gray-200 bg-gray-50 p-3 font-mono text-xs text-gray-900">
{JSON.stringify(s, null, 2)}
        </pre>
      </div>
    </div>
  );
}

function SpecCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">{label}</p>
      <p className="mt-1 truncate text-sm font-medium text-gray-900 font-mono">{value}</p>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-gray-100 py-2 last:border-0">
      <span className="text-xs font-medium uppercase tracking-wide text-gray-400">{label}</span>
      <span className={`text-sm text-gray-900 break-all text-right ${mono ? "font-mono" : ""}`}>{value}</span>
    </div>
  );
}

function ErrorBox({ text }: { text: string }) {
  return (
    <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{text}</div>
  );
}

function ModalFooter({
  onCancel, submitting, submitLabel, tone = "blue",
}: {
  onCancel: () => void; submitting: boolean; submitLabel: string;
  tone?: "blue" | "red" | "purple";
}) {
  const tones = {
    blue: "bg-blue-600 hover:bg-blue-700",
    red: "bg-red-600 hover:bg-red-700",
    purple: "bg-purple-600 hover:bg-purple-700",
  };
  return (
    <div className="flex justify-end gap-3 pt-2">
      <button
        type="button" onClick={onCancel}
        className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 transition-colors"
      >
        Cancel
      </button>
      <button
        type="submit" disabled={submitting}
        className={`inline-flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-white ${tones[tone]} disabled:cursor-not-allowed disabled:opacity-50 transition-colors`}
      >
        {submitting ? <><Spinner size="sm" /> Working...</> : submitLabel}
      </button>
    </div>
  );
}
