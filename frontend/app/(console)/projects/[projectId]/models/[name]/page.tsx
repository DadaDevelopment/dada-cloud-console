"use client";
import { useEffect, useState, FormEvent } from "react";
import { useParams, useSearchParams, useRouter } from "next/navigation";
import Link from "next/link";
import { aiModelsApi } from "@/lib/api";
import type { ResourceSnapshot, AIModelSummary, AIModelDetailResponse } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Playground } from "@/components/ai/playground";

function PhaseBadge({ phase }: { phase?: string }) {
  const p = phase ?? "";
  const lower = p.toLowerCase();
  const tone = lower === "ready"
    ? "bg-green-100 text-green-700"
    : lower === "failed"
      ? "bg-red-100 text-red-700"
      : lower === "waitingforapproval"
        ? "bg-amber-100 text-amber-700"
        : "bg-yellow-100 text-yellow-700";
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${tone}`}>
      {p || "Unknown"}
    </span>
  );
}

export default function ModelDetailPage() {
  const params = useParams<{ projectId: string; name: string }>();
  const search = useSearchParams();
  const router = useRouter();
  const { projectId, name } = params;
  const envId = search.get("envId") ?? "";

  const [detail, setDetail] = useState<AIModelDetailResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Canary modal
  const [isCanaryOpen, setIsCanaryOpen] = useState(false);
  const [canaryPercent, setCanaryPercent] = useState(0);
  const [isCanarySubmitting, setIsCanarySubmitting] = useState(false);
  const [canaryError, setCanaryError] = useState<string | null>(null);

  // Promote modal
  const [isPromoteOpen, setIsPromoteOpen] = useState(false);
  const [isPromoteSubmitting, setIsPromoteSubmitting] = useState(false);
  const [promoteError, setPromoteError] = useState<string | null>(null);

  // Delete modal
  const [isDeleteOpen, setIsDeleteOpen] = useState(false);
  const [deleteForce, setDeleteForce] = useState(false);
  const [isDeleteSubmitting, setIsDeleteSubmitting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  useEffect(() => {
    if (!envId) {
      setError("Missing envId");
      setIsLoading(false);
      return;
    }
    aiModelsApi
      .get(projectId, envId, name)
      .then((d) => setDetail(d))
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

  return (
    <div>
      <div className="mb-8 flex items-start justify-between">
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

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <SpecCard label="Model type" value={s.model_type ?? "—"} />
        <SpecCard label="Profile" value={s.profile ?? "—"} />
        <SpecCard label="Auth mode" value={s.auth_mode ?? "—"} />
        <SpecCard label="Stage" value={s.stage ?? "—"} />
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Source</h2>
          {s.artifact_uri && (
            <Row label="Artifact URI" value={s.artifact_uri} mono />
          )}
          {s.container_image && (
            <Row label="Container image" value={s.container_image} mono />
          )}
          {s.mlflow_name && (
            <Row label="MLflow" value={`${s.mlflow_name} @ v${s.mlflow_version ?? "?"}`} mono />
          )}
          {s.attached_app && (
            <Row label="Attached app" value={s.attached_app} mono />
          )}
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
              <div className="mt-1 flex items-center justify-between text-xs text-gray-400">
                <span>stable</span>
                <span>{100 - canaryPct}% stable · {canaryPct}% canary</span>
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
        {detail.api_key_prefix && (
          <Row label="API key prefix" value={`${detail.api_key_prefix}…`} mono />
        )}
      </div>

      <div className="mt-6 rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
        <div className="mb-3 flex items-center justify-between">
          <div>
            <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500">Playground</h2>
            <p className="text-xs text-gray-400">
              Routed through the backend inference proxy. Calls count toward the advisory monthly budget.
            </p>
          </div>
        </div>
        <Playground
          projectId={projectId}
          envId={envId}
          name={name}
          modelType={s.model_type}
          ready={(model.phase ?? "").toLowerCase() === "ready"}
        />
      </div>

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
          <ModalFooter
            onCancel={() => { setIsCanaryOpen(false); setCanaryError(null); }}
            submitting={isCanarySubmitting}
            submitLabel="Apply"
          />
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
          <ModalFooter
            onCancel={() => { setIsPromoteOpen(false); setPromoteError(null); }}
            submitting={isPromoteSubmitting}
            submitLabel="Promote"
            tone="purple"
          />
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
          <ModalFooter
            onCancel={() => { setIsDeleteOpen(false); setDeleteError(null); }}
            submitting={isDeleteSubmitting}
            submitLabel="Delete"
            tone="red"
          />
        </form>
      </Modal>
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
