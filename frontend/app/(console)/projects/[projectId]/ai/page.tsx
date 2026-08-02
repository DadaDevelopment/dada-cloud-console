"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useParams } from "next/navigation";
import { aiGatewayApi } from "@/lib/api";
import type {
  AICatalogResponse,
  AIGatewayKey,
  AIGatewayKeyCreated,
  AIProviderCredential,
  AIRoutingMode,
  AIRoutingSettings,
  ProjectAIUsage,
} from "@/lib/types";
import { useProjectContext } from "@/lib/project-context";
import { canMutate } from "@/lib/rbac";
import { useT } from "@/lib/i18n/console/context";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { Spinner } from "@/components/ui/spinner";
import { CopyButton } from "@/components/ui/copy-button";
import { timeAgo } from "@/lib/format";
import { pythonSnippet, nodeSnippet, curlSnippet } from "@/lib/ai-snippet";

type SnippetLang = "python" | "node" | "curl";

const KEY_PLACEHOLDER = "sk-dada-ai-...";

/**
 * Project LLM-providers page. The product here is a single working
 * configuration: an OpenAI-compatible base_url plus a project key. The page
 * opens on the one decision everything else follows from -- whose provider key
 * pays -- because a user who has no key of their own used to land on a form
 * demanding one and leave. After that it is ordered as the things a developer
 * must actually do, and the quickstart snippet is rendered with the real values
 * so it is copy-paste-runnable rather than a template to fill in.
 */

/**
 * Which side of the chooser to show on arrival. A project already routing on
 * our key, or one that has keys of its own, has answered the question; anyone
 * else sees the two cards.
 */
function initialView(mode: AIRoutingMode, credentialCount: number): AIRoutingMode | null {
  if (mode === "platform") return "platform";
  if (credentialCount > 0) return "byok";
  return null;
}
export default function ProjectAIPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;
  const { t } = useT();
  const { project, role } = useProjectContext();
  const canWrite = canMutate(role);

  const [catalog, setCatalog] = useState<AICatalogResponse | null>(null);
  const [keys, setKeys] = useState<AIGatewayKey[]>([]);
  const [credentials, setCredentials] = useState<AIProviderCredential[]>([]);
  const [usage, setUsage] = useState<ProjectAIUsage | null>(null);
  const [usageDays, setUsageDays] = useState<7 | 30>(7);
  const [routing, setRouting] = useState<AIRoutingSettings | null>(null);
  const [view, setView] = useState<AIRoutingMode | null>(null);
  const [isSwitching, setIsSwitching] = useState(false);
  const [routingError, setRoutingError] = useState<string | null>(null);

  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [newKeyName, setNewKeyName] = useState("");
  const [isCreatingKey, setIsCreatingKey] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [justCreated, setJustCreated] = useState<AIGatewayKeyCreated | null>(null);
  const [revokingId, setRevokingId] = useState<string | null>(null);

  const [lang, setLang] = useState<SnippetLang>("python");

  const [editingProvider, setEditingProvider] = useState<string | null>(null);
  const [credKey, setCredKey] = useState("");
  const [credBase, setCredBase] = useState("");
  const [isSavingCred, setIsSavingCred] = useState(false);
  const [credError, setCredError] = useState<string | null>(null);

  const load = useCallback(() => {
    Promise.all([
      aiGatewayApi.catalog(),
      aiGatewayApi.listKeys(projectId),
      aiGatewayApi.listCredentials(projectId),
      aiGatewayApi.routing(projectId),
    ])
      .then(([cat, keyResp, credResp, routeResp]) => {
        setCatalog(cat);
        setKeys(keyResp.keys ?? []);
        setCredentials(credResp.credentials ?? []);
        setRouting(routeResp);
        setView((prev) => prev ?? initialView(routeResp.mode, credResp.credentials?.length ?? 0));
        setError(null);
      })
      .catch((e) => setError(e instanceof Error ? e.message : t("ai.keys.error.load")))
      .finally(() => setIsLoading(false));
  }, [projectId, t]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    aiGatewayApi
      .usage(projectId, usageDays)
      .then(setUsage)
      .catch(() => setUsage(null));
  }, [projectId, usageDays]);

  const baseURL = catalog?.base_url ?? justCreated?.base_url ?? "";
  const configuredProviders = useMemo(
    () => new Set(credentials.map((c) => c.provider)),
    [credentials]
  );

  const defaultModel = useMemo(() => {
    const models = catalog?.models ?? [];
    const usable = models.find((m) => m.kind === "chat" && configuredProviders.has(m.provider));
    return usable?.alias ?? models.find((m) => m.kind === "chat")?.alias ?? "gpt-4o-mini";
  }, [catalog, configuredProviders]);

  const snippetKey = justCreated?.key ?? KEY_PLACEHOLDER;
  const snippet = useMemo(() => {
    if (lang === "node") return nodeSnippet(baseURL, snippetKey, defaultModel);
    if (lang === "curl") return curlSnippet(baseURL, snippetKey, defaultModel);
    return pythonSnippet(baseURL, snippetKey, defaultModel);
  }, [lang, baseURL, snippetKey, defaultModel]);

  async function handleSetRouting(mode: AIRoutingMode) {
    setIsSwitching(true);
    setRoutingError(null);
    try {
      const next = await aiGatewayApi.setRouting(projectId, mode);
      setRouting(next);
      setView(next.mode);
    } catch (e) {
      setRoutingError(e instanceof Error ? e.message : t("ai.mode.error.save"));
    } finally {
      setIsSwitching(false);
    }
  }

  async function handleCreateKey() {
    setIsCreatingKey(true);
    setCreateError(null);
    try {
      const created = await aiGatewayApi.createKey(projectId, newKeyName.trim() || undefined);
      setJustCreated(created);
      setNewKeyName("");
      setKeys((prev) => [
        {
          id: created.id,
          name: created.name,
          token_prefix: created.token_prefix,
          scopes: created.scopes,
          created_at: created.created_at,
          last_used_at: null,
          revoked_at: null,
        },
        ...prev,
      ]);
    } catch (e) {
      setCreateError(e instanceof Error ? e.message : t("ai.keys.error.create"));
    } finally {
      setIsCreatingKey(false);
    }
  }

  async function handleRevokeKey(keyId: string) {
    if (!window.confirm(t("ai.keys.revoke.confirm"))) return;
    setRevokingId(keyId);
    try {
      await aiGatewayApi.revokeKey(projectId, keyId);
      setKeys((prev) =>
        prev.map((k) => (k.id === keyId ? { ...k, revoked_at: new Date().toISOString() } : k))
      );
    } catch (e) {
      window.alert(e instanceof Error ? e.message : t("ai.keys.error.revoke"));
    } finally {
      setRevokingId(null);
    }
  }

  async function handleSaveCredential(provider: string) {
    setIsSavingCred(true);
    setCredError(null);
    try {
      await aiGatewayApi.putCredential(projectId, provider, credKey.trim(), credBase.trim() || undefined);
      const resp = await aiGatewayApi.listCredentials(projectId);
      setCredentials(resp.credentials ?? []);
      setEditingProvider(null);
      setCredKey("");
      setCredBase("");
    } catch (e) {
      setCredError(e instanceof Error ? e.message : t("ai.creds.error.save"));
    } finally {
      setIsSavingCred(false);
    }
  }

  async function handleDeleteCredential(provider: string) {
    if (!window.confirm(t("ai.creds.delete.confirm"))) return;
    try {
      await aiGatewayApi.deleteCredential(projectId, provider);
      setCredentials((prev) => prev.filter((c) => c.provider !== provider));
    } catch (e) {
      window.alert(e instanceof Error ? e.message : t("ai.creds.error.delete"));
    }
  }

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-8">
        <Breadcrumb
          items={[
            { label: t("common.crumb.projects"), href: "/projects" },
            { label: project?.display_name ?? t("common.crumb.overview"), href: `/projects/${projectId}` },
            { label: t("nav.ai") },
          ]}
        />
        <div className="mt-2 flex flex-wrap items-center gap-3">
          <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{t("ai.title")}</h1>
          <span className="rounded-full bg-emerald-100 dark:bg-emerald-950/50 px-2.5 py-1 text-xs font-medium text-emerald-700 dark:text-emerald-300">
            {t("ai.free.badge")}
          </span>
        </div>
        <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("ai.subtitle")}</p>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}

      <section className="mb-10" data-onboarding="ai-routing">
        {routingError && (
          <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
            {routingError}
          </div>
        )}

        {view === null ? (
          <>
            <div className="mb-4">
              <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("ai.mode.title")}</h2>
              <p className="text-sm text-gray-500 dark:text-gray-400">{t("ai.mode.subtitle")}</p>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="flex flex-col rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
                <h3 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t("ai.mode.byok.title")}</h3>
                <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("ai.mode.byok.body")}</p>
                <ul className="mt-3 space-y-1 text-sm text-gray-600 dark:text-gray-400">
                  <li>· {t("ai.mode.byok.bullet1")}</li>
                  <li>· {t("ai.mode.byok.bullet2")}</li>
                </ul>
                <button
                  type="button"
                  onClick={() => setView("byok")}
                  className="mt-5 inline-flex items-center justify-center rounded-lg border border-gray-300 dark:border-gray-700 px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors"
                >
                  {t("ai.mode.byok.cta")}
                </button>
              </div>
              <div className="flex flex-col rounded-xl border border-blue-200 dark:border-blue-900 bg-blue-50/50 dark:bg-blue-950/20 p-6 shadow-sm">
                <h3 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t("ai.mode.platform.title")}</h3>
                <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("ai.mode.platform.body")}</p>
                <ul className="mt-3 space-y-1 text-sm text-gray-600 dark:text-gray-400">
                  <li>· {t("ai.mode.platform.bullet1")}</li>
                  <li>· {t("ai.mode.platform.bullet2", { markup: (routing?.markup ?? 1).toFixed(1) })}</li>
                </ul>
                <button
                  type="button"
                  onClick={() => handleSetRouting("platform")}
                  disabled={!canWrite || isSwitching}
                  className="mt-5 inline-flex items-center justify-center rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
                >
                  {isSwitching ? t("ai.mode.platform.enabling") : t("ai.mode.platform.cta")}
                </button>
              </div>
            </div>
          </>
        ) : (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm">
            <div>
              <p className="text-sm font-medium text-gray-900 dark:text-gray-100">
                {routing?.mode === "platform" ? t("ai.mode.platform.on") : t("ai.mode.platform.off")}
              </p>
              <button
                type="button"
                onClick={() => setView(null)}
                className="mt-0.5 text-xs font-medium text-blue-600 dark:text-blue-400 hover:underline"
              >
                {t("ai.mode.back")}
              </button>
            </div>
            {canWrite && (
              <button
                type="button"
                onClick={() => handleSetRouting(routing?.mode === "platform" ? "byok" : "platform")}
                disabled={isSwitching}
                className="shrink-0 rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 disabled:opacity-50 transition-colors"
              >
                {isSwitching
                  ? t("ai.mode.platform.pending")
                  : routing?.mode === "platform"
                    ? t("ai.mode.platform.active")
                    : t("ai.mode.platform.cta")}
              </button>
            )}
          </div>
        )}
      </section>

      {view !== null && (
      <>
      <section className="mb-10 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 shadow-sm">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("ai.quickstart.title")}</h2>
            <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("ai.quickstart.body")}</p>
          </div>
          <div className="flex gap-1 rounded-lg border border-gray-200 dark:border-gray-700 p-0.5">
            {(["python", "node", "curl"] as SnippetLang[]).map((l) => (
              <button
                key={l}
                type="button"
                onClick={() => setLang(l)}
                className={
                  lang === l
                    ? "rounded-md bg-gray-900 dark:bg-gray-100 px-3 py-1 text-xs font-medium text-white dark:text-gray-900"
                    : "rounded-md px-3 py-1 text-xs font-medium text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-100"
                }
              >
                {t(`ai.quickstart.tab.${l}`)}
              </button>
            ))}
          </div>
        </div>

        <div className="relative mt-4">
          <pre className="overflow-x-auto rounded-lg border border-gray-800 bg-gray-900 p-4 pr-20 font-mono text-xs leading-relaxed text-gray-100">
            {snippet}
          </pre>
          <div className="absolute right-2 top-2">
            <CopyButton value={snippet} label={t("common.copy")} />
          </div>
        </div>

        {!justCreated && (
          <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">{t("ai.quickstart.keyPlaceholder")}</p>
        )}
        <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">{t("ai.free.body")}</p>
      </section>

      <section className="mb-10">
        <div className="mb-4">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            1. {t("ai.keys.title")}
          </h2>
          <p className="text-sm text-gray-500 dark:text-gray-400">{t("ai.keys.subtitle")}</p>
        </div>

        {canWrite && (
          <div className="mb-4 flex flex-col gap-2 sm:flex-row sm:items-center">
            <input
              type="text"
              value={newKeyName}
              onChange={(e) => setNewKeyName(e.target.value)}
              placeholder={t("ai.keys.name.placeholder")}
              className="block w-full max-w-xs rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <button
              type="button"
              onClick={handleCreateKey}
              disabled={isCreatingKey}
              className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
            >
              {isCreatingKey ? (
                <>
                  <Spinner size="sm" /> {t("ai.keys.creating")}
                </>
              ) : (
                t("ai.keys.create")
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
            <p className="text-sm font-semibold text-amber-900 dark:text-amber-200">{t("ai.keys.created.title")}</p>
            <p className="mt-1 text-xs text-amber-700 dark:text-amber-400">{t("ai.keys.created.warning")}</p>
            <div className="mt-3 flex items-center gap-2">
              <code className="flex-1 truncate rounded-md border border-amber-200 dark:border-amber-800 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-xs text-amber-900 dark:text-amber-200">
                {justCreated.key}
              </code>
              <CopyButton value={justCreated.key} label={t("common.copy")} />
            </div>
            <button
              type="button"
              onClick={() => setJustCreated(null)}
              className="mt-4 text-xs font-medium text-amber-800 dark:text-amber-300 hover:underline"
            >
              {t("ai.keys.created.done")}
            </button>
          </div>
        )}

        {keys.length === 0 ? (
          <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 py-8">
            <p className="text-sm text-gray-400 dark:text-gray-500">{t("ai.keys.empty")}</p>
          </div>
        ) : (
          <div className="space-y-3">
            {keys.map((k) => (
              <div
                key={k.id}
                className="flex items-center justify-between gap-3 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-gray-900 dark:text-gray-100">
                    {k.name || t("ai.keys.unnamed")}
                  </p>
                  <p className="mt-0.5 truncate font-mono text-xs text-gray-400 dark:text-gray-500">
                    {k.token_prefix}…
                  </p>
                  <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
                    {t("ai.keys.createdAt", { ago: timeAgo(k.created_at) })}
                    {" · "}
                    {k.last_used_at
                      ? t("ai.keys.lastUsed", { ago: timeAgo(k.last_used_at) })
                      : t("ai.keys.neverUsed")}
                  </p>
                </div>
                {k.revoked_at ? (
                  <span className="shrink-0 rounded-full bg-gray-100 dark:bg-gray-800 px-2.5 py-1 text-xs font-medium text-gray-500 dark:text-gray-400">
                    {t("ai.keys.revoked")}
                  </span>
                ) : (
                  canWrite && (
                    <button
                      type="button"
                      onClick={() => handleRevokeKey(k.id)}
                      disabled={revokingId === k.id}
                      className="shrink-0 rounded-lg border border-red-200 dark:border-red-900 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30 disabled:opacity-50 transition-colors"
                    >
                      {t("ai.keys.revoke")}
                    </button>
                  )
                )}
              </div>
            ))}
          </div>
        )}
      </section>

      {view === "byok" && (
      <section className="mb-10">
        <div className="mb-4">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            2. {t("ai.creds.title")}
          </h2>
          <p className="text-sm text-gray-500 dark:text-gray-400">{t("ai.creds.subtitle")}</p>
        </div>

        {credentials.length === 0 && (
          <div className="mb-4 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
            {t("ai.creds.none")}
          </div>
        )}

        {credError && (
          <div className="mb-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-300">
            {credError}
          </div>
        )}

        <div className="space-y-3">
          {(catalog?.providers ?? []).map((p) => {
            const cred = credentials.find((c) => c.provider === p.name);
            const isEditing = editingProvider === p.name;
            return (
              <div
                key={p.name}
                className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm"
              >
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="min-w-0">
                    <p className="text-sm font-medium text-gray-900 dark:text-gray-100">{p.label}</p>
                    {cred ? (
                      <p className="mt-0.5 font-mono text-xs text-gray-400 dark:text-gray-500">
                        {cred.key_hint}
                        {" · "}
                        {t("ai.creds.updated", { ago: timeAgo(cred.updated_at) })}
                      </p>
                    ) : (
                      <a
                        href={p.key_url}
                        target="_blank"
                        rel="noreferrer"
                        className="mt-0.5 inline-block text-xs text-blue-600 dark:text-blue-400 hover:underline"
                      >
                        {t("ai.creds.getKey")}
                      </a>
                    )}
                  </div>
                  {canWrite && !isEditing && (
                    <div className="flex shrink-0 items-center gap-2">
                      <button
                        type="button"
                        onClick={() => {
                          setEditingProvider(p.name);
                          setCredKey("");
                          setCredBase(cred?.api_base ?? "");
                          setCredError(null);
                        }}
                        className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-800 transition-colors"
                      >
                        {cred ? t("ai.creds.replace") : t("ai.creds.add")}
                      </button>
                      {cred && (
                        <button
                          type="button"
                          onClick={() => handleDeleteCredential(p.name)}
                          className="rounded-lg border border-red-200 dark:border-red-900 px-3 py-1.5 text-sm font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/30 transition-colors"
                        >
                          {t("ai.creds.delete")}
                        </button>
                      )}
                    </div>
                  )}
                </div>

                {isEditing && (
                  <div className="mt-3 flex flex-col gap-2 sm:flex-row sm:items-center">
                    <input
                      type="password"
                      value={credKey}
                      onChange={(e) => setCredKey(e.target.value)}
                      placeholder={t("ai.creds.apiKey.placeholder")}
                      autoComplete="off"
                      className="block w-full max-w-sm rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 font-mono text-sm text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                    />
                    <input
                      type="text"
                      value={credBase}
                      onChange={(e) => setCredBase(e.target.value)}
                      placeholder={t("ai.creds.apiBase.placeholder")}
                      className="block w-full max-w-xs rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 focus:border-blue-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
                    />
                    <button
                      type="button"
                      onClick={() => handleSaveCredential(p.name)}
                      disabled={isSavingCred || credKey.trim() === ""}
                      className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-50 transition-colors"
                    >
                      {isSavingCred ? t("ai.creds.saving") : t("ai.creds.save")}
                    </button>
                    <button
                      type="button"
                      onClick={() => setEditingProvider(null)}
                      className="shrink-0 text-sm font-medium text-gray-500 dark:text-gray-400 hover:underline"
                    >
                      {t("ai.creds.cancel")}
                    </button>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </section>
      )}

      <section className="mb-10">
        <div className="mb-4">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            {view === "byok" ? 3 : 2}. {t("ai.models.title")}
          </h2>
          <p className="text-sm text-gray-500 dark:text-gray-400">{t("ai.models.subtitle")}</p>
        </div>

        <div className="overflow-x-auto rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
          <table className="min-w-full text-sm">
            <thead>
              <tr className="border-b border-gray-200 dark:border-gray-800 text-left text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">
                <th className="px-5 py-3 font-medium">{t("ai.models.col.alias")}</th>
                <th className="px-5 py-3 font-medium">{t("ai.models.col.provider")}</th>
                <th className="px-5 py-3 font-medium">{t("ai.models.col.kind")}</th>
                <th className="px-5 py-3 font-medium">{t("ai.models.col.upstream")}</th>
              </tr>
            </thead>
            <tbody>
              {(catalog?.models ?? []).map((m) => (
                <tr key={m.alias} className="border-b border-gray-100 dark:border-gray-800 last:border-0">
                  <td className="px-5 py-3">
                    <code className="font-mono text-xs text-gray-900 dark:text-gray-100">{m.alias}</code>
                  </td>
                  <td className="px-5 py-3 text-gray-600 dark:text-gray-400">
                    {m.provider}
                    {view === "byok" && !configuredProviders.has(m.provider) && (
                      <span className="ml-2 rounded-full bg-amber-100 dark:bg-amber-950/50 px-2 py-0.5 text-xs text-amber-700 dark:text-amber-300">
                        {t("ai.models.needsKey")}
                      </span>
                    )}
                  </td>
                  <td className="px-5 py-3 text-gray-600 dark:text-gray-400">{t(`ai.models.kind.${m.kind}`)}</td>
                  <td className="px-5 py-3 font-mono text-xs text-gray-400 dark:text-gray-500">{m.upstream}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="mb-10">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("ai.usage.title")}</h2>
          <div className="flex gap-1 rounded-lg border border-gray-200 dark:border-gray-700 p-0.5">
            {([7, 30] as const).map((d) => (
              <button
                key={d}
                type="button"
                onClick={() => setUsageDays(d)}
                className={
                  usageDays === d
                    ? "rounded-md bg-gray-900 dark:bg-gray-100 px-3 py-1 text-xs font-medium text-white dark:text-gray-900"
                    : "rounded-md px-3 py-1 text-xs font-medium text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-100"
                }
              >
                {t(`ai.usage.window.${d}`)}
              </button>
            ))}
          </div>
        </div>

        <div className={view === "platform" ? "grid gap-4 sm:grid-cols-4" : "grid gap-4 sm:grid-cols-3"}>
          <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm">
            <p className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("ai.usage.calls")}</p>
            <p className="mt-1 text-2xl font-semibold text-gray-900 dark:text-gray-100">{usage?.total_calls ?? 0}</p>
          </div>
          <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm">
            <p className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("ai.usage.tokens")}</p>
            <p className="mt-1 text-2xl font-semibold text-gray-900 dark:text-gray-100">
              {((usage?.prompt_tokens ?? 0) + (usage?.completion_tokens ?? 0)).toLocaleString()}
            </p>
          </div>
          <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-4 shadow-sm">
            <p className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("ai.usage.cost")}</p>
            <p className="mt-1 text-2xl font-semibold text-gray-900 dark:text-gray-100">
              ${(usage?.total_cost ?? 0).toFixed(2)}
            </p>
            <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("ai.usage.costHint")}</p>
          </div>
          {view === "platform" && (
            <div className="rounded-xl border border-blue-200 dark:border-blue-900 bg-blue-50/50 dark:bg-blue-950/20 px-5 py-4 shadow-sm">
              <p className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">{t("ai.usage.billed")}</p>
              <p className="mt-1 text-2xl font-semibold text-gray-900 dark:text-gray-100">
                ${(usage?.total_billed ?? 0).toFixed(2)}
              </p>
              <p className="mt-1 text-xs text-gray-400 dark:text-gray-500">{t("ai.usage.billedHint")}</p>
            </div>
          )}
        </div>

        {usage && usage.models.length > 0 ? (
          <div className="mt-4 overflow-x-auto rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
            <table className="min-w-full text-sm">
              <thead>
                <tr className="border-b border-gray-200 dark:border-gray-800 text-left text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">
                  <th className="px-5 py-3 font-medium">{t("ai.usage.byModel")}</th>
                  <th className="px-5 py-3 font-medium">{t("ai.usage.calls")}</th>
                  <th className="px-5 py-3 font-medium">{t("ai.usage.cost")}</th>
                </tr>
              </thead>
              <tbody>
                {usage.models.map((m) => (
                  <tr key={m.model} className="border-b border-gray-100 dark:border-gray-800 last:border-0">
                    <td className="px-5 py-3 font-mono text-xs text-gray-900 dark:text-gray-100">{m.model}</td>
                    <td className="px-5 py-3 text-gray-600 dark:text-gray-400">{m.calls}</td>
                    <td className="px-5 py-3 text-gray-600 dark:text-gray-400">${m.cost_usd.toFixed(2)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="mt-4 text-sm text-gray-400 dark:text-gray-500">{t("ai.usage.empty")}</p>
        )}
      </section>
      </>
      )}
    </div>
  );
}
