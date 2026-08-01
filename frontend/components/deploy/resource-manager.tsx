"use client";
import { useEffect, useState } from "react";
import { appsApi } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";

const PLATFORM_CAP = { cpu: "8", memory: "16 GiB" };

interface Envelope {
  cpu_request: string;
  memory_request: string;
  cpu_limit: string;
  memory_limit: string;
}

interface Props {
  projectId: string;
  envId: string;
  appName: string;
  canEdit: boolean;
}

export function ResourceManager({ projectId, envId, appName }: Props) {
  const { t } = useT();
  const [envelope, setEnvelope] = useState<Envelope | null>(null);

  useEffect(() => {
    if (!envId) return;
    appsApi
      .list(projectId, envId)
      .then((d) => {
        const app = (d.apps ?? []).find((a) => a.name === appName);
        const r = app?.summary_json?.resources as Envelope | undefined;
        if (r?.cpu_limit && r?.memory_limit) setEnvelope(r);
      })
      .catch(() => {});
  }, [projectId, envId, appName]);

  const row = "flex items-baseline justify-between gap-4 py-2";
  const dim = "text-sm text-gray-500 dark:text-gray-400";
  const num = "font-mono text-sm text-gray-900 dark:text-gray-100";

  return (
    <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 px-5 py-6">
      <div className="flex items-center gap-3">
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{t("apps.resources.title")}</h2>
        <span className="rounded-full bg-green-50 px-2.5 py-0.5 text-xs font-medium text-green-700 dark:bg-green-950 dark:text-green-400">
          {t("apps.resources.auto")}
        </span>
      </div>
      <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{t("apps.resources.subtitle")}</p>

      <div className="mt-4 rounded-lg bg-gray-50 dark:bg-gray-950 px-4 py-3">
        <div className="text-xs uppercase tracking-wide text-gray-400 dark:text-gray-500">
          {t("apps.resources.current")}
        </div>
        {envelope ? (
          <div className="mt-1 divide-y divide-gray-200 dark:divide-gray-800">
            <div className={row}>
              <span className={dim}>{t("apps.resources.cpu")}</span>
              <span className={num}>
                {envelope.cpu_request} <span className={dim}>{t("apps.resources.request")}</span> · {envelope.cpu_limit}{" "}
                <span className={dim}>{t("apps.resources.limit")}</span>
              </span>
            </div>
            <div className={row}>
              <span className={dim}>{t("apps.resources.memory")}</span>
              <span className={num}>
                {envelope.memory_request} <span className={dim}>{t("apps.resources.request")}</span> ·{" "}
                {envelope.memory_limit} <span className={dim}>{t("apps.resources.limit")}</span>
              </span>
            </div>
          </div>
        ) : (
          <div className="mt-1 text-sm text-gray-400 dark:text-gray-500">{t("apps.resources.none")}</div>
        )}
      </div>

      <p className="mt-4 text-xs text-gray-500 dark:text-gray-400">{t("apps.resources.growth")}</p>
      <p className="mt-2 text-xs text-gray-400 dark:text-gray-500">
        {t("apps.resources.cap", { cpu: PLATFORM_CAP.cpu, memory: PLATFORM_CAP.memory })}
      </p>
    </div>
  );
}
