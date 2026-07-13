"use client";

import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref, localeHref } from "@/lib/site";
import { ProductHero, FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

const GUIDE_GROUPS: {
  title: { ru: string; en: string };
  items: { slug: string; label: string }[];
}[] = [
  {
    title: { ru: "Основное", en: "Core" },
    items: [
      { slug: "applications-deploy-from-github", label: "Deploy an app from GitHub" },
      { slug: "applications-deploy-image-and-compose", label: "Deploy from an image or Compose" },
      { slug: "databases-postgres", label: "Managed Postgres databases" },
      { slug: "domains-and-https", label: "Custom domains and HTTPS" },
      { slug: "monitoring-metrics-logs-alerts", label: "Monitoring: metrics, logs, alerts" },
      { slug: "mcp-ai-agents", label: "Control the cloud from an AI agent (MCP)" },
    ],
  },
  {
    title: { ru: "Серверы", en: "Servers" },
    items: [
      { slug: "app-servers-bring-your-own-vm", label: "Bring your own VM" },
      { slug: "app-servers-order-a-vm", label: "Order a managed VM" },
      { slug: "app-servers-adopt-existing-workloads", label: "Adopt existing workloads on a VM" },
      { slug: "app-servers-agency-fleet", label: "Running a fleet of client VMs" },
    ],
  },
  {
    title: { ru: "Дополнительно", en: "Advanced" },
    items: [
      { slug: "object-storage", label: "Object storage (S3-compatible)" },
      { slug: "members-and-roles", label: "Members and roles" },
      { slug: "billing-plans-and-limits", label: "Billing, plans, and limits" },
      { slug: "builds", label: "Builds and deployments" },
      { slug: "ai-models", label: "AI Models (model serving)" },
      { slug: "ai-model-approvals", label: "AI model approvals" },
    ],
  },
];

export default function DeveloperPage() {
  const { locale } = useLang();
  const copy = {
    ru: {
      title: "API и разработчикам",
      subtitle: "Всё, что делает консоль — деплой приложений, серверы, базы, домены, мониторинг — доступно через REST API с авторизацией по токену.",
      buildTitle: "Собрать на API",
      apiTitle: "REST API",
      apiDesc: "Каждый ресурс консоли доступен через /api/v1 с авторизацией по токену.",
      consoleTitle: "Открыть консоль",
      consoleDesc: "Войдите в панель — там токен доступа к API и все ресурсы проекта.",
      guidesTitle: "Руководства",
      guidesSubtitle: "Пошаговые гайды по консоли — от первого деплоя до биллинга и ролей.",
      guidesNote: "Документация пока доступна только на английском.",
      faqTitle: "Вопросы по API и разработке",
      faq: [
        { q: "Есть ли у DADA Cloud REST API?", a: "Да. Всё, что делает консоль — деплой приложений, серверы, базы, домены и мониторинг — доступно через REST API /api/v1 с авторизацией по токену." },
        { q: "Где взять токен доступа к API?", a: "Токен выдаётся в консоли: войдите в панель и откройте проект — там доступен токен доступа и все ресурсы." },
        { q: "На каком языке документация?", a: "Пошаговые руководства пока доступны на английском; сам лендинг — на русском и английском." },
      ],
    },
    en: {
      title: "API & developers",
      subtitle: "Everything the console does — app deploys, servers, databases, domains, monitoring — is available over a token-authenticated REST API.",
      buildTitle: "Build on the API",
      apiTitle: "REST API",
      apiDesc: "Every console resource is available via /api/v1 with token auth.",
      consoleTitle: "Open console",
      consoleDesc: "Log in to the panel for your API access token and all project resources.",
      guidesTitle: "Guides",
      guidesSubtitle: "Step-by-step how-tos for the console — from your first deploy to billing and roles.",
      guidesNote: "",
      faqTitle: "API and developer FAQ",
      faq: [
        { q: "Does DADA Cloud have a REST API?", a: "Yes. Everything the console does — app deploys, servers, databases, domains and monitoring — is available over the /api/v1 REST API with token auth." },
        { q: "Where do I get an API token?", a: "In the console: log in, open a project, and your access token and all resources are there." },
        { q: "What language are the docs in?", a: "The step-by-step guides are currently in English; the marketing site itself is available in Russian and English." },
      ],
    },
  }[locale];

  return (
    <>
      <FaqJsonLd path="/developer" items={copy.faq} />
      <ProductHero title={copy.title} subtitle={copy.subtitle} />
      <section className="bg-white py-20">
        <div className="mx-auto max-w-5xl px-4 sm:px-6 lg:px-8">
          <h2 className="mb-8 text-3xl font-bold tracking-tight text-slate-900">{copy.buildTitle}</h2>
        </div>
        <div className="mx-auto grid max-w-5xl gap-6 px-4 sm:px-6 md:grid-cols-2 lg:px-8">
          <div className="rounded-xl border border-slate-200 bg-white p-7">
            <h3 className="text-lg font-semibold text-slate-900">{copy.apiTitle}</h3>
            <p className="mt-2 text-sm text-slate-600">{copy.apiDesc}</p>
            <pre className="mt-4 overflow-x-auto rounded-lg bg-slate-900 p-4 text-xs text-slate-100">
{`curl -H "Authorization: Bearer $TOKEN" \\
  https://api.dada.cloud/api/v1/projects`}
            </pre>
          </div>
          <Link href={consoleHref("/projects")} className="rounded-xl border border-slate-200 bg-white p-7 transition-shadow hover:shadow-md">
            <h3 className="text-lg font-semibold text-slate-900">{copy.consoleTitle}</h3>
            <p className="mt-2 text-sm text-slate-600">{copy.consoleDesc}</p>
          </Link>
        </div>
      </section>
      <section className="bg-slate-50 py-20">
        <div className="mx-auto max-w-5xl px-4 sm:px-6 lg:px-8">
          <h2 className="text-3xl font-bold tracking-tight text-slate-900">{copy.guidesTitle}</h2>
          <p className="mt-3 max-w-2xl text-sm text-slate-600">{copy.guidesSubtitle}</p>
          {copy.guidesNote && <p className="mt-2 text-xs text-slate-400">{copy.guidesNote}</p>}
          <div className="mt-10 grid gap-8 md:grid-cols-3">
            {GUIDE_GROUPS.map((group) => (
              <div key={group.title.en}>
                <h3 className="text-sm font-semibold uppercase tracking-wide text-slate-500">
                  {group.title[locale]}
                </h3>
                <ul className="mt-4 space-y-1">
                  {group.items.map((item) => (
                    <li key={item.slug}>
                      <Link
                        href={localeHref(`/developer/${item.slug}`, locale)}
                        className="group flex items-center justify-between gap-2 rounded-lg border border-transparent px-3 py-2 text-sm text-slate-700 transition-colors hover:border-slate-200 hover:bg-white hover:text-slate-900"
                      >
                        <span>{item.label}</span>
                        <ArrowRight className="h-4 w-4 shrink-0 text-slate-300 transition-colors group-hover:text-blue-600" />
                      </Link>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>
      </section>
      <FaqList title={copy.faqTitle} items={copy.faq} />
      <CtaBand />
    </>
  );
}
