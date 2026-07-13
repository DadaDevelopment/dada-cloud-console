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
import { useT } from "@/lib/i18n/console/context";

type Tab = "overview" | "versions" | "access" | "playground" | "operations" | "manifests";

export default function ModelDetailPage() {
  const params = useParams<{ projectId: string; name: string }>();
  const search = useSearchParams();
  const router = useRouter();
  const [refreshTick, setRefreshTick] = useState(0);
  const { t } = useT();
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
      .catch((err) => setError(err instanceof Error ? err.message : t("models.detail.error.load")))
      .finally(() => setIsLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, envId, name, refreshTick]);

  function gotoOp() {
    setTimeout(() => setRefreshTick((v) => v + 1), 1500);
  }

  async function submitCanary(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setCanaryError(null);
    setIsCanarySubmitting(true);
    try {
      await aiModelsApi.setCanary(projectId, envId, name, canaryPercent);
      setIsCanaryOpen(false);
      gotoOp();
    } catch (err) {
      setCanaryError(err instanceof Error ? err.message : t("models.canary.error"));
    } finally {
      setIsCanarySubmitting(false);
    }
  }

  async function submitPromote(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setPromoteError(null);
    setIsPromoteSubmitting(true);
    try {
      await aiModelsApi.promote(projectId, envId, name);
      setIsPromoteOpen(false);
      gotoOp();
    } catch (err) {
      setPromoteError(err instanceof Error ? err.message : t("models.promote.error"));
    } finally {
      setIsPromoteSubmitting(false);
    }
  }

  async function submitDelete(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setDeleteError(null);
    setIsDeleteSubmitting(true);
    try {
      await aiModelsApi.delete(projectId, envId, name, deleteForce);
      setIsDeleteOpen(false);
      router.push(`/projects/${projectId}/models`);
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : t("models.delete.error"));
    } finally {
      setIsDeleteSubmitting(false);
    }
  }

  if (isLoading) {
    return <div className="flex h-64 items-center justify-center"><Spinner size="lg" /></div>;
  }
  if (error || !detail) {
    return (
      <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
        {error ?? t("models.detail.notFound")}
      </div>
    );
  }

  const model: ResourceSnapshot = detail.model;
  const s = model.summary_json as unknown as AIModelSummary;
  const canaryPct = typeof s.canary_percent === "number" ? s.canary_percent : 0;
  const canaryActive = canaryPct > 0;
  const isAdmin = role === "Owner" || role === "Admin";

  const tabs: { key: Tab; label: string; admin?: boolean }[] = [
    { key: "overview", label: t("models.tab.overview") },
    { key: "versions", label: t("models.tab.versions") },
    { key: "access", label: t("models.tab.access") },
    { key: "playground", label: t("models.tab.playground") },
    { key: "operations", label: t("models.tab.operations") },
    { key: "manifests", label: t("models.tab.manifests"), admin: true },
  ];

  return (
    <div>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
            <Link href="/projects" className="hover:text-gray-700">{t("common.crumb.projects")}</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}`} className="hover:text-gray-700">{t("common.crumb.overview")}</Link>
            <span>/</span>
            <Link href={`/projects/${projectId}/models`} className="hover:text-gray-700">{t("nav.models")}</Link>
            <span>/</span>
            <span className="font-mono text-gray-900 dark:text-gray-100">{name}</span>
          </div>
          <div className="mt-2 flex items-center gap-3">
            <h1 className="font-mono text-2xl font-bold text-gray-900 dark:text-gray-100">{name}</h1>
            <PhaseBadge phase={model.phase} />
          </div>
          <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
            {s.model_type} · {s.profile} · v{s.version ?? "—"} · stage {s.stage ?? "—"}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button
            onClick={() => { setCanaryPercent(canaryPct); setIsCanaryOpen(true); }}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-purple-300 hover:text-purple-600 transition-colors shadow-sm"
          >
            {t("models.detail.setCanary")}
          </button>
          {canaryActive && (
            <button
              onClick={() => setIsPromoteOpen(true)}
              className="inline-flex items-center gap-2 rounded-lg bg-purple-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-purple-700 transition-colors"
            >
              {t("models.detail.promote")}
            </button>
          )}
          <button
            onClick={() => { setDeleteForce(false); setIsDeleteOpen(true); }}
            className="inline-flex items-center gap-2 rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 transition-colors shadow-sm"
          >
            {t("common.delete")}
          </button>
        </div>
      </div>

      <div className="mb-6 border-b border-gray-200 dark:border-gray-800">
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
                    ? "border-blue-600 text-blue-600 dark:text-blue-400"
                    : "border-transparent text-gray-500 dark:text-gray-400 hover:border-gray-300 hover:text-gray-700"
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
        title={t("models.canary.modal.title")}
      >
        <form onSubmit={submitCanary} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {t("models.canary.label", { pct: String(canaryPercent) })}
            </label>
            <input
              type="range" min={0} max={100} step={5}
              value={canaryPercent}
              onChange={(e) => setCanaryPercent(parseInt(e.target.value, 10))}
              className="mt-2 w-full"
            />
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {t("models.canary.help")}
            </p>
          </div>
          {canaryError && <ErrorBox text={canaryError} />}
          <ModalFooter onCancel={() => setIsCanaryOpen(false)} submitting={isCanarySubmitting} submitLabel={t("common.apply")} />
        </form>
      </Modal>

      <Modal
        isOpen={isPromoteOpen}
        onClose={() => { setIsPromoteOpen(false); setPromoteError(null); }}
        title={t("models.promote.modal.title")}
      >
        <form onSubmit={submitPromote} className="space-y-4">
          <p className="text-sm text-gray-600 dark:text-gray-400">
            {t("models.promote.body")}
          </p>
          {promoteError && <ErrorBox text={promoteError} />}
          <ModalFooter onCancel={() => setIsPromoteOpen(false)} submitting={isPromoteSubmitting} submitLabel={t("models.promote.submit")} tone="purple" />
        </form>
      </Modal>

      <Modal
        isOpen={isDeleteOpen}
        onClose={() => { setIsDeleteOpen(false); setDeleteError(null); }}
        title={t("models.delete.modal.title")}
      >
        <form onSubmit={submitDelete} className="space-y-4">
          <p className="text-sm text-gray-600 dark:text-gray-400">
            {t("models.delete.body")}
          </p>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={deleteForce}
              onChange={(e) => setDeleteForce(e.target.checked)}
              className="h-4 w-4 rounded border-gray-300 dark:border-gray-700 text-red-600 dark:text-red-400 focus:ring-red-500"
            />
            <span className="text-sm text-gray-700 dark:text-gray-200">
              {t("models.delete.force")}
            </span>
          </label>
          {deleteError && <ErrorBox text={deleteError} />}
          <ModalFooter onCancel={() => setIsDeleteOpen(false)} submitting={isDeleteSubmitting} submitLabel={t("common.delete")} tone="red" />
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
  const { t } = useT();
  return (
    <div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <SpecCard label={t("models.overview.specCard.modelType")} value={s.model_type ?? "—"} />
        <SpecCard label={t("models.overview.specCard.profile")} value={s.profile ?? "—"} />
        <SpecCard label={t("models.overview.specCard.authMode")} value={s.auth_mode ?? "—"} />
        <SpecCard label={t("models.overview.specCard.stage")} value={s.stage ?? "—"} />
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.overview.source")}</h2>
          {s.artifact_uri && <Row label={t("models.overview.row.artifactUri")} value={s.artifact_uri} mono />}
          {s.container_image && <Row label={t("models.overview.row.containerImage")} value={s.container_image} mono />}
          {s.mlflow_name && <Row label={t("models.overview.row.mlflow")} value={`${s.mlflow_name} @ v${s.mlflow_version ?? "?"}`} mono />}
          {s.attached_app && <Row label={t("models.overview.row.attachedApp")} value={s.attached_app} mono />}
        </div>

        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.overview.traffic")}</h2>
          {canaryActive ? (
            <>
              <div className="flex items-center justify-between text-sm text-gray-600 dark:text-gray-400">
                <span>{t("models.overview.traffic.canary")}</span>
                <span className="font-mono font-semibold text-purple-700 dark:text-purple-300">{canaryPct}%</span>
              </div>
              <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                <div className="h-full bg-purple-500" style={{ width: `${canaryPct}%` }} />
              </div>
              <p className="mt-3 text-xs text-gray-400 dark:text-gray-500">
                {t("models.overview.traffic.canaryHelp")}
              </p>
            </>
          ) : (
            <p className="text-sm text-gray-500 dark:text-gray-400">{t("models.overview.traffic.stable")}</p>
          )}
        </div>
      </div>

      <div className="mt-6 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.overview.status")}</h2>
        <Row label={t("models.overview.row.phase")} value={model.phase ?? "Unknown"} />
        <Row label={t("models.overview.row.lastSynced")} value={new Date(model.last_synced_at).toLocaleString()} />
        {s.status && <Row label={t("models.overview.row.status")} value={s.status} />}
        {apiKeyPrefix && <Row label={t("models.overview.row.apiKeyPrefix")} value={`${apiKeyPrefix}…`} mono />}
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
  const { t } = useT();
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
      setErr(e2 instanceof Error ? e2.message : t("models.versions.error"));
    } finally {
      setSubmitting(false);
    }
  }

  const versionOps = operations.filter((o) =>
    ["UpdateAIModelArtifact", "PinAIModelMlflowVersion", "CreateAIModel"].includes(o.action)
  );

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.versions.updateArtifact")}</h2>
        <form onSubmit={submit} className="space-y-4">
          <div className="flex gap-1 rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 p-1 w-fit">
            {(["artifact", "mlflow"] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setMode(m)}
                className={`rounded-md px-3 py-1 text-xs font-medium transition-colors ${
                  mode === m ? "bg-white dark:bg-gray-900 text-gray-900 dark:text-gray-100 shadow-sm" : "text-gray-500 dark:text-gray-400 hover:text-gray-700"
                }`}
              >
                {m === "artifact" ? "S3 URI" : "MLflow pin"}
              </button>
            ))}
          </div>
          {mode === "artifact" ? (
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.versions.artifactUri.label")}</label>
              <input
                type="text" required value={artifactURI}
                onChange={(e) => setArtifactURI(e.target.value)}
                placeholder="s3://platform-models/<project>/<name>/v2"
                className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
              <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("models.versions.artifactUri.help")}</p>
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.versions.mlflowName.label")}</label>
                <input
                  type="text" required value={mlflowName}
                  onChange={(e) => setMlflowName(e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("models.versions.mlflowVersion.label")}</label>
                <input
                  type="text" required value={mlflowVersion}
                  onChange={(e) => setMlflowVersion(e.target.value)}
                  className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm font-mono text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
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
              {submitting ? <><Spinner size="sm" /> {t("models.versions.updating")}</> : t("models.versions.updateBtn")}
            </button>
          </div>
        </form>
      </div>

      <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.versions.history")}</h2>
        {versionOps.length === 0 ? (
          <p className="text-sm text-gray-500 dark:text-gray-400">{t("models.versions.noHistory")}</p>
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-800">
            {versionOps.map((op) => (
              <li key={op.id} className="flex items-center justify-between py-2 text-sm">
                <span className="text-gray-700 dark:text-gray-200">{op.action}</span>
                <span className="text-xs text-gray-400 dark:text-gray-500">{new Date(op.created_at).toLocaleString()}</span>
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
  const { t } = useT();
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
      setErr(e instanceof Error ? e.message : t("models.access.reveal.error"));
    } finally {
      setRevealing(false);
    }
  }

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.access.auth")}</h2>
        <Row label={t("models.overview.specCard.authMode")} value={s.auth_mode ?? "—"} />
        {apiKeyPrefix ? (
          <Row label={t("models.overview.row.apiKeyPrefix")} value={`${apiKeyPrefix}…`} mono />
        ) : (
          <p className="mt-2 text-sm text-gray-500 dark:text-gray-400">{t("models.access.noApiKey")}</p>
        )}
      </div>

      {s.auth_mode === "apikey" && (
        <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.access.reveal.title")}</h2>
          <p className="text-sm text-gray-600 dark:text-gray-400">
            {t("models.access.reveal.body")}
          </p>
          {revealed ? (
            <div className="mt-3">
              <pre className="overflow-x-auto rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 p-3 font-mono text-sm text-amber-900">
                {revealed}
              </pre>
              <p className="mt-2 text-xs text-amber-700 dark:text-amber-300">{t("models.access.reveal.save")}</p>
            </div>
          ) : (
            <button
              onClick={reveal}
              disabled={revealing}
              className="mt-3 inline-flex items-center gap-2 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50 transition-colors"
            >
              {revealing ? <><Spinner size="sm" /> {t("models.access.reveal.revealing")}</> : t("models.access.reveal.btn")}
            </button>
          )}
          {err && <div className="mt-3"><ErrorBox text={err} /></div>}
        </div>
      )}
    </div>
  );
}

function OperationsTab({ operations, projectId }: { operations: Operation[]; projectId: string }) {
  const { t } = useT();
  if (operations.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-12 text-center">
        <p className="text-sm text-gray-500 dark:text-gray-400">{t("models.ops.empty")}</p>
      </div>
    );
  }
  return (
    <div className="overflow-x-auto rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
      <table className="min-w-full divide-y divide-gray-200 dark:divide-gray-800">
        <thead className="bg-gray-50 dark:bg-gray-900">
          <tr>
            <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.ops.col.action")}</th>
            <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.ops.col.status")}</th>
            <th className="px-5 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.ops.col.when")}</th>
            <th className="px-5 py-3 text-right text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.ops.col.link")}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
          {operations.map((op) => (
            <tr key={op.id}>
              <td className="px-5 py-3 text-sm text-gray-900 dark:text-gray-100">{op.action}</td>
              <td className="px-5 py-3 text-sm">
                <span className="inline-flex items-center rounded-full bg-gray-100 dark:bg-gray-800 px-2 py-0.5 text-xs font-medium text-gray-700 dark:text-gray-200">
                  {op.status}
                </span>
              </td>
              <td className="px-5 py-3 text-xs text-gray-400 dark:text-gray-500">{new Date(op.created_at).toLocaleString()}</td>
              <td className="px-5 py-3 text-right">
                <Link
                  href={`/projects/${projectId}/operations?highlight=${op.id}`}
                  className="text-xs text-blue-600 dark:text-blue-400 hover:text-blue-700"
                >
                  {t("models.ops.details")}
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
  const { t } = useT();
  const lastOp = operations[0];
  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.manifests.gitops")}</h2>
        {lastOp?.git_path && <Row label={t("models.manifests.row.gitPath")} value={lastOp.git_path} mono />}
        {lastOp?.git_commit && <Row label={t("models.manifests.row.lastCommit")} value={lastOp.git_commit.slice(0, 12)} mono />}
        {lastOp?.argo_application && <Row label={t("models.manifests.row.argoApp")} value={lastOp.argo_application} mono />}
        <Row label={t("models.manifests.row.crossplane")} value={`aimodel-${model.name}`} mono />
      </div>

      <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{t("models.manifests.resolvedSpec")}</h2>
        <pre className="max-h-96 overflow-auto rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 p-3 font-mono text-xs text-gray-900 dark:text-gray-100">
{JSON.stringify(s, null, 2)}
        </pre>
      </div>
    </div>
  );
}

function SpecCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</p>
      <p className="mt-1 truncate text-sm font-medium text-gray-900 dark:text-gray-100 font-mono">{value}</p>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-gray-100 dark:border-gray-800 py-2 last:border-0">
      <span className="text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</span>
      <span className={`text-sm text-gray-900 dark:text-gray-100 break-all text-right ${mono ? "font-mono" : ""}`}>{value}</span>
    </div>
  );
}

function ErrorBox({ text }: { text: string }) {
  return (
    <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">{text}</div>
  );
}

function ModalFooter({
  onCancel, submitting, submitLabel, tone = "blue",
}: {
  onCancel: () => void; submitting: boolean; submitLabel: string;
  tone?: "blue" | "red" | "purple";
}) {
  const { t } = useT();
  const tones = {
    blue: "bg-blue-600 hover:bg-blue-700",
    red: "bg-red-600 hover:bg-red-700",
    purple: "bg-purple-600 hover:bg-purple-700",
  };
  return (
    <div className="flex justify-end gap-3 pt-2">
      <button
        type="button" onClick={onCancel}
        className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 transition-colors"
      >
        {t("common.cancel")}
      </button>
      <button
        type="submit" disabled={submitting}
        className={`inline-flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-white ${tones[tone]} disabled:cursor-not-allowed disabled:opacity-50 transition-colors`}
      >
        {submitting ? <><Spinner size="sm" /> {t("models.working")}</> : submitLabel}
      </button>
    </div>
  );
}
