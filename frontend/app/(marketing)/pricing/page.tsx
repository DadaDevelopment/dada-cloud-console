"use client";

import { useState } from "react";
import Link from "next/link";
import { Check } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref } from "@/lib/site";
import { ProductHero, CtaBand } from "@/components/marketing/sections";
import { clsx } from "clsx";
import { api } from "@/lib/api";
import type { BillingPlanKey, RecommendPlanResponse } from "@/lib/api";

const PLAN_QUOTAS: Record<string, { apps: number | null; databases: number | null; storage_gb: number | null; domains: number | null; members: number | null }> = {
  free:       { apps: 2,    databases: 1,  storage_gb: 2,   domains: 1,  members: 1  },
  startup:    { apps: 5,    databases: 2,  storage_gb: 10,  domains: 5,  members: 3  },
  business:   { apps: 20,   databases: 10, storage_gb: 100, domains: 20, members: 10 },
  enterprise: { apps: null, databases: null, storage_gb: null, domains: null, members: null },
};

function recommendClientSide(need: { apps: number; databases: number; domains: number; members: number; storage_gb: number }): BillingPlanKey {
  const order: BillingPlanKey[] = ["free", "startup", "business", "enterprise"];
  for (const key of order) {
    const q = PLAN_QUOTAS[key];
    if (q.apps === null) return "enterprise";
    if (
      need.apps <= q.apps &&
      need.databases <= (q.databases ?? 0) &&
      need.storage_gb <= (q.storage_gb ?? 0) &&
      need.domains <= (q.domains ?? 0) &&
      need.members <= (q.members ?? 0)
    ) {
      return key;
    }
  }
  return "enterprise";
}

function QuotaRow({ label, value }: { label: string; value: string }) {
  return (
    <li className="flex items-center justify-between gap-2 border-t border-slate-100 py-2 text-sm first:border-0">
      <span className="text-slate-500">{label}</span>
      <span className="font-medium text-slate-800">{value}</span>
    </li>
  );
}

function PlanRecommender() {
  const { t } = useLang();
  const r = t.pricing.recommender;

  const [apps, setApps] = useState(1);
  const [databases, setDatabases] = useState(1);
  const [domains, setDomains] = useState(1);
  const [members, setMembers] = useState(1);
  const [storage, setStorage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<{ key: BillingPlanKey; name: string } | null>(null);
  const [fallbackNote, setFallbackNote] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    setResult(null);
    setFallbackNote(false);

    const need = { apps, databases, domains, members, storage_gb: storage };
    let recommended: BillingPlanKey;

    try {
      const resp = await api.post<RecommendPlanResponse>("/api/v1/billing/recommend-plan", need);
      recommended = resp.recommended;
    } catch {
      recommended = recommendClientSide(need);
      setFallbackNote(true);
    }

    const plan = t.pricing.plans.find((p) => p.key === recommended);
    setResult({ key: recommended, name: plan?.name ?? recommended });
    setLoading(false);
  }

  return (
    <div className="mx-auto max-w-2xl rounded-2xl border border-slate-200 bg-slate-50 p-8">
      <h2 className="text-xl font-semibold text-slate-900">{r.title}</h2>
      <p className="mt-1 text-sm text-slate-500">{r.subtitle}</p>
      <form onSubmit={handleSubmit} className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {(
          [
            { label: r.labelApps, value: apps, set: setApps },
            { label: r.labelDatabases, value: databases, set: setDatabases },
            { label: r.labelDomains, value: domains, set: setDomains },
            { label: r.labelMembers, value: members, set: setMembers },
            { label: r.labelStorage, value: storage, set: setStorage },
          ] as { label: string; value: number; set: (n: number) => void }[]
        ).map(({ label, value, set }) => (
          <label key={label} className="flex flex-col gap-1">
            <span className="text-xs font-medium text-slate-600">{label}</span>
            <input
              type="number"
              min={0}
              value={value}
              onChange={(e) => set(Math.max(0, parseInt(e.target.value) || 0))}
              className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-900 focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </label>
        ))}
        <div className="flex items-end sm:col-span-2 lg:col-span-1">
          <button
            type="submit"
            disabled={loading}
            className="w-full rounded-lg bg-blue-600 px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-blue-700 disabled:opacity-60"
          >
            {loading ? r.loading : r.submit}
          </button>
        </div>
      </form>
      {result && (
        <div className="mt-5 rounded-xl border border-blue-200 bg-blue-50 px-5 py-4">
          <p className="text-sm text-slate-600">{r.result}</p>
          <p className="mt-1 text-2xl font-bold text-blue-700">{result.name}</p>
          {fallbackNote && (
            <p className="mt-2 text-xs text-slate-400">{r.errorFallback}</p>
          )}
        </div>
      )}
    </div>
  );
}

export default function PricingPage() {
  const { t, locale } = useLang();

  const quotaLabels = t.pricing.plans[0]
    ? Object.keys(t.pricing.plans[0].quotaMatrix)
    : [];

  const matrixLabelMap: Record<string, string> = {
    apps: t.pricing.plans[0]?.features[0]?.split(" ")[1] ?? "Приложения",
    databases: "Базы данных",
    storage: "Хранилище",
    domains: "Домены",
    environments: "Среды",
    members: "Участники",
    backups: "Бэкапы",
    support: "Поддержка",
  };

  const isRu = locale !== "en";

  const matrixLabelsRu: Record<string, string> = {
    apps: "Приложения",
    databases: "Базы данных",
    storage: "Хранилище",
    domains: "Домены",
    environments: "Среды",
    members: "Участники",
    backups: "Бэкапы",
    support: "Поддержка",
  };

  const matrixLabelsEn: Record<string, string> = {
    apps: "Applications",
    databases: "Databases",
    storage: "Storage",
    domains: "Domains",
    environments: "Environments",
    members: "Members",
    backups: "Backups",
    support: "Support",
  };

  const labels = isRu ? matrixLabelsRu : matrixLabelsEn;

  void quotaLabels;
  void matrixLabelMap;

  return (
    <>
      <ProductHero title={t.pricing.heroTitle} subtitle={t.pricing.heroSubtitle} />
      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="grid gap-6 lg:grid-cols-4">
            {t.pricing.plans.map((p) => (
              <div
                key={p.key}
                className={clsx(
                  "flex flex-col rounded-2xl border bg-white p-7",
                  p.highlight
                    ? "border-blue-600 shadow-xl ring-1 ring-blue-600"
                    : "border-slate-200",
                )}
              >
                {p.highlight && (
                  <span className="mb-3 inline-block w-fit rounded-full bg-blue-600 px-3 py-1 text-xs font-semibold text-white">
                    Popular
                  </span>
                )}
                <h3 className="text-lg font-bold text-slate-900">{p.name}</h3>
                <p className="mt-1 text-xs text-slate-500">{p.tagline}</p>
                <div className="mt-4 flex items-baseline gap-1">
                  <span className="text-3xl font-bold text-slate-900">{p.price}</span>
                  {p.period && <span className="text-sm text-slate-400">{p.period}</span>}
                </div>

                <ul className="mt-5 space-y-0.5">
                  {(Object.keys(p.quotaMatrix) as (keyof typeof p.quotaMatrix)[]).map((k) => (
                    <QuotaRow key={k} label={labels[k] ?? k} value={p.quotaMatrix[k]} />
                  ))}
                </ul>

                <ul className="mt-5 flex-1 space-y-2">
                  {p.features.map((f) => (
                    <li key={f} className="flex items-start gap-2 text-xs text-slate-600">
                      <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-blue-500" />
                      <span>{f}</span>
                    </li>
                  ))}
                </ul>

                <Link
                  href={p.key === "enterprise" ? "mailto:hello@dada-tuda.ru" : consoleHref("/register")}
                  className={clsx(
                    "mt-6 rounded-lg px-5 py-2.5 text-center text-sm font-semibold transition-colors",
                    p.highlight
                      ? "bg-blue-600 text-white hover:bg-blue-700"
                      : p.key === "enterprise"
                        ? "border border-slate-800 text-slate-900 hover:bg-slate-50"
                        : "border border-slate-200 text-slate-900 hover:bg-slate-50",
                  )}
                >
                  {p.cta}
                </Link>
              </div>
            ))}
          </div>

          <div className="mt-16">
            <PlanRecommender />
          </div>
        </div>
      </section>
      <CtaBand />
    </>
  );
}
