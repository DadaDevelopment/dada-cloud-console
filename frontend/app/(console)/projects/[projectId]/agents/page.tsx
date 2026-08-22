"use client";
import { useCallback, useEffect, useState, FormEvent } from "react";
import { useParams } from "next/navigation";
import { agentsApi } from "@/lib/api";
import type {
  AgentDraft,
  AgentFieldError,
  AgentState,
  AgentToolServer,
  ResourceSnapshot,
} from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { isSettling } from "@/lib/phase";
import { timeAgo } from "@/lib/format";
import { PhaseBadge } from "@/components/ui/phase-badge";
import { ResourceZeroState } from "@/components/ui/resource-zero-state";
import { Bot } from "lucide-react";
import { useT } from "@/lib/i18n/console/context";
import { trackUxEvent } from "@/lib/ux-telemetry";
import {
  agentFormFromSnapshot,
  isConsoleOwnedAgent,
  parseEnvLines,
  EMPTY_AGENT_FORM,
  type AgentFormValues,
} from "@/lib/agents";

/**
 * Agents page.
 *
 * Two sources, deliberately not merged: the list is what was ordered through
 * the console and written to git, the per-agent state is what the runtime is
 * actually serving. A ready badge assembled from the git row would claim an
 * agent answers minutes before it does, and forever if the sync is stuck.
 */
export default function AgentsPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { t } = useT();

  const { project, selectedEnv, role, environments, loading: isLoadingEnvs } = useProjectContext();
  const selectedEnvId = selectedEnv?.id ?? "";

  const [agents, setAgents] = useState<ResourceSnapshot[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [refreshTick, setRefreshTick] = useState(0);
  const [states, setStates] = useState<Record<string, AgentState>>({});

  const [tools, setTools] = useState<AgentToolServer[]>([]);
  const [isEditorOpen, setIsEditorOpen] = useState(false);
  const [editingExisting, setEditingExisting] = useState(false);
  const [form, setForm] = useState<AgentFormValues>(EMPTY_AGENT_FORM);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<AgentFieldError[]>([]);
  const [deleting, setDeleting] = useState<ResourceSnapshot | null>(null);
  const [isDeleting, setIsDeleting] = useState(false);

  const load = useCallback(() => {
    return agentsApi.list(projectId, selectedEnvId).then((data) => setAgents(data.agents ?? []));
  }, [projectId, selectedEnvId]);

  useEffect(() => {
    /* eslint-disable react-hooks/set-state-in-effect */
    if (!selectedEnvId) {
      if (!isLoadingEnvs && environments.length === 0) setIsLoading(false);
      return;
    }
    setIsLoading(true);
    setError(null);
    /* eslint-enable react-hooks/set-state-in-effect */
    load()
      .catch((err) => setError(err instanceof Error ? err.message : t("agents.error.load")))
      .finally(() => setIsLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, selectedEnvId, isLoadingEnvs, environments.length, refreshTick]);

  useEffect(() => {
    if (!selectedEnvId) return;
    if (!isSettling(agents)) return;
    const id = setTimeout(() => {
      load().catch(() => undefined);
    }, 4000);
    return () => clearTimeout(id);
  }, [agents, load, selectedEnvId]);

  useEffect(() => {
    let cancelled = false;
    for (const agent of agents) {
      agentsApi
        .state(agent.name)
        .then((state) => {
          if (!cancelled) setStates((prev) => ({ ...prev, [agent.name]: state }));
        })
        .catch(() => undefined);
    }
    return () => {
      cancelled = true;
    };
  }, [agents]);

  useEffect(() => {
    if (!isEditorOpen) return;
    trackUxEvent("view", "agent_editor:opened");
    agentsApi
      .tools()
      .then((data) => setTools(data.tools ?? []))
      .catch(() => setTools([]));
  }, [isEditorOpen]);

  function openCreate() {
    setForm(EMPTY_AGENT_FORM);
    setEditingExisting(false);
    setFieldErrors([]);
    setSubmitError(null);
    setIsEditorOpen(true);
  }

  function openEdit(agent: ResourceSnapshot) {
    setForm(agentFormFromSnapshot(agent));
    setEditingExisting(true);
    setFieldErrors([]);
    setSubmitError(null);
    setIsEditorOpen(true);
  }

  function change(field: keyof AgentFormValues, value: string | string[]) {
    setForm((prev) => ({ ...prev, [field]: value }));
  }

  function toggleTool(name: string) {
    setForm((prev) => ({
      ...prev,
      tools: prev.tools.includes(name) ? prev.tools.filter((n) => n !== name) : [...prev.tools, name],
    }));
  }

  function errorFor(field: string): string | null {
    const hit = fieldErrors.find((e) => e.field === field || e.field.startsWith(`${field}[`));
    return hit ? hit.message : null;
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setSubmitError(null);
    setFieldErrors([]);
    setIsSubmitting(true);
    const draft: AgentDraft = {
      name: form.name.trim(),
      display_name: form.display_name.trim() || undefined,
      description: form.description.trim() || undefined,
      prompt: form.prompt,
      prompt_version: form.prompt_version.trim() || undefined,
      model_config: form.model_config.trim() || undefined,
      tools: form.tools.map((name) => ({ name })),
      env: parseEnvLines(form.env),
    };
    try {
      await agentsApi.save(projectId, selectedEnvId, draft);
      setIsEditorOpen(false);
      setForm(EMPTY_AGENT_FORM);
      setRefreshTick((v) => v + 1);
    } catch (err) {
      const api = err as Error & { fieldErrors?: AgentFieldError[] };
      if (api.fieldErrors?.length) setFieldErrors(api.fieldErrors);
      else setSubmitError(err instanceof Error ? err.message : t("agents.error.save"));
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleDelete() {
    if (!deleting) return;
    setIsDeleting(true);
    try {
      await agentsApi.remove(projectId, selectedEnvId, deleting.name);
      setDeleting(null);
      setRefreshTick((v) => v + 1);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : t("agents.error.delete"));
    } finally {
      setIsDeleting(false);
    }
  }

  const canWrite = canMutate(role);

  if (isLoadingEnvs || (!selectedEnvId && environments.length > 0)) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.projects"), href: "/projects" },
              { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
              { label: t("nav.agents") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("agents.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("agents.subtitle")}</p>
        </div>
        {canWrite && (
          <button
            onClick={openCreate}
            disabled={!selectedEnvId}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50 transition-colors"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            {t("agents.create")}
          </button>
        )}
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {isLoading ? (
        <div className="flex h-40 items-center justify-center">
          <Spinner />
        </div>
      ) : agents.length === 0 ? (
        <ResourceZeroState
          tone="violet"
          icon={<Bot className="h-8 w-8" />}
          title={t("agents.empty.title")}
          description={t("agents.empty.description")}
          cta={canWrite ? { label: t("agents.create"), onClick: openCreate, disabled: !selectedEnvId } : undefined}
          steps={[t("agents.empty.step1"), t("agents.empty.step2"), t("agents.empty.step3")]}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {agents.map((agent) => {
            const summary = (agent.summary_json ?? {}) as Record<string, unknown>;
            const state = states[agent.name];
            const consoleOwned = isConsoleOwnedAgent(agent.kind);
            const readyPods = state?.pods?.filter((p) => p.ready).length ?? 0;
            const restarts = state?.pods?.reduce((sum, p) => sum + p.restarts, 0) ?? 0;
            return (
              <div
                key={agent.id}
                className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm"
              >
                <div className="mb-3 flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <p className="truncate font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">
                      {agent.name}
                    </p>
                    <p className="mt-0.5 truncate text-xs text-gray-400 dark:text-gray-500">
                      {String(summary.display_name ?? summary.description ?? "")}
                    </p>
                  </div>
                  <PhaseBadge phase={agent.phase} />
                </div>

                <p className="text-xs font-medium text-gray-600 dark:text-gray-400">
                  {!state
                    ? t("agents.state.unknown")
                    : !state.exists
                      ? t("agents.state.pending")
                      : state.ready
                        ? t("agents.state.ready")
                        : `${t("agents.state.notReady")}${state.reason ? ` — ${state.reason}` : ""}`}
                </p>
                {state?.exists && (
                  <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {t("agents.state.pods", { ready: readyPods, total: state.pods?.length ?? 0 })}
                    {restarts > 0 ? ` · ${t("agents.state.restarts", { count: restarts })}` : ""}
                  </p>
                )}
                {state?.prompt_version && (
                  <p className="mt-1 font-mono text-xs text-gray-500 dark:text-gray-400">
                    {t("agents.state.promptVersion", { version: state.prompt_version })}
                  </p>
                )}
                {state?.traces_url && (
                  <a
                    href={state.traces_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="mt-2 inline-block text-xs font-medium text-blue-600 hover:text-blue-700"
                  >
                    {t("agents.state.traces")}
                  </a>
                )}

                <p className="mt-3 text-xs text-gray-400 dark:text-gray-500">
                  {t("common.status.synced", { ago: timeAgo(agent.last_synced_at) })}
                </p>

                {!consoleOwned && (
                  <p className="mt-2 text-xs text-amber-600 dark:text-amber-400">
                    {t("agents.gitOwned")}
                  </p>
                )}

                {canWrite && consoleOwned && (
                  <div className="mt-3 flex gap-2">
                    <button
                      onClick={() => openEdit(agent)}
                      className="rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800"
                    >
                      {t("agents.action.edit")}
                    </button>
                    <button
                      onClick={() => setDeleting(agent)}
                      className="rounded-lg border border-red-200 dark:border-red-900 px-3 py-1.5 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/40"
                    >
                      {t("agents.action.delete")}
                    </button>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      <Modal
        isOpen={isEditorOpen}
        onClose={() => { setIsEditorOpen(false); setSubmitError(null); }}
        title={editingExisting ? t("agents.modal.editTitle") : t("agents.modal.createTitle")}
      >
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
              {t("agents.modal.name")}{" "}
              <span className="text-xs font-normal text-gray-400">{t("agents.modal.nameSub")}</span>
            </label>
            <input
              value={form.name}
              onChange={(e) => change("name", e.target.value)}
              disabled={editingExisting}
              required
              className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-sm disabled:opacity-60"
            />
            {editingExisting && (
              <p className="mt-1 text-xs text-gray-400">{t("agents.modal.nameLocked")}</p>
            )}
            {errorFor("name") && <p className="mt-1 text-xs text-red-600">{errorFor("name")}</p>}
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
              {t("agents.modal.displayName")}
            </label>
            <input
              value={form.display_name}
              onChange={(e) => change("display_name", e.target.value)}
              className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
              {t("agents.modal.description")}
            </label>
            <input
              value={form.description}
              onChange={(e) => change("description", e.target.value)}
              className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
              {t("agents.modal.prompt")}
            </label>
            <textarea
              value={form.prompt}
              onChange={(e) => change("prompt", e.target.value)}
              required
              rows={8}
              className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-sm"
            />
            <p className="mt-1 text-xs text-gray-400">{t("agents.modal.promptHint")}</p>
            {errorFor("prompt") && <p className="mt-1 text-xs text-red-600">{errorFor("prompt")}</p>}
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
                {t("agents.modal.promptVersion")}
              </label>
              <input
                value={form.prompt_version}
                onChange={(e) => change("prompt_version", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-sm"
              />
              <p className="mt-1 text-xs text-gray-400">{t("agents.modal.promptVersionHint")}</p>
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
                {t("agents.modal.modelConfig")}
              </label>
              <input
                value={form.model_config}
                onChange={(e) => change("model_config", e.target.value)}
                className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-sm"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
              {t("agents.modal.tools")}
            </label>
            {tools.length === 0 ? (
              <p className="mt-1 text-xs text-gray-400">{t("agents.modal.toolsEmpty")}</p>
            ) : (
              <div className="mt-2 max-h-48 space-y-2 overflow-y-auto rounded-lg border border-gray-200 dark:border-gray-800 p-3">
                {tools.map((tool) => (
                  <label key={tool.name} className="flex items-start gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={form.tools.includes(tool.name)}
                      onChange={() => toggleTool(tool.name)}
                      className="mt-1"
                    />
                    <span className="min-w-0">
                      <span className="font-mono text-xs font-semibold text-gray-900 dark:text-gray-100">
                        {tool.name}
                      </span>
                      <span className="ml-2 text-xs text-gray-500 dark:text-gray-400">
                        {tool.ready
                          ? t("agents.modal.toolDiscovered", { count: tool.discovered_tools?.length ?? 0 })
                          : t("agents.modal.toolNotReady")}
                      </span>
                      {tool.description && (
                        <span className="block text-xs text-gray-400">{tool.description}</span>
                      )}
                    </span>
                  </label>
                ))}
              </div>
            )}
            {errorFor("tools") && <p className="mt-1 text-xs text-red-600">{errorFor("tools")}</p>}
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
              {t("agents.modal.env")}
            </label>
            <textarea
              value={form.env}
              onChange={(e) => change("env", e.target.value)}
              rows={4}
              placeholder="KEY=value"
              className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-sm"
            />
            <p className="mt-1 text-xs text-gray-400">{t("agents.modal.envHint")}</p>
            {errorFor("env") && <p className="mt-1 text-xs text-red-600">{errorFor("env")}</p>}
          </div>

          {submitError && (
            <div className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-700 dark:text-red-300">
              {submitError}
            </div>
          )}

          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={() => setIsEditorOpen(false)}
              className="rounded-lg border border-gray-200 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300"
            >
              {t("agents.modal.cancel")}
            </button>
            <button
              type="submit"
              disabled={isSubmitting}
              className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {isSubmitting ? t("agents.modal.saving") : t("agents.modal.save")}
            </button>
          </div>
        </form>
      </Modal>

      <Modal isOpen={deleting !== null} onClose={() => setDeleting(null)} title={t("agents.delete.title")}>
        <p className="text-sm text-gray-600 dark:text-gray-400">
          {t("agents.delete.confirm", { name: deleting?.name ?? "" })}
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <button
            onClick={() => setDeleting(null)}
            className="rounded-lg border border-gray-200 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            {t("agents.modal.cancel")}
          </button>
          <button
            onClick={handleDelete}
            disabled={isDeleting}
            className="rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-50"
          >
            {t("agents.delete.submit")}
          </button>
        </div>
      </Modal>
    </div>
  );
}
