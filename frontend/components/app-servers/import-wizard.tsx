"use client";
import { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { appServersApi, projectsApi } from "@/lib/api";
import type { ImportServiceInput, WorkloadDiscovery } from "@/lib/types";
import { Modal } from "@/components/ui/modal";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";

interface ImportWizardProps {
  projectId: string;
  serverName: string;
  discovery: WorkloadDiscovery;
  isOpen: boolean;
  onClose: () => void;
}

/**
 * Turns a read-only discovery result into a managed compose app. One screen:
 * pick services, rename, paste optional .env (with an explicit git-plaintext
 * consent gate), preview the generated compose, then Import & Deploy. Follows
 * the "review before change" product principle — nothing leaves until submit.
 */
function slugify(v: string): string {
  return v.toLowerCase().replace(/[^a-z0-9-]/g, "-").replace(/-+/g, "-").replace(/^-|-$/g, "");
}

function servicesFromDiscovery(d: WorkloadDiscovery): ImportServiceInput[] {
  return d.containers.map((c) => ({
    container_name: c.name,
    service_name: slugify(c.name) || "service",
    image: c.image,
    ports: c.ports,
    volumes: c.mounts
      .filter((m) => m.type === "volume" && m.name)
      .map((m) => `${m.name}:${m.destination}`),
    include: true,
  }));
}

function parseEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const eq = line.indexOf("=");
    if (eq <= 0) continue;
    out[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  }
  return out;
}

function buildComposePreview(services: ImportServiceInput[], hasEnv: boolean): string {
  const included = services.filter((s) => s.include);
  if (included.length === 0) return "";
  const lines: string[] = ["services:"];
  const externalVols = new Set<string>();
  for (const s of included) {
    lines.push(`  ${s.service_name}:`);
    lines.push(`    image: ${s.image}`);
    if (s.ports.length) {
      lines.push("    ports:");
      s.ports.forEach((p) => lines.push(`      - "${p}"`));
    }
    if (hasEnv) lines.push("    env_file: [.env]");
    if (s.volumes.length) {
      lines.push("    volumes:");
      s.volumes.forEach((v) => {
        lines.push(`      - ${v}`);
        const name = v.split(":")[0];
        if (name && !v.startsWith("/") && !v.startsWith(".")) externalVols.add(name);
      });
    }
  }
  if (externalVols.size) {
    lines.push("volumes:");
    externalVols.forEach((n) => {
      lines.push(`  ${n}:`);
      lines.push("    external: true");
      lines.push(`    name: ${n}`);
    });
  }
  return lines.join("\n");
}

export function ImportWizard({ projectId, serverName, discovery, isOpen, onClose }: ImportWizardProps) {
  const router = useRouter();
  const { t } = useT();

  const [appName, setAppName] = useState(() => slugify(serverName) + "-stack");
  const [services, setServices] = useState<ImportServiceInput[]>(() => servicesFromDiscovery(discovery));
  const [envText, setEnvText] = useState("");
  const [ack, setAck] = useState(false);
  const [showEnv, setShowEnv] = useState(false);
  const [showPreview, setShowPreview] = useState(false);

  const [submitting, setSubmitting] = useState(false);
  const [progress, setProgress] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const env = useMemo(() => parseEnv(envText), [envText]);
  const hasEnv = Object.keys(env).length > 0;
  const includedCount = services.filter((s) => s.include).length;
  const preview = useMemo(() => buildComposePreview(services, hasEnv), [services, hasEnv]);

  const appNameValid = /^[a-z]([-a-z0-9]*[a-z0-9])?$/.test(appName);
  const canSubmit = includedCount > 0 && appNameValid && (!hasEnv || ack) && !submitting;

  function patchService(idx: number, patch: Partial<ImportServiceInput>) {
    setServices((prev) => prev.map((s, i) => (i === idx ? { ...s, ...patch } : s)));
  }

  async function handleSubmit() {
    setError(null);
    setSubmitting(true);
    setProgress(t("appServers.import.progress.queued"));
    try {
      const { operation } = await appServersApi.import(projectId, serverName, {
        app_name: appName,
        services,
        env,
        ack_secrets_in_git: ack,
      });
      const terminal = new Set(["Committed", "Ready", "Failed", "Cancelled"]);
      let op = operation;
      for (let i = 0; i < 60 && !terminal.has(op.status); i++) {
        setProgress(t("appServers.import.progress.running", { status: op.status }));
        await new Promise((r) => setTimeout(r, 1500));
        op = (await projectsApi.getOperation(projectId, op.id)).operation;
      }
      if (op.status === "Failed" || op.status === "Cancelled") throw new Error(op.error_message || t("appServers.import.failed"));
      if (!terminal.has(op.status)) throw new Error(t("appServers.import.timeout"));
      router.push(`/projects/${projectId}/apps/${appName}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : t("appServers.import.failed"));
      setProgress(null);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal isOpen={isOpen} onClose={submitting ? () => {} : onClose} title={t("appServers.import.title")}>
      <div className="space-y-5">
        <p className="text-sm text-gray-500 dark:text-gray-400">{t("appServers.import.subtitle")}</p>

        <div>
          <label className="block text-sm font-medium text-gray-700 dark:text-gray-200">{t("appServers.import.appName.label")}</label>
          <input
            type="text"
            value={appName}
            onChange={(e) => setAppName(e.target.value)}
            className="mt-1 w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 font-mono text-sm focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
          />
          {!appNameValid && appName.length > 0 && (
            <p className="mt-1 text-xs text-red-600 dark:text-red-400">{t("appServers.import.appName.invalid")}</p>
          )}
        </div>

        <div>
          <div className="mb-2 flex items-center justify-between">
            <p className="text-sm font-medium text-gray-700 dark:text-gray-200">{t("appServers.import.services.label")}</p>
            <span className="text-xs text-gray-400 dark:text-gray-500">{t("appServers.import.services.count", { n: includedCount, total: services.length })}</span>
          </div>
          <div className="space-y-2">
            {services.map((s, idx) => {
              const external = s.volumes.length > 0;
              return (
                <div
                  key={s.container_name}
                  className={`rounded-lg border p-3 transition-colors ${
                    s.include
                      ? "border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900"
                      : "border-gray-100 dark:border-gray-800/60 bg-gray-50 dark:bg-gray-950 opacity-60"
                  }`}
                >
                  <div className="flex items-start gap-3">
                    <input
                      type="checkbox"
                      checked={s.include}
                      onChange={(e) => patchService(idx, { include: e.target.checked })}
                      aria-label={t("appServers.import.services.include", { name: s.container_name })}
                      className="mt-1 h-4 w-4 shrink-0 cursor-pointer rounded border-gray-300 text-amber-600 focus:ring-amber-500"
                    />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <input
                          type="text"
                          value={s.service_name}
                          disabled={!s.include}
                          onChange={(e) => patchService(idx, { service_name: slugify(e.target.value) })}
                          className="w-40 rounded-md border border-gray-300 dark:border-gray-700 px-2 py-1 font-mono text-xs focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500 disabled:opacity-50"
                        />
                        <span className="font-mono text-[11px] text-gray-400 dark:text-gray-500">← {s.container_name}</span>
                      </div>
                      <div className="mt-2 flex flex-wrap items-center gap-1.5">
                        <span className="rounded bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 font-mono text-[11px] text-gray-600 dark:text-gray-400">{s.image}</span>
                        {s.ports.map((p) => (
                          <span key={p} className="rounded bg-blue-50 dark:bg-blue-950/40 px-1.5 py-0.5 font-mono text-[11px] text-blue-700 dark:text-blue-300">{p}</span>
                        ))}
                        {external && (
                          <span className="inline-flex items-center gap-1 rounded bg-green-50 dark:bg-green-950/40 px-1.5 py-0.5 text-[11px] font-medium text-green-700 dark:text-green-300" title={t("appServers.import.services.dataSafe")}>
                            <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" /></svg>
                            {s.volumes.length} vol
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        <div>
          <button
            type="button"
            onClick={() => setShowEnv((v) => !v)}
            className="flex items-center gap-1.5 text-sm font-medium text-gray-600 dark:text-gray-300 hover:text-gray-900 dark:hover:text-gray-100"
          >
            <svg className={`h-3.5 w-3.5 transition-transform ${showEnv ? "rotate-90" : ""}`} fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" /></svg>
            {t("appServers.import.env.toggle")}
            {hasEnv && <span className="rounded-full bg-amber-100 dark:bg-amber-950/40 px-1.5 text-[11px] text-amber-700 dark:text-amber-300">{Object.keys(env).length}</span>}
          </button>
          {showEnv && (
            <div className="mt-2 space-y-2">
              <textarea
                value={envText}
                onChange={(e) => setEnvText(e.target.value)}
                rows={5}
                placeholder={"DATABASE_URL=postgres://…\nREDIS_URL=redis://…"}
                className="w-full rounded-lg border border-gray-300 dark:border-gray-700 px-3 py-2 font-mono text-xs focus:border-amber-500 focus:outline-none focus:ring-1 focus:ring-amber-500"
              />
              {hasEnv && (
                <label className="flex cursor-pointer items-start gap-2 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/30 p-3">
                  <input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} className="mt-0.5 h-4 w-4 shrink-0 cursor-pointer rounded border-amber-300 text-amber-600 focus:ring-amber-500" />
                  <span className="text-xs text-amber-800 dark:text-amber-200">{t("appServers.import.env.consent")}</span>
                </label>
              )}
            </div>
          )}
        </div>

        {preview && (
          <div>
            <button
              type="button"
              onClick={() => setShowPreview((v) => !v)}
              className="flex items-center gap-1.5 text-sm font-medium text-gray-600 dark:text-gray-300 hover:text-gray-900 dark:hover:text-gray-100"
            >
              <svg className={`h-3.5 w-3.5 transition-transform ${showPreview ? "rotate-90" : ""}`} fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" /></svg>
              {t("appServers.import.preview.toggle")}
            </button>
            {showPreview && (
              <pre className="mt-2 max-h-64 overflow-auto rounded-lg bg-gray-50 dark:bg-gray-950 p-3 font-mono text-xs text-gray-800 dark:text-gray-200">{preview}</pre>
            )}
          </div>
        )}

        {error && (
          <div role="alert" className="rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-3 py-2 text-sm text-red-700 dark:text-red-300">{error}</div>
        )}
        {progress && !error && (
          <div className="flex items-center gap-2 rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-900 px-3 py-2 text-sm text-gray-600 dark:text-gray-300">
            <Spinner size="sm" /> {progress}
          </div>
        )}

        <div className="flex justify-end gap-3 pt-1">
          <button type="button" disabled={submitting} onClick={onClose} className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50">
            {t("common.cancel")}
          </button>
          <button
            type="button"
            disabled={!canSubmit}
            onClick={handleSubmit}
            className="inline-flex items-center gap-2 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-amber-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {submitting ? <Spinner size="sm" /> : (
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" /></svg>
            )}
            {t("appServers.import.submit")}
          </button>
        </div>
      </div>
    </Modal>
  );
}
