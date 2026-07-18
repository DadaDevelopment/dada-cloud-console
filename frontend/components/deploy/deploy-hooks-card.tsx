"use client";

import { useCallback, useEffect, useState } from "react";
import { deployHooksApi } from "@/lib/api";
import type { DeployHook, DeployHookCreated } from "@/lib/types";
import { useT } from "@/lib/i18n/console/context";
import { CopyButton } from "@/components/ui/copy-button";
import { Spinner } from "@/components/ui/spinner";
import { timeAgo } from "@/lib/format";
import { githubActionsStep, deployCurl } from "@/lib/deploy-snippet";

/**
 * "Deploy from CI" card on the app detail page. Manages deploy-hook tokens that let
 * an external CI (GitHub Actions, or plain curl) push a new image without console
 * access. Follows the reveal-once pattern used elsewhere for API keys / S3
 * credentials: the plaintext token is shown exactly once, right after creation.
 */
export function DeployHooksCard({
  projectId,
  envId,
  appName,
  canMutate,
}: {
  projectId: string;
  envId: string;
  appName: string;
  canMutate: boolean;
}) {
  const { t } = useT();
  const [hooks, setHooks] = useState<DeployHook[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [newName, setNewName] = useState("");
  const [isCreating, setIsCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [justCreated, setJustCreated] = useState<DeployHookCreated | null>(null);

  const [revokingId, setRevokingId] = useState<string | null>(null);

  const load = useCallback(() => {
    setIsLoading(true);
    deployHooksApi
      .list(projectId, envId, appName)
      .then((data) => {
        setHooks(data ?? []);
        setError(null);
      })
      .catch((e) => setError(e instanceof Error ? e.message : t("deployHooks.error.load")))
      .finally(() => setIsLoading(false));
  }, [projectId, envId, appName, t]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleCreate() {
    setIsCreating(true);
    setCreateError(null);
    try {
      const created = await deployHooksApi.create(projectId, envId, appName, newName.trim() || undefined);
      setJustCreated(created);
      setNewName("");
      setHooks((prev) => [
        {
          id: created.id,
          name: created.name,
          token_prefix: created.token_prefix,
          created_at: created.created_at,
          last_used_at: null,
          revoked_at: null,
        },
        ...prev,
      ]);
    } catch (e) {
      setCreateError(e instanceof Error ? e.message : t("deployHooks.error.create"));
    } finally {
      setIsCreating(false);
    }
  }

  async function handleRevoke(hookId: string) {
    if (!window.confirm(t("deployHooks.revoke.confirm"))) return;
    setRevokingId(hookId);
    try {
      await deployHooksApi.revoke(projectId, envId, appName, hookId);
      setHooks((prev) =>
        prev.map((h) => (h.id === hookId ? { ...h, revoked_at: new Date().toISOString() } : h))
      );
    } catch (e) {
      window.alert(e instanceof Error ? e.message : t("deployHooks.error.revoke"));
    } finally {
      setRevokingId(null);
    }
  }

  return (
    <section>
      <div className="mb-4">
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("deployHooks.title")}</h2>
        <p className="text-sm text-gray-400 dark:text-gray-500">{t("deployHooks.subtitle")}</p>
      </div>

      {error && (
        <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      {canMutate && (
        <div className="mb-4 flex flex-col gap-2 sm:flex-row sm:items-center">
          <input
            type="text"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder={t("deployHooks.name.placeholder")}
            className="block w-full max-w-xs rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
          <button
            type="button"
            onClick={handleCreate}
            disabled={isCreating}
            className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
          >
            {isCreating ? (
              <>
                <Spinner size="sm" /> {t("deployHooks.creating")}
              </>
            ) : (
              t("deployHooks.create")
            )}
          </button>
        </div>
      )}

      {createError && (
        <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {createError}
        </div>
      )}

      {justCreated && (
        <div className="mb-4 rounded-xl border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 p-5">
          <p className="text-sm font-semibold text-amber-900 dark:text-amber-200">{t("deployHooks.created.title")}</p>
          <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">{t("deployHooks.created.warning")}</p>

          <div className="mt-3 flex items-center gap-2">
            <code className="flex-1 truncate rounded-md border border-amber-200 dark:border-amber-800 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-xs text-amber-900 dark:text-amber-200">
              {justCreated.token}
            </code>
            <CopyButton value={justCreated.token} label={t("common.copy")} />
          </div>

          <div className="mt-4">
            <p className="text-xs font-semibold uppercase tracking-wide text-amber-800 dark:text-amber-400">
              {t("deployHooks.snippet.actionTitle")}
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">{t("deployHooks.snippet.actionHint")}</p>
            <div className="relative mt-1.5">
              <pre className="overflow-x-auto rounded-lg border border-gray-800 bg-gray-900 p-3 pr-20 font-mono text-xs text-gray-100">
                {githubActionsStep()}
              </pre>
              <div className="absolute right-2 top-2">
                <CopyButton value={githubActionsStep()} label={t("common.copy")} />
              </div>
            </div>
          </div>

          <div className="mt-4">
            <p className="text-xs font-semibold uppercase tracking-wide text-amber-800 dark:text-amber-400">
              {t("deployHooks.snippet.curlTitle")}
            </p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">{t("deployHooks.snippet.curlHint")}</p>
            <div className="relative mt-1.5">
              <pre className="overflow-x-auto rounded-lg border border-gray-800 bg-gray-900 p-3 pr-20 font-mono text-xs text-gray-100">
                {deployCurl(justCreated.base_url)}
              </pre>
              <div className="absolute right-2 top-2">
                <CopyButton value={deployCurl(justCreated.base_url)} label={t("common.copy")} />
              </div>
            </div>
          </div>

          <p className="mt-3 text-xs text-amber-700 dark:text-amber-400">{t("deployHooks.snippet.secretHint")}</p>

          <button
            type="button"
            onClick={() => setJustCreated(null)}
            className="mt-4 text-xs font-medium text-amber-800 dark:text-amber-300 hover:underline"
          >
            {t("deployHooks.created.done")}
          </button>
        </div>
      )}

      {isLoading ? (
        <div className="flex h-20 items-center justify-center">
          <Spinner />
        </div>
      ) : hooks.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-8">
          <p className="text-sm text-gray-400 dark:text-gray-500">{t("deployHooks.empty")}</p>
        </div>
      ) : (
        <div className="space-y-3">
          {hooks.map((hook) => (
            <div
              key={hook.id}
              className="flex items-center justify-between gap-3 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm"
            >
              <div className="min-w-0">
                <p className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">
                  {hook.name || t("deployHooks.unnamed")}
                </p>
                <p className="mt-0.5 truncate font-mono text-xs text-gray-400 dark:text-gray-500">
                  {hook.token_prefix}…
                </p>
                <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
                  {t("deployHooks.createdAt", { ago: timeAgo(hook.created_at) })}
                  {" · "}
                  {hook.last_used_at
                    ? t("deployHooks.lastUsed", { ago: timeAgo(hook.last_used_at) })
                    : t("deployHooks.neverUsed")}
                </p>
              </div>
              {hook.revoked_at ? (
                <span className="shrink-0 rounded-full bg-gray-100 dark:bg-gray-800 px-2.5 py-1 text-xs font-medium text-gray-500 dark:text-gray-400">
                  {t("deployHooks.revoked")}
                </span>
              ) : (
                canMutate && (
                  <button
                    type="button"
                    onClick={() => handleRevoke(hook.id)}
                    disabled={revokingId === hook.id}
                    className="shrink-0 rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30 disabled:opacity-50 transition-colors"
                  >
                    {t("deployHooks.revoke")}
                  </button>
                )
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
