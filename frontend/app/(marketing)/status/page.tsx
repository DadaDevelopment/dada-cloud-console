"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useLang } from "@/lib/i18n/context";
import { ProductHero, FaqList } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";
import { consoleHref, localeHref } from "@/lib/site";

const UTM = "utm_source=statusradar";
const SITE_URL = "https://cloud.dada-tuda.ru";

type StatusEntry = {
  id: string;
  name: string;
  target: string;
  reachable: boolean;
  http_status: number;
  latency_ms: number;
  tls_ok: boolean;
};

type StatusResponse = {
  vantage: string;
  updated_at: string;
  services: StatusEntry[];
};

function formatUpdatedAt(iso: string, locale: "ru" | "en"): string {
  try {
    return new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
      dateStyle: "medium",
      timeStyle: "medium",
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}

export default function StatusRadarPage() {
  const { t, locale } = useLang();
  const g = t.statusRadar;

  const [data, setData] = useState<StatusResponse | null>(null);
  const [failed, setFailed] = useState(false);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 10_000);

    fetch("/api/public/status", { signal: controller.signal })
      .then((res) => {
        if (!res.ok) throw new Error(`status ${res.status}`);
        return res.json();
      })
      .then((json: StatusResponse) => {
        if (!cancelled) {
          setData(json);
          setFailed(false);
        }
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
      clearTimeout(timeout);
      controller.abort();
    };
  }, []);

  const rows = g.services.map((svc) => {
    const live = data?.services.find((s) => s.id === svc.id);
    return { ...svc, live };
  });

  const datasetJsonLd = {
    "@context": "https://schema.org",
    "@type": "Dataset",
    "@id": `${SITE_URL}${locale === "en" ? "/en" : ""}/status#dataset`,
    name: g.datasetName,
    description: g.datasetDescription,
    inLanguage: locale,
    creator: { "@type": "Organization", name: "DADA Cloud", url: SITE_URL },
    temporalCoverage: data?.updated_at ?? undefined,
    variableMeasured: ["reachable", "http_status", "latency_ms", "tls_ok"],
    measurementTechnique: "HTTP GET probe from a server located in Russia",
  };

  return (
    <>
      <FaqJsonLd path="/status" items={g.faq} />
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(datasetJsonLd) }}
      />

      <ProductHero title={g.heroTitle} subtitle={g.heroSubtitle} />

      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-6 flex flex-wrap items-center justify-between gap-2 text-sm text-slate-500">
            <span>
              {g.vantageLabel} {data?.vantage ?? "РФ (dada-cloud, beget-prod)"}
            </span>
            {data?.updated_at && (
              <span>
                {g.updatedLabel} {formatUpdatedAt(data.updated_at, locale)}
              </span>
            )}
          </div>

          {(loading || failed) && (
            <div className="mb-6 rounded-lg border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-600">
              {loading ? g.loadingText : g.errorText}
            </div>
          )}

          <div className="overflow-x-auto rounded-xl border border-slate-200 bg-white">
            <table className="w-full min-w-[640px] text-left text-sm">
              <thead className="border-b border-slate-200 bg-slate-50 text-xs font-semibold uppercase tracking-wide text-slate-500">
                <tr>
                  <th className="px-6 py-3">{g.tableHeaders.service}</th>
                  <th className="px-6 py-3">{g.tableHeaders.availability}</th>
                  <th className="px-6 py-3">{g.tableHeaders.httpStatus}</th>
                  <th className="px-6 py-3">{g.tableHeaders.latency}</th>
                  <th className="px-6 py-3">{g.tableHeaders.tls}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-200">
                {rows.map((row) => (
                  <tr key={row.id}>
                    <td className="px-6 py-4 align-top font-medium text-slate-900">{row.name}</td>
                    <td className="px-6 py-4 align-top">
                      <span className="inline-flex items-center gap-2">
                        <span
                          className={
                            "h-2.5 w-2.5 shrink-0 rounded-full " +
                            (row.live === undefined
                              ? "bg-slate-300"
                              : row.live.reachable
                                ? "bg-emerald-500"
                                : "bg-red-500")
                          }
                        />
                        <span className="text-slate-700">
                          {row.live === undefined
                            ? g.unknownLabel
                            : row.live.reachable
                              ? g.reachableLabel
                              : g.unreachableLabel}
                        </span>
                      </span>
                    </td>
                    <td className="px-6 py-4 align-top text-slate-700">
                      {row.live?.http_status ?? "-"}
                    </td>
                    <td className="px-6 py-4 align-top text-slate-700">
                      {row.live?.latency_ms ?? "-"}
                    </td>
                    <td className="px-6 py-4 align-top text-slate-700">
                      {row.live === undefined ? "-" : row.live.tls_ok ? "OK" : g.unreachableLabel}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mt-8 rounded-xl border border-amber-200 bg-amber-50 p-6">
            <h3 className="text-base font-semibold text-slate-900">{g.disclaimerTitle}</h3>
            <p className="mt-2 text-sm text-slate-600">{g.disclaimer}</p>
          </div>
        </div>
      </section>

      <section className="bg-slate-50 py-20">
        <div className="mx-auto max-w-3xl px-4 sm:px-6 lg:px-8">
          <div className="rounded-xl border border-slate-200 bg-white p-8">
            <h3 className="text-xl font-bold tracking-tight text-slate-900">{g.paymentTitle}</h3>
            <p className="mt-3 text-sm text-slate-600">{g.paymentText}</p>
          </div>
        </div>
      </section>

      <FaqList title={g.faqTitle} items={g.faq} />

      <section className="mkt-hero-gradient">
        <div className="mx-auto max-w-7xl px-4 py-16 text-center sm:px-6 lg:px-8">
          <h2 className="text-3xl font-bold tracking-tight text-white sm:text-4xl">{g.ctaTitle}</h2>
          <p className="mx-auto mt-4 max-w-xl text-lg text-white/70">{g.ctaSubtitle}</p>
          <div className="mt-8 flex flex-wrap justify-center gap-3">
            <Link
              href={`${localeHref("/migrate-vercel", locale)}?${UTM}`}
              className="rounded-md bg-blue-600 px-8 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
            >
              {g.ctaPrimaryLabel}
            </Link>
            <Link
              href={`${consoleHref("/login")}?${UTM}`}
              className="rounded-md border border-white/20 px-8 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/5"
            >
              {g.ctaSecondaryLabel}
            </Link>
          </div>
        </div>
      </section>
    </>
  );
}
