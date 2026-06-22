"use client";

import Link from "next/link";
import {
  Server,
  Boxes,
  Database,
  HardDrive,
  Globe,
  LineChart,
  GitBranch,
  ShieldCheck,
  Activity,
  ArrowRight,
  Check,
} from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref } from "@/lib/site";
import { CtaBand } from "@/components/marketing/sections";

const SERVICE_ICONS: Record<string, React.ComponentType<{ className?: string }>> = {
  vps: Server,
  k8s: Boxes,
  db: Database,
  s3: HardDrive,
  cdn: Globe,
  monitoring: LineChart,
};

const WHY_ICONS = [GitBranch, ShieldCheck, Activity];

export default function HomePage() {
  const { t } = useLang();

  return (
    <>
      {/* Hero */}
      <section className="mkt-hero-gradient">
        <div className="mkt-grid-bg">
          <div className="mx-auto max-w-7xl px-4 py-24 sm:px-6 lg:px-8 lg:py-32">
            <span className="mb-5 inline-flex items-center gap-2 rounded-full border border-blue-400/30 bg-blue-500/10 px-3 py-1 text-xs font-semibold text-blue-300">
              <span className="h-1.5 w-1.5 rounded-full bg-blue-400" />
              GitOps Cloud Platform
            </span>
            <h1 className="max-w-4xl text-4xl font-bold leading-tight tracking-tight text-white sm:text-5xl lg:text-6xl">
              {t.home.heroTitle}
            </h1>
            <p className="mt-6 max-w-2xl text-lg text-white/70 sm:text-xl">{t.home.heroSubtitle}</p>
            <div className="mt-9 flex flex-wrap gap-3">
              <Link
                href={consoleHref("/login")}
                className="inline-flex items-center gap-2 rounded-md bg-blue-600 px-7 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
              >
                {t.home.heroPrimary}
                <ArrowRight className="h-4 w-4" />
              </Link>
              <Link
                href="/developer"
                className="rounded-md border border-white/20 px-7 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/5"
              >
                {t.home.heroSecondary}
              </Link>
            </div>
          </div>
        </div>
      </section>

      {/* Services */}
      <section className="bg-white py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{t.home.servicesTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{t.home.servicesSubtitle}</p>
          </div>
          <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
            {t.home.services.map((s) => {
              const Icon = SERVICE_ICONS[s.key] ?? Server;
              return (
                <Link
                  key={s.key}
                  href={s.href}
                  className="group relative flex flex-col rounded-xl border border-slate-200 bg-white p-6 transition-all hover:border-blue-300 hover:shadow-lg"
                >
                  {s.badge && (
                    <span className="absolute right-5 top-5 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-semibold text-amber-700">
                      {s.badge}
                    </span>
                  )}
                  <div className="mb-4 flex h-11 w-11 items-center justify-center rounded-lg bg-blue-600 text-white">
                    <Icon className="h-6 w-6" />
                  </div>
                  <h3 className="text-lg font-semibold text-slate-900">{s.title}</h3>
                  <p className="mt-2 flex-1 text-sm text-slate-600">{s.desc}</p>
                  <span className="mt-4 inline-flex items-center gap-1 text-sm font-medium text-blue-600">
                    {t.common.learnMore}
                    <ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-1" />
                  </span>
                </Link>
              );
            })}
          </div>
        </div>
      </section>

      {/* Why */}
      <section className="bg-slate-50 py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="mb-12 max-w-2xl">
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{t.home.whyTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{t.home.whySubtitle}</p>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {t.home.why.map((f, i) => {
              const Icon = WHY_ICONS[i] ?? GitBranch;
              return (
                <div key={f.title} className="rounded-xl border border-slate-200 bg-white p-7">
                  <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-blue-50 text-blue-600">
                    <Icon className="h-6 w-6" />
                  </div>
                  <h3 className="text-lg font-semibold text-slate-900">{f.title}</h3>
                  <p className="mt-2 text-sm text-slate-600">{f.desc}</p>
                </div>
              );
            })}
          </div>
        </div>
      </section>

      {/* Control panel showcase */}
      <section className="bg-white py-20">
        <div className="mx-auto grid max-w-7xl items-center gap-12 px-4 sm:px-6 lg:grid-cols-2 lg:px-8">
          <div>
            <h2 className="text-3xl font-bold tracking-tight text-slate-900">{t.home.panelTitle}</h2>
            <p className="mt-3 text-lg text-slate-600">{t.home.panelSubtitle}</p>
            <ul className="mt-6 space-y-3">
              {t.home.panelBullets.map((b) => (
                <li key={b} className="flex items-start gap-3 text-slate-700">
                  <Check className="mt-0.5 h-5 w-5 shrink-0 text-blue-600" />
                  <span>{b}</span>
                </li>
              ))}
            </ul>
            <Link
              href={consoleHref("/login")}
              className="mt-8 inline-flex items-center gap-2 rounded-md bg-blue-600 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
            >
              {t.common.getStarted}
              <ArrowRight className="h-4 w-4" />
            </Link>
          </div>
          {/* Stylized panel mock */}
          <div className="rounded-2xl border border-slate-200 bg-slate-900 p-2 shadow-2xl">
            <div className="flex items-center gap-1.5 px-3 py-2">
              <span className="h-3 w-3 rounded-full bg-red-400" />
              <span className="h-3 w-3 rounded-full bg-amber-400" />
              <span className="h-3 w-3 rounded-full bg-green-400" />
            </div>
            <div className="rounded-xl bg-slate-950 p-5">
              <div className="flex items-center justify-between">
                <span className="text-sm font-semibold text-white">Prod k8s</span>
                <span className="rounded-full bg-green-500/20 px-2 py-0.5 text-xs text-green-400">
                  ● Started
                </span>
              </div>
              <div className="mt-4 grid grid-cols-2 gap-3">
                {["CPU 112%", "Memory 64%", "Nodes 3/3", "Uptime 99.9%"].map((m) => (
                  <div key={m} className="rounded-lg bg-white/5 px-3 py-3 text-xs text-white/70">
                    {m}
                  </div>
                ))}
              </div>
              <div className="mt-3 h-24 rounded-lg bg-gradient-to-t from-blue-600/30 to-transparent" />
            </div>
          </div>
        </div>
      </section>

      {/* Stats */}
      <section className="bg-slate-50 py-16">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <div className="grid grid-cols-2 gap-8 md:grid-cols-4">
            {t.home.stats.map((s) => (
              <div key={s.label} className="text-center">
                <div className="text-4xl font-bold text-slate-900">{s.value}</div>
                <div className="mt-1 text-sm text-slate-600">{s.label}</div>
              </div>
            ))}
          </div>
          <p className="mt-6 text-center text-xs text-slate-400">{t.home.statsNote}</p>
        </div>
      </section>

      <CtaBand />
    </>
  );
}
