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
      intro: [
        "API работает с теми же объектами, что и консоль: проект, приложение, база данных, домен, сервер. Одно приложение — это репозиторий или образ плюс переменные окружения; всё остальное платформа собирает сама, поэтому в запросе не нужно описывать инфраструктуру.",
        "Авторизация — токен в заголовке Authorization. Токен привязан к вашей роли в проекте: что нельзя сделать руками в консоли, того не сделает и запрос. Для выкатки из чужой CI отдельно выдаётся деплой-хук на конкретное приложение, чтобы не носить в пайплайне полноценный токен.",
        "Операции, которые меняют состояние, асинхронные: запрос возвращает идентификатор операции, а её статус читается отдельным вызовом. Логи и метрики отдаются тем же API, так что дежурный скрипт или AI-агент видят ровно то же, что видно в панели.",
      ],
      faqTitle: "Вопросы по API и разработке",
      faq: [
        { q: "Есть ли у DADA Cloud REST API?", a: "Да. Всё, что делает консоль — деплой приложений, серверы, базы, домены и мониторинг — доступно через REST API /api/v1 с авторизацией по токену." },
        { q: "Где взять токен доступа к API?", a: "Токен выдаётся в консоли: войдите в панель и откройте проект — там доступен токен доступа и все ресурсы." },
        { q: "На каком языке документация?", a: "Пошаговые руководства пока доступны на английском; сам лендинг — на русском и английском." },
        { q: "Чем API отличается от деплой-хука?", a: "Деплой-хук — это один защищённый токеном адрес, который запускает пересборку конкретного приложения; его удобно дёргать из чужой CI. REST API даёт полное управление: создать проект, поднять базу, привязать домен, прочитать метрики и логи." },
        { q: "Можно ли управлять облаком из AI-агента?", a: "Да. Кроме REST есть MCP-сервер: агент подключается к нему и работает с проектами, приложениями, базами и логами теми же правами, что и ваш токен. Отдельное руководство по MCP есть в списке ниже." },
        { q: "Есть ли ограничения на частоту запросов?", a: "Чтение метрик и логов кэшируется на стороне платформы, поэтому опрос раз в несколько секунд не создаёт нагрузки. Операции изменения (деплой, создание ресурсов) идут через очередь задач и возвращают идентификатор операции, по которому отслеживается статус." },
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
      intro: [
        "The API works with the same objects as the console: project, app, database, domain, server. An app is a repository or an image plus environment variables; the platform assembles everything else, so a request never has to describe infrastructure.",
        "Auth is a token in the Authorization header. The token carries your role in the project: whatever you cannot do by hand in the console, a request cannot do either. For deploys from someone else's CI there is a per-app deploy hook, so a pipeline never has to carry a full token.",
        "State-changing calls are asynchronous: the request returns an operation id and the status is read separately. Logs and metrics come from the same API, so an on-call script or an AI agent sees exactly what the console shows.",
      ],
      faqTitle: "API and developer FAQ",
      faq: [
        { q: "Does DADA Cloud have a REST API?", a: "Yes. Everything the console does — app deploys, servers, databases, domains and monitoring — is available over the /api/v1 REST API with token auth." },
        { q: "Where do I get an API token?", a: "In the console: log in, open a project, and your access token and all resources are there." },
        { q: "What language are the docs in?", a: "The step-by-step guides are currently in English; the marketing site itself is available in Russian and English." },
        { q: "How is the API different from a deploy hook?", a: "A deploy hook is a single token-protected URL that triggers a rebuild of one app; it is handy to call from someone else's CI. The REST API is full control: create a project, provision a database, attach a domain, read metrics and logs." },
        { q: "Can an AI agent drive the cloud?", a: "Yes. Besides REST there is an MCP server: an agent connects to it and works with projects, apps, databases and logs under exactly the permissions your token has. There is a dedicated MCP guide in the list below." },
        { q: "Are there rate limits?", a: "Metric and log reads are cached on the platform side, so polling every few seconds costs nothing. Mutating operations (deploys, resource creation) go through a task queue and return an operation id you can track." },
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
          <div className="mb-10 space-y-4">
            {copy.intro.map((par) => (
              <p key={par.slice(0, 24)} className="max-w-3xl text-base text-slate-600">
                {par}
              </p>
            ))}
          </div>
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
