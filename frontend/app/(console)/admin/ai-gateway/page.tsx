"use client";
import { useCallback, useEffect, useState } from "react";
import { adminApi } from "@/lib/api";
import type {
  AIGatewayUsageResponse,
  AIGatewayProviderStat,
  AIGatewayProjectStat,
  AIGatewayModelStat,
  AIGatewaySourceStat,
  AdminAIGatewayCredential,
  AdminAIGatewayModelStat,
  AIGatewayCredentialUsageStat,
} from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdminTabs } from "@/components/console/admin-tabs";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useT } from "@/lib/i18n/console/context";
import { buildCredentialUpdate, type CredentialEditValues } from "@/lib/ai-gateway-credentials";

const REFRESH_MS = 60_000;

function formatUSD(v: number | undefined): string {
  return `$${(v ?? 0).toFixed(4)}`;
}

function formatInt(v: number | undefined): string {
  return (v ?? 0).toLocaleString("ru-RU");
}

export default function AIGatewayUsagePage() {
  const { t } = useT();

  const [days, setDays] = useState<7 | 30>(7);
  const [data, setData] = useState<AIGatewayUsageResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);

  const load = useCallback(async (opts: { silent?: boolean } = {}) => {
    if (!opts.silent) setIsLoading(true);
    setError(null);
    try {
      const resp = await adminApi.getAIGatewayUsage(days);
      setData(resp);
      setForbidden(false);
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      if (status === 403) {
        setForbidden(true);
      } else {
        setError(err instanceof Error ? err.message : t("aiGateway.error.load"));
      }
    } finally {
      setIsLoading(false);
    }
  }, [days, t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (forbidden) return;
    const interval = setInterval(() => { void load({ silent: true }); }, REFRESH_MS);
    return () => clearInterval(interval);
  }, [forbidden, load]);

  const crumb = (
    <Breadcrumb
      items={[
        { label: t("common.crumb.console"), href: "/projects" },
        { label: t("approvals.crumb.admin") },
        { label: t("aiGateway.crumb.aiGateway") },
      ]}
    />
  );

  if (forbidden) {
    return (
      <div>
        {crumb}
        <div className="mt-4 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("aiGateway.accessDenied")}
        </div>
      </div>
    );
  }

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          {crumb}
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("aiGateway.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("aiGateway.subtitle")}</p>
        </div>
        <div className="flex items-center gap-2">
          <div className="flex rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-0.5 shadow-sm">
            {([7, 30] as const).map((d) => (
              <button
                key={d}
                onClick={() => setDays(d)}
                className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                  days === d
                    ? "bg-blue-600 text-white"
                    : "text-gray-600 hover:text-blue-600 dark:text-gray-300 dark:hover:text-blue-400"
                }`}
              >
                {t(`aiGateway.window.${d}d`)}
              </button>
            ))}
          </div>
          <button
            onClick={() => load()}
            className="inline-flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-200 hover:border-blue-300 hover:text-blue-600 transition-colors shadow-sm"
          >
            {t("common.refresh")}
          </button>
        </div>
      </div>

      <AdminTabs active="ai-gateway" />

      <CredentialPool t={t} usage={data?.credentials ?? []} />

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
      )}

      <div className="mb-6 grid grid-cols-2 gap-4 lg:grid-cols-4">
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("aiGateway.kpi.totalCost")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">
              {isLoading ? "—" : formatUSD(data?.total_cost)}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <p className="text-xs font-medium text-gray-500 dark:text-gray-400">{t("aiGateway.kpi.totalCalls")}</p>
            <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">
              {isLoading ? "—" : formatInt(data?.total_calls)}
            </p>
          </CardContent>
        </Card>
      </div>

      <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("aiGateway.byProvider.title")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            <ProviderTable rows={data?.providers ?? []} isLoading={isLoading} t={t} />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("aiGateway.bySource.title")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            <SourceTable rows={data?.sources ?? []} isLoading={isLoading} t={t} />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("aiGateway.byProject.title")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            <ProjectTable rows={data?.projects ?? []} isLoading={isLoading} t={t} />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="p-4 pb-2"><CardTitle className="text-sm">{t("aiGateway.byModel.title")}</CardTitle></CardHeader>
          <CardContent className="p-4 pt-0">
            <ModelTable rows={data?.models ?? []} isLoading={isLoading} t={t} />
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

type Tr = (key: string, vars?: Record<string, string | number>) => string;

function CredentialPool({ t, usage }: { t: Tr; usage: AIGatewayCredentialUsageStat[] }) {
  const [credentials, setCredentials] = useState<AdminAIGatewayCredential[]>([]);
  const [models, setModels] = useState<AdminAIGatewayModelStat[]>([]);
  const [provider, setProvider] = useState("sotamodel");
  const [label, setLabel] = useState("");
  const [apiBase, setApiBase] = useState("");
  const [keys, setKeys] = useState("");
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editValues, setEditValues] = useState<CredentialEditValues>({ label: "", apiBase: "", apiKey: "", priority: "100" });

  const loadPool = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const [credentialResponse, modelResponse] = await Promise.all([
        adminApi.listAIGatewayCredentials(),
        adminApi.getAIGatewayModelStats(),
      ]);
      setCredentials(credentialResponse.credentials ?? []);
      setModels(modelResponse.models ?? []);
      setForbidden(false);
    } catch (err) {
      if ((err as { status?: number })?.status === 403) setForbidden(true);
      else setError(err instanceof Error ? err.message : t("aiGateway.pool.error.load"));
    } finally {
      setIsLoading(false);
    }
  }, [t]);

  useEffect(() => { void loadPool(); }, [loadPool]);

  async function addCredentials() {
    const apiKeys = keys.split(/\r?\n/).map((key) => key.trim()).filter(Boolean);
    if (!provider.trim() || apiKeys.length === 0) return;
    setIsSaving(true);
    setError(null);
    try {
      await adminApi.createAIGatewayCredentials({
        provider: provider.trim(),
        label: label.trim() || undefined,
        api_base: apiBase.trim() || undefined,
        api_keys: apiKeys,
      });
      setKeys("");
      setLabel("");
      await loadPool();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("aiGateway.pool.error.save"));
    } finally {
      setIsSaving(false);
    }
  }

  async function toggleCredential(credential: AdminAIGatewayCredential) {
    try {
      await adminApi.updateAIGatewayCredential(credential.id, { enabled: !credential.enabled });
      await loadPool();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("aiGateway.pool.error.save"));
    }
  }

  function beginEdit(credential: AdminAIGatewayCredential) {
    setEditingId(credential.id);
    setEditValues({
      label: credential.label ?? "",
      apiBase: credential.api_base ?? "",
      apiKey: "",
      priority: String(credential.priority),
    });
    setError(null);
  }

  async function saveCredential(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!editingId) return;
    setIsSaving(true);
    setError(null);
    try {
      await adminApi.updateAIGatewayCredential(editingId, buildCredentialUpdate(editValues));
      setEditingId(null);
      setEditValues({ label: "", apiBase: "", apiKey: "", priority: "100" });
      await loadPool();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("aiGateway.pool.error.save"));
    } finally {
      setIsSaving(false);
    }
  }

  async function deleteCredential(credential: AdminAIGatewayCredential) {
    if (!window.confirm(t("aiGateway.pool.delete.confirm", { label: credential.label || credential.provider }))) return;
    try {
      await adminApi.deleteAIGatewayCredential(credential.id);
      await loadPool();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("aiGateway.pool.error.delete"));
    }
  }

  if (forbidden) return null;

  return (
    <section className="mb-6 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("aiGateway.pool.title")}</h2>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("aiGateway.pool.subtitle")}</p>
        </div>
        <button type="button" onClick={() => void loadPool()} className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm font-medium text-gray-700 dark:text-gray-300">
          {t("common.refresh")}
        </button>
      </div>

      {error && <div className="mt-4 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>}

      <div className="mt-5 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <label className="text-xs font-medium text-gray-600 dark:text-gray-400">{t("aiGateway.pool.provider")}
          <select value={provider} onChange={(event) => setProvider(event.target.value)} className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100">
            <option value="sotamodel">SotaModel</option>
            <option value="openai">OpenAI</option>
            <option value="anthropic">Anthropic</option>
            <option value="openrouter">OpenRouter</option>
            <option value="groq">Groq</option>
            <option value="sambanova">SambaNova</option>
          </select>
        </label>
        <label className="text-xs font-medium text-gray-600 dark:text-gray-400">{t("aiGateway.pool.label")}
          <input value={label} onChange={(event) => setLabel(event.target.value)} placeholder={t("aiGateway.pool.label.placeholder")} className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100" />
        </label>
        <label className="text-xs font-medium text-gray-600 dark:text-gray-400">{t("aiGateway.pool.apiBase")}
          <input type="url" value={apiBase} onChange={(event) => setApiBase(event.target.value)} placeholder="https://api.example.com/v1" className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 text-sm text-gray-900 dark:text-gray-100" />
        </label>
        <label className="text-xs font-medium text-gray-600 dark:text-gray-400">{t("aiGateway.pool.keys")}
          <textarea value={keys} onChange={(event) => setKeys(event.target.value)} rows={4} placeholder={"sk-…\nsk-…\nsk-…"} className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-950 px-3 py-2 font-mono text-sm text-gray-900 dark:text-gray-100" />
        </label>
      </div>
      <div className="mt-3 flex justify-end"><button type="button" onClick={() => void addCredentials()} disabled={isSaving || !provider.trim() || !keys.trim()} className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-50">{isSaving ? t("aiGateway.pool.adding") : t("aiGateway.pool.add")}</button></div>

      <div className="mt-5 space-y-3">
        {isLoading ? <p className="py-5 text-center text-sm text-gray-400">…</p> : credentials.length === 0 ? <p className="py-5 text-center text-sm text-gray-400">{t("aiGateway.pool.empty")}</p> : credentials.map((credential) => (
          <div key={credential.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-gray-200 dark:border-gray-800 px-4 py-3">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2"><span className="font-medium text-gray-900 dark:text-gray-100">{credential.label || credential.provider}</span><code className="text-xs text-gray-400">{credential.key_hint}</code>{credential.source === "legacy_import" ? <span className="rounded-full bg-amber-50 dark:bg-amber-950/40 px-2 py-0.5 text-xs text-amber-700 dark:text-amber-300">{t("aiGateway.pool.legacyFallback")}</span> : credential.editable ? (credential.enabled ? <span className="rounded-full bg-emerald-100 dark:bg-emerald-950/50 px-2 py-0.5 text-xs text-emerald-700 dark:text-emerald-300">{t("aiGateway.pool.enabled")}</span> : <span className="text-xs text-gray-400">{t("aiGateway.pool.disabled")}</span>) : <span className="rounded-full bg-blue-50 dark:bg-blue-950/40 px-2 py-0.5 text-xs text-blue-700 dark:text-blue-300">{t("aiGateway.pool.projectByok")}</span>}</div>
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{credential.provider}{credential.api_base ? ` · ${credential.api_base}` : ""}{credential.editable ? ` · ${t("aiGateway.pool.priority", { priority: credential.priority })}` : ` · ${t("aiGateway.pool.projectScope", { project: credential.project_name || credential.project_id || "—" })}`}</p>
            </div>
            {credential.editable && <div className="flex flex-wrap gap-2"><button type="button" onClick={() => beginEdit(credential)} className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm text-gray-700 dark:text-gray-300">{t("aiGateway.pool.edit")}</button><button type="button" onClick={() => void toggleCredential(credential)} className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm text-gray-700 dark:text-gray-300">{credential.enabled ? t("aiGateway.pool.disable") : t("aiGateway.pool.enable")}</button><button type="button" onClick={() => void deleteCredential(credential)} className="rounded-lg border border-red-200 dark:border-red-900 px-3 py-1.5 text-sm text-red-600 dark:text-red-400">{t("aiGateway.pool.delete")}</button></div>}
            {editingId === credential.id && <form onSubmit={saveCredential} className="basis-full rounded-lg bg-gray-50 dark:bg-gray-950 p-4" aria-label={t("aiGateway.pool.edit.title")}>
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                <label className="text-xs font-medium text-gray-600 dark:text-gray-400">{t("aiGateway.pool.label")}<input autoFocus value={editValues.label} onChange={(event) => setEditValues((value) => ({ ...value, label: event.target.value }))} className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100" /></label>
                <label className="text-xs font-medium text-gray-600 dark:text-gray-400">{t("aiGateway.pool.apiBase")}<input type="url" value={editValues.apiBase} onChange={(event) => setEditValues((value) => ({ ...value, apiBase: event.target.value }))} className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100" /></label>
                <label className="text-xs font-medium text-gray-600 dark:text-gray-400">{t("aiGateway.pool.edit.key")}<input type="password" autoComplete="new-password" value={editValues.apiKey} onChange={(event) => setEditValues((value) => ({ ...value, apiKey: event.target.value }))} placeholder={t("aiGateway.pool.edit.keyHint")} className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100" /></label>
                <label className="text-xs font-medium text-gray-600 dark:text-gray-400">{t("aiGateway.pool.edit.priority")}<input type="number" min={0} step={1} required value={editValues.priority} onChange={(event) => setEditValues((value) => ({ ...value, priority: event.target.value }))} className="mt-1 block w-full rounded-lg border border-gray-300 dark:border-gray-700 bg-white dark:bg-gray-900 px-3 py-2 text-sm text-gray-900 dark:text-gray-100" /></label>
              </div>
              <div className="mt-3 flex justify-end gap-2"><button type="button" onClick={() => setEditingId(null)} disabled={isSaving} className="rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-1.5 text-sm text-gray-700 dark:text-gray-300 disabled:opacity-50">{t("aiGateway.pool.edit.cancel")}</button><button type="submit" disabled={isSaving} className="rounded-lg bg-blue-600 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50">{isSaving ? t("aiGateway.pool.edit.saving") : t("aiGateway.pool.edit.save")}</button></div>
            </form>}
          </div>
        ))}
      </div>

      {models.length > 0 && <div className="mt-5 border-t border-gray-200 dark:border-gray-800 pt-4"><h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("aiGateway.pool.models")}</h3><div className="mt-2 grid gap-2 md:grid-cols-2">{models.map((model) => <div key={`${model.provider}:${model.id}`} className="flex items-center justify-between rounded-lg bg-gray-50 dark:bg-gray-950 px-3 py-2 text-xs"><code>{model.id}</code><span className="text-gray-500">{model.provider || "—"} · {t("aiGateway.pool.modelCredentials", { count: model.credential_count })}</span></div>)}</div></div>}

      {usage.length > 0 && <div className="mt-5 overflow-x-auto border-t border-gray-200 dark:border-gray-800 pt-4"><h3 className="mb-2 text-sm font-semibold text-gray-900 dark:text-gray-100">{t("aiGateway.pool.analytics")}</h3><table className="w-full text-sm"><thead><tr className="border-b border-gray-200 dark:border-gray-800 text-xs text-gray-500"><th className="py-2 text-left">{t("aiGateway.pool.credential")}</th><th className="py-2 text-right">{t("aiGateway.table.calls")}</th><th className="py-2 text-right">{t("aiGateway.pool.tokens")}</th><th className="py-2 text-right">{t("aiGateway.table.cost")}</th><th className="py-2 text-right">{t("aiGateway.pool.billed")}</th></tr></thead><tbody>{usage.map((row) => <tr key={row.credential_id} className="border-b border-gray-100 dark:border-gray-800/60"><td className="py-2"><span className="font-medium text-gray-900 dark:text-gray-100">{row.label || row.provider}</span><code className="ml-2 text-xs text-gray-400">{row.key_hint}</code></td><td className="py-2 text-right font-mono text-xs">{formatInt(row.calls)}</td><td className="py-2 text-right font-mono text-xs">{formatInt(row.total_tokens)}</td><td className="py-2 text-right font-mono text-xs">{formatUSD(row.cost_usd)}</td><td className="py-2 text-right font-mono text-xs">{formatUSD(row.billed_usd)}</td></tr>)}</tbody></table></div>}
    </section>
  );
}

function sourceLabel(source: string, t: Tr): string {
  const key = `aiGateway.source.${source}`;
  const label = t(key);
  return label === key ? source : label;
}

function EmptyOrLoading({ isLoading, empty, t }: { isLoading: boolean; empty: boolean; t: Tr }) {
  if (isLoading) return <p className="py-6 text-center text-sm text-gray-400 dark:text-gray-500">…</p>;
  if (empty) return <p className="py-6 text-center text-sm text-gray-400 dark:text-gray-500">{t("aiGateway.empty")}</p>;
  return null;
}

function ProviderTable({ rows, isLoading, t }: { rows: AIGatewayProviderStat[]; isLoading: boolean; t: Tr }) {
  if (isLoading || rows.length === 0) return <EmptyOrLoading isLoading={isLoading} empty={rows.length === 0} t={t} />;
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400">
            <th className="py-2 text-left">{t("aiGateway.table.provider")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.calls")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.promptTokens")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.completionTokens")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.cost")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.provider} className="border-b border-gray-100 dark:border-gray-800/60">
              <td className="py-2 font-medium text-gray-900 dark:text-gray-100">{r.provider}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatInt(r.calls)}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatInt(r.prompt_tokens)}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatInt(r.completion_tokens)}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatUSD(r.cost_usd)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SourceTable({ rows, isLoading, t }: { rows: AIGatewaySourceStat[]; isLoading: boolean; t: Tr }) {
  if (isLoading || rows.length === 0) return <EmptyOrLoading isLoading={isLoading} empty={rows.length === 0} t={t} />;
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400">
            <th className="py-2 text-left">{t("aiGateway.table.source")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.calls")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.cost")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.source} className="border-b border-gray-100 dark:border-gray-800/60">
              <td className="py-2 font-medium text-gray-900 dark:text-gray-100">{sourceLabel(r.source, t)}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatInt(r.calls)}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatUSD(r.cost_usd)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ProjectTable({ rows, isLoading, t }: { rows: AIGatewayProjectStat[]; isLoading: boolean; t: Tr }) {
  if (isLoading || rows.length === 0) return <EmptyOrLoading isLoading={isLoading} empty={rows.length === 0} t={t} />;
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400">
            <th className="py-2 text-left">{t("aiGateway.table.project")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.calls")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.cost")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.project_id || r.project_name} className="border-b border-gray-100 dark:border-gray-800/60">
              <td className="py-2 font-medium text-gray-900 dark:text-gray-100">{r.project_name}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatInt(r.calls)}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatUSD(r.cost_usd)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ModelTable({ rows, isLoading, t }: { rows: AIGatewayModelStat[]; isLoading: boolean; t: Tr }) {
  if (isLoading || rows.length === 0) return <EmptyOrLoading isLoading={isLoading} empty={rows.length === 0} t={t} />;
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200 dark:border-gray-800 text-xs font-medium text-gray-500 dark:text-gray-400">
            <th className="py-2 text-left">{t("aiGateway.table.model")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.calls")}</th>
            <th className="py-2 text-right">{t("aiGateway.table.cost")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.model} className="border-b border-gray-100 dark:border-gray-800/60">
              <td className="py-2 font-medium text-gray-900 dark:text-gray-100">{r.model}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatInt(r.calls)}</td>
              <td className="py-2 text-right font-mono text-xs text-gray-700 dark:text-gray-200">{formatUSD(r.cost_usd)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
