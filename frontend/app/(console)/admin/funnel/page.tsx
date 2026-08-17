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

type Row = { key: string; label: string; count: number; tone?: "error" };

function stages(data: AdminFunnelResponse): Row[] {
  return [
    { key: "signups", label: "adminFunnel.stage.signups", count: data.signups },
    { key: "app", label: "adminFunnel.stage.app", count: data.app_up },
    { key: "db", label: "adminFunnel.stage.db", count: data.db_up },
    { key: "vm", label: "adminFunnel.stage.vm", count: data.vm_up },
    { key: "box", label: "adminFunnel.stage.box", count: data.box_up },
    { key: "s3", label: "adminFunnel.stage.s3", count: data.s3_up },
    { key: "model", label: "adminFunnel.stage.model", count: data.model_up },
    { key: "paid", label: "adminFunnel.stage.paid", count: data.paid },
  ];
}

function channelLabel(channel: string): string {
  if (channel === "password") return "Email/пароль";
  if (channel === "yandex") return "Яндекс";
  if (channel === "google") return "Google";
  if (channel === "github") return "GitHub";
  return channel;
}

/** Shared bar-row visual so the Keycloak leg and the product-adoption leg
 * read as one funnel, not two different chart styles bolted together. */
function BarRows({ rows, labelWidth = "w-16" }: { rows: Row[]; labelWidth?: string }) {
  const max = rows.length ? Math.max(1, ...rows.map((r) => r.count)) : 1;
  return (
    <div className="space-y-2.5">
      {rows.map((r) => {
        const pct = max > 0 ? Math.max(r.count > 0 ? 3 : 0, (r.count / max) * 100) : 0;
        return (
          <div key={r.key} className="flex items-center gap-3">
            <div className={`${labelWidth} shrink-0 text-xs font-medium text-gray-500 dark:text-gray-400`}>{r.label}</div>
            <div className="relative h-6 flex-1 overflow-hidden rounded bg-gray-100 dark:bg-gray-800">
              <div
                className={`h-full rounded transition-all ${r.tone === "error" ? "bg-red-500 dark:bg-red-600" : "bg-blue-500 dark:bg-blue-600"}`}
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
  );
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

  const rows = data ? stages(data).map((r) => ({ ...r, label: t(r.label) })) : [];
  const reg = data?.registration_funnel;
  const regRows: Row[] = (reg?.stages ?? []).map((s) => ({
    key: s.key,
    label: s.label,
    count: s.count,
    tone: s.key === "kc_register_error" ? "error" : undefined,
  }));
  const channelsTotal = (reg?.channels ?? []).reduce((sum, c) => sum + c.count, 0);
  const channelRows: Row[] = (reg?.channels ?? []).map((c) => ({
    key: c.channel,
    label: `${channelLabel(c.channel)}${channelsTotal > 0 ? ` (${Math.round((c.count / channelsTotal) * 100)}%)` : ""}`,
    count: c.count,
  }));

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

      <div className="mb-6 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Регистрация в Keycloak: открыл форму → зарегистрировался</h2>
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
          Этапы — из отдельного счётчика Метрики на id.dada-tuda.ru (не общий счётчик консоли). «Зарегистрировано» —
          реальные строки в базе за то же окно, не сэмпл Метрики. Этапы видят только форму email/пароль — вход через
          Яндекс/Google/GitHub уходит на сторону провайдера и минует эту форму, поэтому его считает блок «По каналу»
          ниже, а не этапы.
        </p>
        {!loading && reg && !reg.available ? (
          <p className="mt-3 text-sm text-amber-600 dark:text-amber-400">{reg.note || "Метрика недоступна"}</p>
        ) : loading ? (
          <div className="flex h-24 items-center justify-center"><Spinner size="md" /></div>
        ) : (
          <div className="mt-3 grid grid-cols-1 gap-4 md:grid-cols-3">
            <div className="md:col-span-2"><BarRows rows={regRows} labelWidth="w-40" /></div>
            <div className="flex flex-col justify-center gap-3 border-t border-gray-100 dark:border-gray-800/60 pt-3 md:border-t-0 md:border-l md:pl-4 md:pt-0">
              <div>
                <p className="text-xs text-gray-500 dark:text-gray-400">Зарегистрировано (БД)</p>
                <p className="text-2xl font-semibold text-gray-900 dark:text-gray-100">{reg?.registered ?? "—"}</p>
              </div>
            </div>
          </div>
        )}
      </div>

      <div className="mb-6 rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-5 shadow-sm">
        <h2 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Регистрация по каналу: пароль vs провайдер</h2>
        <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
          Реальные строки БД, по тому, как родился аккаунт — email/пароль или брокер (Яндекс и т.п. не требуют
          подтверждения почты, конверсия там обычно выше). Аккаунты, заведённые до этой метки, в разбивку не
          попадают — их канал не записан.
        </p>
        {loading ? (
          <div className="flex h-24 items-center justify-center"><Spinner size="md" /></div>
        ) : channelRows.length === 0 ? (
          <p className="mt-3 text-sm text-gray-400 dark:text-gray-500">Нет данных за окно — либо ещё не было регистраций, либо все они старше метки канала.</p>
        ) : (
          <div className="mt-3"><BarRows rows={channelRows} labelWidth="w-40" /></div>
        )}
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
          <BarRows rows={rows} />
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
