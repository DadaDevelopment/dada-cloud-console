"use client";
import { Globe, ShieldCheck, ShieldOff, Lock, ArrowRight } from "lucide-react";
import type { ResourceSnapshot } from "@/lib/types";
import { extractIngressSpec } from "@/lib/vm-resources";
import { useT } from "@/lib/i18n/console/context";

function Chip({ on, label, icon: Icon }: { on: boolean; label: string; icon: typeof ShieldCheck }) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ${
        on
          ? "bg-emerald-50 dark:bg-emerald-950/40 text-emerald-700 dark:text-emerald-300 ring-1 ring-inset ring-emerald-200 dark:ring-emerald-900"
          : "bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 ring-1 ring-inset ring-gray-200 dark:ring-gray-700"
      }`}
    >
      <Icon className="h-3.5 w-3.5" />
      {label}
    </span>
  );
}

export function IngressDetail({ app }: { app: ResourceSnapshot }) {
  const { t } = useT();
  const spec = extractIngressSpec(app);
  if (!spec) return null;

  const tlsOn = Boolean(spec.tls?.enabled);
  const rules = spec.rules ?? [];

  return (
    <div className="overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 shadow-sm">
      <div className="border-b border-gray-100 dark:border-gray-800 px-5 py-4">
        <div className="flex items-center gap-2">
          <Globe className="h-4 w-4 text-blue-600 dark:text-blue-400" />
          <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{t("resources.ingress.title")}</h2>
        </div>
        <p className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{t("resources.ingress.subtitle")}</p>
      </div>

      <div className="px-5 py-4">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <a
            href={`https://${spec.host}`}
            target="_blank"
            rel="noopener noreferrer"
            className="font-mono text-base font-semibold text-gray-900 dark:text-gray-100 hover:text-blue-600 dark:hover:text-blue-400"
          >
            {spec.host}
          </a>
          {(spec.aliases ?? []).map((a) => (
            <span
              key={a}
              className="rounded-md bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 font-mono text-xs text-gray-500 dark:text-gray-400"
            >
              {a}
            </span>
          ))}
        </div>

        <div className="mt-3 flex flex-wrap gap-2">
          <Chip
            on={tlsOn}
            icon={tlsOn ? ShieldCheck : ShieldOff}
            label={`${t("resources.ingress.tls")} · ${
              tlsOn ? t("resources.ingress.tls.on") : t("resources.ingress.tls.off")
            }${tlsOn && spec.tls?.min_version ? " · " + t("resources.ingress.tls.min", { v: spec.tls.min_version }) : ""}`}
          />
          {spec.ssl_redirect && <Chip on icon={ArrowRight} label={t("resources.ingress.sslRedirect")} />}
          {spec.basic_auth && <Chip on icon={Lock} label={t("resources.ingress.basicAuth")} />}
        </div>

        {spec.tls?.cert_path && (
          <p className="mt-3 truncate text-xs text-gray-400 dark:text-gray-500">
            <span className="font-medium">{t("resources.ingress.cert")}:</span>{" "}
            <span className="font-mono">{spec.tls.cert_path}</span>
          </p>
        )}
      </div>

      <div className="border-t border-gray-100 dark:border-gray-800 px-5 py-4">
        <h3 className="mb-3 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
          {t("resources.ingress.routes.title")}
        </h3>
        {rules.length === 0 ? (
          <p className="text-sm text-gray-400 dark:text-gray-500">{t("resources.ingress.routes.empty")}</p>
        ) : (
          <div className="overflow-hidden rounded-lg border border-gray-100 dark:border-gray-800">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100 dark:border-gray-800 bg-gray-50 dark:bg-gray-900/60 text-left">
                  <th className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
                    {t("resources.ingress.routes.path")}
                  </th>
                  <th className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-gray-400 dark:text-gray-500">
                    {t("resources.ingress.routes.target")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {rules.map((r, i) => (
                  <tr
                    key={`${r.path}-${i}`}
                    className="border-b border-gray-50 dark:border-gray-800/60 last:border-0"
                  >
                    <td className="px-4 py-2.5 font-mono text-gray-900 dark:text-gray-100">{r.path}</td>
                    <td className="px-4 py-2.5">
                      <span className="inline-flex items-center gap-1.5 text-gray-600 dark:text-gray-300">
                        <ArrowRight className="h-3.5 w-3.5 text-gray-300 dark:text-gray-600" />
                        <span className="font-mono">
                          {r.app}
                          <span className="text-gray-400 dark:text-gray-500">:{r.port}</span>
                        </span>
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
