"use client";
import { useEffect, useState } from "react";
import { agentsApi } from "@/lib/api";
import type { AgentPromptSource, AgentPromptSourceSyncResult } from "@/lib/types";
import { Spinner } from "@/components/ui/spinner";
import { timeAgo } from "@/lib/format";
import { useT } from "@/lib/i18n/console/context";

interface PromptSourceSectionProps {
  projectId: string;
  envId: string;
  agentName: string;
  editingExisting: boolean;
  canWrite: boolean;
  source: AgentPromptSource | null;
  onSourceChange: (source: AgentPromptSource | null) => void;
}

const inputClass =
  "mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-sm disabled:opacity-60";
const buttonClass =
  "shrink-0 rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-1.5 text-xs font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50";
const dangerButtonClass =
  "shrink-0 rounded-lg border border-red-200 dark:border-red-900 px-3 py-1.5 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/40 disabled:opacity-50";

/**
 * Prompt source block of the agent editor.
 *
 * Loads the source for an existing agent, offers the connect form when there is
 * none, and otherwise shows what the last sync read: commit, status, the prompt
 * read-only with its version, and each skill with its size. The parent hides
 * its own prompt fields while `source` is set, since the repo owns them.
 *
 * Mount it with `key={agentName}` so the local state starts fresh per agent;
 * `onSourceChange` must be referentially stable (a state setter is fine).
 */
export function PromptSourceSection({
  projectId,
  envId,
  agentName,
  editingExisting,
  canWrite,
  source,
  onSourceChange,
}: PromptSourceSectionProps) {
  const { t } = useT();
  const [loading, setLoading] = useState(editingExisting && agentName !== "");
  const [repo, setRepo] = useState("");
  const [ref, setRef] = useState("main");
  const [path, setPath] = useState("");
  const [busy, setBusy] = useState<"connect" | "sync" | "remove" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [lastSync, setLastSync] = useState<AgentPromptSourceSyncResult | null>(null);
  const [openSkill, setOpenSkill] = useState<string | null>(null);

  useEffect(() => {
    if (!editingExisting || !agentName) return;
    let cancelled = false;
    agentsApi.promptSource
      .get(projectId, envId, agentName)
      .then((data) => {
        if (!cancelled) onSourceChange(data);
      })
      .catch(() => {
        if (!cancelled) onSourceChange(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [projectId, envId, agentName, editingExisting, onSourceChange]);

  async function connect() {
    setBusy("connect");
    setError(null);
    try {
      const res = await agentsApi.promptSource.set(projectId, envId, agentName, {
        repo_full_name: repo.trim(),
        ref: ref.trim() || undefined,
        path: path.trim() || undefined,
      });
      onSourceChange(res.source);
      setLastSync(res.sync);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("agents.modal.source.errorSet"));
    } finally {
      setBusy(null);
    }
  }

  async function syncNow() {
    setBusy("sync");
    setError(null);
    try {
      const res = await agentsApi.promptSource.sync(projectId, envId, agentName);
      onSourceChange(res.source);
      setLastSync(res.sync);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("agents.modal.source.errorSync"));
    } finally {
      setBusy(null);
    }
  }

  async function disconnect() {
    setBusy("remove");
    setError(null);
    try {
      await agentsApi.promptSource.remove(projectId, envId, agentName);
      onSourceChange(null);
      setLastSync(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("agents.modal.source.errorRemove"));
    } finally {
      setBusy(null);
    }
  }

  const label = (
    <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">
      {t("agents.modal.source.label")}
    </label>
  );

  if (!editingExisting) {
    return (
      <div>
        {label}
        <p className="mt-1 text-xs text-gray-400">{t("agents.modal.source.saveFirst")}</p>
      </div>
    );
  }

  if (loading) {
    return (
      <div>
        {label}
        <div className="mt-2 flex items-center gap-2 text-xs text-gray-400">
          <Spinner size="sm" />
          {t("agents.modal.source.checking")}
        </div>
      </div>
    );
  }

  if (!source) {
    return (
      <div>
        {label}
        <p className="mt-1 text-xs text-gray-400">{t("agents.modal.source.intro")}</p>
        <div className="mt-2 grid gap-3 sm:grid-cols-3">
          <div className="sm:col-span-3">
            <label className="block text-xs text-gray-500">{t("agents.modal.source.repo")}</label>
            <input
              value={repo}
              onChange={(e) => setRepo(e.target.value)}
              placeholder={t("agents.modal.source.repoPlaceholder")}
              disabled={!canWrite || busy !== null}
              className={inputClass}
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500">{t("agents.modal.source.ref")}</label>
            <input value={ref} onChange={(e) => setRef(e.target.value)} disabled={!canWrite || busy !== null} className={inputClass} />
          </div>
          <div className="sm:col-span-2">
            <label className="block text-xs text-gray-500">{t("agents.modal.source.path")}</label>
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder={`agents/${agentName}`}
              disabled={!canWrite || busy !== null}
              className={inputClass}
            />
          </div>
        </div>
        {canWrite && (
          <button type="button" onClick={connect} disabled={busy !== null || !repo.trim()} className={`mt-2 ${buttonClass}`}>
            {busy === "connect" ? t("agents.modal.source.connecting") : t("agents.modal.source.connect")}
          </button>
        )}
        {error && <p className="mt-1 text-xs text-red-600">{error}</p>}
      </div>
    );
  }

  const statusClass =
    source.last_sync_status === "ok"
      ? "text-green-700 dark:text-green-400"
      : source.last_sync_status === "error"
        ? "text-red-600 dark:text-red-400"
        : "text-gray-500";

  return (
    <div>
      {label}
      <div className="mt-2 space-y-3 rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-3">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0 text-xs text-gray-600 dark:text-gray-400">
            <div className="font-mono text-sm text-gray-800 dark:text-gray-200">
              {source.repo_full_name}@{source.ref}/{source.path}
            </div>
            <div className="mt-1">
              {source.resolved_sha
                ? `${t("agents.modal.source.sha")} ${source.resolved_sha.slice(0, 12)}`
                : t("agents.modal.source.neverSynced")}
              {source.synced_at && ` / ${t("agents.modal.source.syncedAt", { when: timeAgo(source.synced_at) })}`}
              {source.last_checked_at && ` / ${t("agents.modal.source.checkedAt", { when: timeAgo(source.last_checked_at) })}`}
            </div>
            <div className={`mt-1 font-medium ${statusClass}`}>{t(`agents.modal.source.status.${source.last_sync_status}`)}</div>
            {source.last_sync_error && (
              <pre className="mt-1 whitespace-pre-wrap break-words font-mono text-xs text-red-600 dark:text-red-400">
                {source.last_sync_error}
              </pre>
            )}
            {lastSync && !lastSync.error && (
              <div className="mt-1 text-gray-500">
                {lastSync.changed
                  ? t("agents.modal.source.synced", { version: lastSync.version, files: lastSync.files, bytes: lastSync.bytes })
                  : t("agents.modal.source.unchanged")}
              </div>
            )}
          </div>
          {canWrite && (
            <div className="flex shrink-0 gap-2">
              <button type="button" onClick={syncNow} disabled={busy !== null} className={buttonClass}>
                {busy === "sync" ? t("agents.modal.source.syncing") : t("agents.modal.source.syncNow")}
              </button>
              <button type="button" onClick={disconnect} disabled={busy !== null} className={dangerButtonClass}>
                {busy === "remove" ? t("agents.modal.source.disconnecting") : t("agents.modal.source.disconnect")}
              </button>
            </div>
          )}
        </div>
        {error && <p className="text-xs text-red-600">{error}</p>}

        {source.synced_at && (
          <div>
            <div className="flex items-baseline justify-between gap-2">
              <span className="text-xs font-medium text-gray-700 dark:text-gray-300">{t("agents.modal.prompt")}</span>
              <span className="font-mono text-xs text-gray-500">
                {t("agents.modal.source.promptVersion", { version: source.prompt_version })} / {t("agents.modal.source.bytes", { bytes: source.prompt_bytes })}
              </span>
            </div>
            <pre className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 dark:bg-gray-950 p-3 font-mono text-xs text-gray-800 dark:text-gray-200">
              {source.prompt}
            </pre>
          </div>
        )}

        {source.synced_at && (
          <div>
            <span className="text-xs font-medium text-gray-700 dark:text-gray-300">{t("agents.modal.source.skills")}</span>
            {source.skills.length === 0 ? (
              <p className="mt-1 text-xs text-gray-400">{t("agents.modal.source.noSkills")}</p>
            ) : (
              <ul className="mt-1 divide-y divide-gray-100 dark:divide-gray-800 rounded-lg border border-gray-100 dark:border-gray-800">
                {source.skills.map((skill) => (
                  <li key={skill.name}>
                    <button
                      type="button"
                      onClick={() => setOpenSkill(openSkill === skill.name ? null : skill.name)}
                      className="flex w-full items-center justify-between px-3 py-1.5 text-left font-mono text-xs text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800"
                    >
                      <span>{skill.name}</span>
                      <span className="text-gray-400">{t("agents.modal.source.bytes", { bytes: skill.bytes })}</span>
                    </button>
                    {openSkill === skill.name && (
                      <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words border-t border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-950 px-3 py-2 font-mono text-xs text-gray-800 dark:text-gray-200">
                        {skill.content}
                      </pre>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
