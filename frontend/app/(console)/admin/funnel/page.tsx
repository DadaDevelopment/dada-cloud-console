"use client";
import { useCallback, useEffect, useMemo, useState } from "react";
import { adminApi } from "@/lib/api";
import type { AdminFunnelResponse } from "@/lib/types";
import { Breadcrumb } from "@/components/ui/breadcrumb";
import { AdminTabs } from "@/components/console/admin-tabs";
import { AuditFacetFilter, type FacetOption } from "@/components/console/audit-facet-filter";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/console/context";

const WINDOWS = ["7d", "30d", "90d", "all"] as const;

type Stage = { key: string; labelKey: string; count: number };

function stages(data: AdminFunnelResponse): Stage[] {
  return [
    { key: "signups", labelKey: "adminFunnel.stage.signups", count: data.signups },
    { key: "app", labelKey: "adminFunnel.stage.app", count: data.app_up },
    { key: "db", labelKey: "adminFunnel.stage.db", count: data.db_up },
    { key: "vm", labelKey: "adminFunnel.stage.vm", count: data.vm_up },
    { key: "box", labelKey: "adminFunnel.stage.box", count: data.box_up },
    { key: "s3", labelKey: "adminFunnel.stage.s3", count: data.s3_up },
    { key: "model", labelKey: "adminFunnel.stage.model", count: data.model_up },
    { key: "paid", labelKey: "adminFunnel.stage.paid", count: data.paid },
  ];
}

export default function AdminFunnelPage() {
  const { t } = useT();
  const [windowKey, setWindowKey] = useState<string>("30d");
  const [hiddenKinds, setHiddenKinds] = useState<Set<string>>(new Set());
  const [data, setData] = useState<AdminFunnelResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);

  const excludeKinds = useMemo(() => [...hiddenKinds], [hiddenKinds]);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const r = await adminApi.getFunnel({ window: windowKey, excludeKinds });
      setData(r);
      setForbidden(false);
    } catch (err) {
      const status = (err as { status?: number } | undefined)?.status;
      if (status === 403) setForbidden(true);
      else setError(err instanceof Error ? err.message : t("adminFunnel.error.load"));
    } finally {
      setLoading(false);
    }
  }, [windowKey, excludeKinds, t]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch-on-mount; load() is the page's data source and there's no Suspense boundary above this client component.
    void load();
  }, [load]);

  const cohortOptions: FacetOption[] = (data?.cohort_counts ?? []).map((c) => ({
    value: c.account_kind,
    display: t(`audit.filter.kind.${c.account_kind}`),
    count: c.count,
  }));

  if (forbidden) {
    return (
      <div>
        <Breadcrumb items={[
          { label: t("common.crumb.console"), href: "/projects" },
          { label: t("approvals.crumb.admin") },
          { label: t("adminFunnel.crumb.funnel") },
        ]} />
        <div className="mt-4 rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50 dark:bg-amber-950/40 px-4 py-3 text-sm text-amber-800 dark:text-amber-300">
          {t("adminFunnel.accessDenied")}
        </div>
      </div>
    );
  }

  const rows = data ? stages(data) : [];
  const max = rows.length ? Math.max(1, rows[0].count) : 1;

  return (
    <div>
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
        <div>
          <Breadcrumb
            items={[
              { label: t("common.crumb.console"), href: "/projects" },
              { label: t("approvals.crumb.admin") },
              { label: t("adminFunnel.crumb.funnel") },
            ]}
          />
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-gray-100">{t("adminFunnel.title")}</h1>
          <p className="mt-0.5 text-sm text-gray-500 dark:text-gray-400">{t("adminFunnel.subtitle")}</p>
        </div>
      </div>

      <AdminTabs active="funnel" />

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
      )}

      <div className="mb-6 rounded-lg border border-blue-200 dark:border-blue-900 bg-blue-50 dark:bg-blue-950/40 px-4 py-3">
        <p className="text-sm font-medium text-blue-900 dark:text-blue-300">{t("adminFunnel.metrikaGap.title")}</p>
        <p className="mt-1 text-xs text-blue-800/80 dark:text-blue-400/80">{t("adminFunnel.metrikaGap.body")}</p>
      </div>

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div className="flex shrink-0 items-center gap-1 rounded-lg bg-gray-100 dark:bg-gray-800 p-0.5">
          {WINDOWS.map((w) => (
            <button
              key={w}
              type="button"
              onClick={() => setWindowKey(w)}
              className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                windowKey === w
                  ? "bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 shadow-sm"
                  : "text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200"
              }`}
            >
              {t(`adminFunnel.window.${w}`)}
            </button>
          ))}
        </div>
        <AuditFacetFilter
          label={t("adminFunnel.cohort.label")}
          options={cohortOptions}
          hidden={hiddenKinds}
          onChange={setHiddenKinds}
        />
      </div>

      <div className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        {loading ? (
          <div className="flex h-40 items-center justify-center">
            <Spinner size="md" />
          </div>
        ) : (
          <div className="space-y-2.5">
            {rows.map((r) => {
              const pct = max > 0 ? Math.max(r.count > 0 ? 3 : 0, (r.count / max) * 100) : 0;
              return (
                <div key={r.key} className="flex items-center gap-3">
                  <div className="w-16 shrink-0 text-xs font-medium text-gray-500 dark:text-gray-400">{t(r.labelKey)}</div>
                  <div className="relative h-6 flex-1 overflow-hidden rounded bg-gray-100 dark:bg-gray-800">
                    <div
                      className="h-full rounded bg-blue-500 dark:bg-blue-600 transition-all"
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                  <div className="w-14 shrink-0 text-right text-sm font-semibold tabular-nums text-gray-900 dark:text-gray-100">
                    {r.count}
                  </div>
                </div>
              );
            })}
          </div>
        )}
        {data?.paid_note && (
          <p className="mt-4 border-t border-gray-100 dark:border-gray-800 pt-3 text-xs text-gray-400 dark:text-gray-500">
            {t("adminFunnel.note.paid")}
          </p>
        )}
      </div>
    </div>
  );
}
