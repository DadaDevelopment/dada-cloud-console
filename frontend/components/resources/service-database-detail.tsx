"use client";
import { useState } from "react";
import { Database, HardDrive, Copy, Check, KeyRound } from "lucide-react";
import type { ResourceSnapshot } from "@/lib/types";
import { extractDatabaseSpec } from "@/lib/vm-resources";
import { useT } from "@/lib/i18n/console/context";

function Field({ label, value, mono = true }: { label: string; value: string; mono?: boolean }) {
  if (!value) return null;
  return (
    <div>
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">{label}</p>
      <p className={`mt-1 truncate text-sm text-gray-900 dark:text-gray-100 ${mono ? "font-mono" : ""}`}>{value}</p>
    </div>
  );
}

export function ServiceDatabaseDetail({ app }: { app: ResourceSnapshot }) {
  const { t } = useT();
  const db = extractDatabaseSpec(app);
  const [copied, setCopied] = useState(false);

  async function copyDsn() {
    try {
      await navigator.clipboard.writeText(db.dsn);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable */
    }
  }

  return (
    <div className="space-y-6">
      <div className="overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
        <div className="border-b border-gray-100 dark:border-gray-800 px-5 py-4">
          <div className="flex items-center gap-2">
            <Database className="h-4 w-4 text-violet-600 dark:text-violet-400" />
            <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("resources.db.title")}</h2>
          </div>
          <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{t("resources.db.subtitle")}</p>
        </div>

        <div className="grid gap-4 px-5 py-4 sm:grid-cols-2 lg:grid-cols-3">
          <Field label={t("resources.db.engine")} value={db.engine} mono={false} />
          <Field label={t("resources.db.version")} value={db.version} />
          <Field label={t("resources.db.database")} value={db.database} />
          <Field label={t("resources.db.user")} value={db.user} />
          <Field label={t("resources.db.host")} value={db.host} />
          <Field label={t("resources.db.port")} value={db.port ? String(db.port) : ""} />
        </div>

        {db.dsn && (
          <div className="border-t border-gray-100 dark:border-gray-800 px-5 py-4">
            <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
              {t("resources.db.dsn")}
            </p>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded-lg border border-gray-200 dark:border-gray-800 bg-gray-50 dark:bg-gray-950/60 px-3 py-2 font-mono text-xs text-gray-700 dark:text-gray-300">
                {db.dsn}
              </code>
              <button
                onClick={copyDsn}
                className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-3 py-2 text-xs font-medium text-gray-600 dark:text-gray-300 hover:border-blue-300 hover:text-blue-600 transition-colors"
              >
                {copied ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                {copied ? t("resources.db.copied") : t("resources.db.copy")}
              </button>
            </div>
            {db.hasPassword && (
              <p className="mt-2 inline-flex items-center gap-1.5 text-xs text-gray-400 dark:text-gray-500">
                <KeyRound className="h-3.5 w-3.5" />
                {t("resources.db.password.managed")}
              </p>
            )}
          </div>
        )}
      </div>

      {db.volume && (
        <div className="overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
          <div className="border-b border-gray-100 dark:border-gray-800 px-5 py-4">
            <div className="flex items-center gap-2">
              <HardDrive className="h-4 w-4 text-amber-600 dark:text-amber-400" />
              <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("resources.db.storage.title")}</h2>
            </div>
          </div>
          <div className="px-5 py-4">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
              {t("resources.db.storage.volume")}
            </p>
            <p className="mt-1 font-mono text-sm text-gray-900 dark:text-gray-100">{db.volume}</p>
            <p className="mt-1 text-xs text-emerald-600 dark:text-emerald-400">{t("resources.db.storage.external")}</p>
          </div>
        </div>
      )}
    </div>
  );
}
