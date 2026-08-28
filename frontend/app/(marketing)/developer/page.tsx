"use client";

import Link from "next/link";
import {
  ArrowRight,
  Bot,
  Boxes,
  BrainCircuit,
  CreditCard,
  Database,
  GitBranch,
  Globe,
  HardDrive,
  Hammer,
  LineChart,
  ListTree,
  MousePointerClick,
  Package,
  ServerCog,
  ShieldCheck,
  Sparkles,
  Users,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref, localeHref } from "@/lib/site";
import { FaqList, CtaBand } from "@/components/marketing/sections";
import { FaqJsonLd } from "@/components/marketing/faq-jsonld";

type Bilingual = { ru: string; en: string };

interface GuideCard {
  slug: string;
  icon: LucideIcon;
  title: Bilingual;
  desc: Bilingual;
  /** Rendered with an accent border and a filled icon -- the entry points we want found first. */
  featured?: boolean;
}

/**
 * The guide catalog, rendered as cards.
 *
 * It used to be three columns of bare English link text sitting below two
 * screens of API prose. One user went looking for the MCP documentation and did
 * not find it; another did not register the list as documentation at all. Each
 * entry now carries a Russian title, a one-line description of the job it does,
 * and an icon, so the page reads as a catalog rather than a footer.
 */
const GUIDE_GROUPS: { title: Bilingual; items: GuideCard[] }[] = [
  {
    title: { ru: "Начать", en: "Get started" },
    items: [
      {
        slug: "applications-deploy-from-github",
        icon: GitBranch,
        title: { ru: "Деплой из GitHub", en: "Deploy an app from GitHub" },
        desc: {
          ru: "Подключить репозиторий, собрать и выкатить приложение — и пересобирать его на каждый push.",
          en: "Connect a repository, build and ship the app, and rebuild it on every push.",
        },
      },
      {
        slug: "applications-deploy-image-and-compose",
        icon: Package,
        title: { ru: "Деплой из образа или Compose", en: "Deploy from an image or Compose" },
        desc: {
          ru: "Готовый образ из реестра или docker-compose.yml — без сборки на нашей стороне.",
          en: "A ready image from a registry or a docker-compose.yml, with no build on our side.",
        },
      },
      {
        slug: "deploy-button-badge",
        icon: MousePointerClick,
        title: { ru: "Кнопка «Deploy on Dada»", en: 'The "Deploy on Dada" button' },
        desc: {
          ru: "Бейдж в README, который разворачивает ваш публичный репозиторий в один клик.",
          en: "A README badge that deploys your public repository in one click.",
        },
      },
      {
        slug: "databases-postgres",
        icon: Database,
        title: { ru: "Управляемый Postgres", en: "Managed Postgres databases" },
        desc: {
          ru: "Поднять базу, получить строку подключения, включить бэкапы и восстановиться из них.",
          en: "Provision a database, get its connection string, enable backups and restore from them.",
        },
      },
      {
        slug: "domains-and-https",
        icon: Globe,
        title: { ru: "Домены и HTTPS", en: "Custom domains and HTTPS" },
        desc: {
          ru: "Привязать свой домен, подтвердить владение, получить сертификат автоматически.",
          en: "Attach your own domain, verify ownership, and get a certificate automatically.",
        },
      },
      {
        slug: "monitoring-metrics-logs-alerts",
        icon: LineChart,
        title: { ru: "Мониторинг: метрики, логи, алерты", en: "Monitoring: metrics, logs, alerts" },
        desc: {
          ru: "Где смотреть, что происходит с приложением, и как узнать о падении раньше пользователей.",
          en: "Where to see what the app is doing, and how to hear about a failure before your users do.",
        },
      },
    ],
  },
  {
    title: { ru: "AI-агенты (MCP)", en: "AI agents (MCP)" },
    items: [
      {
        slug: "mcp-ai-agents",
        icon: Bot,
        featured: true,
        title: { ru: "Подключить MCP-сервер", en: "Connect the MCP server" },
        desc: {
          ru: "Адрес сервера, вход через браузер и настройка Claude Code, Claude Desktop, Cursor и любого другого клиента.",
          en: "The server URL, browser sign-in, and setup for Claude Code, Claude Desktop, Cursor and any other client.",
        },
      },
      {
        slug: "mcp-tool-reference",
        icon: ListTree,
        featured: true,
        title: { ru: "Справочник: все 60 инструментов", en: "Reference: all 60 tools" },
        desc: {
          ru: "Каждый инструмент с аргументами, что меняет и что возвращает — и чего на поверхности нет намеренно.",
          en: "Every tool with its arguments, what it changes, what it returns, and what is deliberately absent.",
        },
      },
      {
        slug: "mcp-recipes",
        icon: Sparkles,
        featured: true,
        title: { ru: "Рецепты: готовые сценарии", en: "Recipes: worked flows" },
        desc: {
          ru: "Точные последовательности вызовов за деплоем, базой, песочницей и диагностикой падения.",
          en: "The exact call sequences behind a deploy, a database, a sandbox and a failure diagnosis.",
        },
      },
    ],
  },
  {
    title: { ru: "Серверы", en: "Servers" },
    items: [
      {
        slug: "app-servers-bring-your-own-vm",
        icon: ServerCog,
        title: { ru: "Подключить свою VM", en: "Bring your own VM" },
        desc: {
          ru: "Взять существующий сервер под управление платформы, не переезжая в кластер.",
          en: "Put an existing server under platform management without moving into the cluster.",
        },
      },
      {
        slug: "app-servers-order-a-vm",
        icon: ServerCog,
        title: { ru: "Заказать VM", en: "Order a managed VM" },
        desc: {
          ru: "Выбрать конфигурацию и получить готовый сервер с агентом на борту.",
          en: "Pick a configuration and get a ready server with the agent already on it.",
        },
      },
      {
        slug: "app-servers-adopt-existing-workloads",
        icon: Boxes,
        title: { ru: "Забрать то, что уже крутится", en: "Adopt existing workloads" },
        desc: {
          ru: "Импортировать запущенные на сервере контейнеры и Compose-стеки в консоль.",
          en: "Import the containers and Compose stacks already running on a server into the console.",
        },
      },
      {
        slug: "app-servers-agency-fleet",
        icon: Users,
        title: { ru: "Флот клиентских VM", en: "Running a fleet of client VMs" },
        desc: {
          ru: "Как агентству держать десятки клиентских серверов в одном месте, а не в ящике с SSH-ключами.",
          en: "How an agency keeps dozens of client servers in one place instead of a drawer of SSH keys.",
        },
      },
    ],
  },
  {
    title: { ru: "Дополнительно", en: "Advanced" },
    items: [
      {
        slug: "object-storage",
        icon: HardDrive,
        title: { ru: "Объектное хранилище (S3)", en: "Object storage (S3-compatible)" },
        desc: {
          ru: "Бакет, ключи доступа и подключение из приложения обычным S3 SDK.",
          en: "A bucket, access keys, and connecting from an app with any ordinary S3 SDK.",
        },
      },
      {
        slug: "members-and-roles",
        icon: Users,
        title: { ru: "Участники и роли", en: "Members and roles" },
        desc: {
          ru: "Кого пускать в проект и что каждая роль реально может сделать.",
          en: "Who gets into a project and what each role can actually do.",
        },
      },
      {
        slug: "billing-plans-and-limits",
        icon: CreditCard,
        title: { ru: "Биллинг, тарифы и лимиты", en: "Billing, plans, and limits" },
        desc: {
          ru: "За что списываются деньги, где посмотреть расход и какие лимиты стоят по умолчанию.",
          en: "What you are charged for, where to see spend, and which limits apply by default.",
        },
      },
      {
        slug: "builds",
        icon: Hammer,
        title: { ru: "Сборки и деплои", en: "Builds and deployments" },
        desc: {
          ru: "Как устроен пайплайн сборки, где логи и что делать, когда сборка красная.",
          en: "How the build pipeline works, where the logs are, and what to do when a build goes red.",
        },
      },
      {
        slug: "llm-providers",
        icon: BrainCircuit,
        title: { ru: "LLM-провайдеры: GPT и Claude из России", en: "LLM providers: GPT and Claude from Russia" },
        desc: {
          ru: "OpenAI-совместимый эндпоинт без VPN: со своим ключом провайдера бесплатно или на нашем ключе за роутинг.",
          en: "An OpenAI-compatible endpoint with no VPN: free on your own provider key, or on ours for a routing fee.",
        },
      },
      {
        slug: "ai-models",
        icon: BrainCircuit,
        title: { ru: "AI Models: свои модели", en: "AI Models (model serving)" },
        desc: {
          ru: "Запустить модель на GPU и обращаться к ней по OpenAI-совместимому API.",
          en: "Run a model on a GPU and call it over an OpenAI-compatible API.",
        },
      },
      {
        slug: "ai-model-approvals",
        icon: ShieldCheck,
        title: { ru: "Согласование GPU-моделей", en: "AI model approvals" },
        desc: {
          ru: "Почему запуск на GPU проходит через очередь согласования и как её пройти.",
          en: "Why a GPU launch goes through an approval queue and how to get through it.",
        },
      },
    ],
  },
];

const API_EXAMPLE = 'curl -H "Authorization: Bearer $TOKEN" https://console.dada-tuda.ru/api/v1/projects';

export default function DeveloperPage() {
  const { locale } = useLang();
  const copy = {
    ru: {
      title: "Документация",
      subtitle:
        "Пошаговые руководства по консоли — от первого деплоя до баз, доменов, мониторинга и управления облаком из AI-агента. Плюс REST API и MCP-сервер для тех, кто автоматизирует.",
      heroGuides: "К руководствам",
      heroMcp: "MCP-сервер",
      guidesTitle: "Руководства",
      guidesSubtitle: "Каждая карточка — одна задача целиком, от начала до работающего результата.",
      guidesNote: "Все руководства на русском; на английском остался только справочник инструментов MCP.",
      buildTitle: "Автоматизация: REST API и MCP",
      apiTitle: "REST API",
      apiDesc: "Каждый ресурс консоли доступен через /api/v1 с авторизацией по токену.",
      mcpTitle: "MCP-сервер",
      mcpDesc: "60 инструментов для AI-агента: те же права, что у вашего аккаунта, вход через браузер.",
      mcpLink: "Как подключить",
      consoleTitle: "Открыть консоль",
      consoleDesc: "Войдите в панель — там токен доступа к API и все ресурсы проекта.",
      readLink: "Читать",
      intro: [
        "API работает с теми же объектами, что и консоль: проект, приложение, база данных, домен, сервер. Одно приложение — это репозиторий или образ плюс переменные окружения; всё остальное платформа собирает сама, поэтому в запросе не нужно описывать инфраструктуру.",
        "Авторизация — токен в заголовке Authorization. Токен привязан к вашей роли в проекте: что нельзя сделать руками в консоли, того не сделает и запрос. Для выкатки из чужой CI отдельно выдаётся деплой-хук на конкретное приложение, чтобы не носить в пайплайне полноценный токен.",
        "Операции, которые меняют состояние, асинхронные: запрос возвращает идентификатор операции, а её статус читается отдельным вызовом. Логи отдаются тем же API, так что дежурный скрипт или AI-агент видят ровно то же, что видно в панели.",
      ],
      faqTitle: "Вопросы по API и разработке",
      faq: [
        { q: "Где документация DADA Cloud?", a: "На этой странице: раздел «Руководства» — пошаговые гайды по консоли, от первого деплоя до биллинга, ролей и подключения AI-агента по MCP. Все гайды на русском." },
        { q: "Есть ли у DADA Cloud REST API?", a: "Да. Всё, что делает консоль — деплой приложений, серверы, базы, домены и мониторинг — доступно через REST API /api/v1 с авторизацией по токену." },
        { q: "Где взять токен доступа к API?", a: "Токен выдаётся в консоли: войдите в панель и откройте проект — там доступен токен доступа и все ресурсы." },
        { q: "На каком языке документация?", a: "Руководства доступны на русском и английском: /developer/* отдаёт русский текст, /en/developer/* — английский. На английском остался только машинный справочник инструментов MCP." },
        { q: "Чем API отличается от деплой-хука?", a: "Деплой-хук — это один защищённый токеном адрес, который запускает пересборку конкретного приложения; его удобно дёргать из чужой CI. REST API даёт полное управление: создать проект, поднять базу, привязать домен, прочитать логи." },
        { q: "Можно ли управлять облаком из AI-агента?", a: "Да. Кроме REST есть MCP-сервер: агент получает 60 инструментов и работает с проектами, приложениями, базами и логами теми же правами, что и ваш аккаунт. В разделе «AI-агенты (MCP)» выше — подключение, полный справочник инструментов и готовые сценарии." },
        { q: "Есть ли ограничения на частоту запросов?", a: "Чтение метрик и логов кэшируется на стороне платформы, поэтому опрос раз в несколько секунд не создаёт нагрузки. Операции изменения (деплой, создание ресурсов) идут через очередь задач и возвращают идентификатор операции, по которому отслеживается статус." },
      ],
    },
    en: {
      title: "Documentation",
      subtitle:
        "Step-by-step guides for the console — from your first deploy to databases, domains, monitoring and driving the cloud from an AI agent. Plus the REST API and the MCP server for everything you want automated.",
      heroGuides: "Browse the guides",
      heroMcp: "MCP server",
      guidesTitle: "Guides",
      guidesSubtitle: "Each card is one whole job, from nothing to a working result.",
      guidesNote: "",
      buildTitle: "Automation: REST API and MCP",
      apiTitle: "REST API",
      apiDesc: "Every console resource is available via /api/v1 with token auth.",
      mcpTitle: "MCP server",
      mcpDesc: "60 tools for an AI agent: exactly your account's permissions, browser sign-in, no tokens in config files.",
      mcpLink: "How to connect",
      consoleTitle: "Open console",
      consoleDesc: "Log in to the panel for your API access token and all project resources.",
      readLink: "Read",
      intro: [
        "The API works with the same objects as the console: project, app, database, domain, server. An app is a repository or an image plus environment variables; the platform assembles everything else, so a request never has to describe infrastructure.",
        "Auth is a token in the Authorization header. The token carries your role in the project: whatever you cannot do by hand in the console, a request cannot do either. For deploys from someone else's CI there is a per-app deploy hook, so a pipeline never has to carry a full token.",
        "State-changing calls are asynchronous: the request returns an operation id and the status is read separately. Logs come from the same API, so an on-call script or an AI agent sees exactly what the console shows.",
      ],
      faqTitle: "API and developer FAQ",
      faq: [
        { q: "Where are the DADA Cloud docs?", a: "On this page: the Guides section is a set of step-by-step how-tos for the console, from your first deploy to billing, roles and connecting an AI agent over MCP." },
        { q: "Does DADA Cloud have a REST API?", a: "Yes. Everything the console does — app deploys, servers, databases, domains and monitoring — is available over the /api/v1 REST API with token auth." },
        { q: "Where do I get an API token?", a: "In the console: log in, open a project, and your access token and all resources are there." },
        { q: "What language are the docs in?", a: "The step-by-step guides are currently in English; the marketing site itself is available in Russian and English." },
        { q: "How is the API different from a deploy hook?", a: "A deploy hook is a single token-protected URL that triggers a rebuild of one app; it is handy to call from someone else's CI. The REST API is full control: create a project, provision a database, attach a domain, read logs." },
        { q: "Can an AI agent drive the cloud?", a: "Yes. Besides REST there is an MCP server: the agent gets 60 tools and works with projects, apps, databases and logs under exactly the permissions your account has. The AI agents (MCP) section above has setup, the full tool reference and worked recipes." },
        { q: "Are there rate limits?", a: "Metric and log reads are cached on the platform side, so polling every few seconds costs nothing. Mutating operations (deploys, resource creation) go through a task queue and return an operation id you can track." },
      ],
    },
  }[locale];

  return (
    <>
      <FaqJsonLd path="/developer" items={copy.faq} />
      <section className="mkt-hero-gradient">
        <div className="mkt-grid-bg">
          <div className="mx-auto max-w-7xl px-4 py-16 sm:px-6 lg:px-8 lg:py-20">
            <h1 className="max-w-3xl text-4xl font-bold tracking-tight text-white sm:text-5xl">{copy.title}</h1>
            <p className="mt-5 max-w-2xl text-lg text-white/70">{copy.subtitle}</p>
            <div className="mt-8 flex flex-wrap gap-3">
              <a
                href="#guides"
                className="rounded-md bg-blue-600 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
              >
                {copy.heroGuides}
              </a>
              <Link
                href={localeHref("/mcp", locale)}
                className="rounded-md border border-white/20 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-white/5"
              >
                {copy.heroMcp}
              </Link>
            </div>
          </div>
        </div>
      </section>

      <section id="guides" className="scroll-mt-20 bg-slate-50 py-16 lg:py-20">
        <div className="mx-auto max-w-7xl px-4 sm:px-6 lg:px-8">
          <h2 className="text-3xl font-bold tracking-tight text-slate-900">{copy.guidesTitle}</h2>
          <p className="mt-3 max-w-2xl text-sm text-slate-600">{copy.guidesSubtitle}</p>
          {copy.guidesNote && <p className="mt-2 text-xs text-slate-400">{copy.guidesNote}</p>}
          <div className="mt-10 space-y-12">
            {GUIDE_GROUPS.map((group) => (
              <div key={group.title.en}>
                <h3 className="text-sm font-semibold uppercase tracking-wide text-slate-500">
                  {group.title[locale]}
                </h3>
                <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                  {group.items.map((item) => {
                    const Icon = item.icon;
                    return (
                      <Link
                        key={item.slug}
                        href={localeHref(`/developer/${item.slug}`, locale)}
                        className={`group flex flex-col rounded-xl border bg-white p-5 transition-all hover:-translate-y-0.5 hover:shadow-md ${
                          item.featured ? "border-blue-200 ring-1 ring-blue-100" : "border-slate-200"
                        }`}
                      >
                        <span
                          className={`flex h-10 w-10 items-center justify-center rounded-lg ${
                            item.featured ? "bg-blue-600 text-white" : "bg-slate-100 text-slate-600"
                          }`}
                        >
                          <Icon className="h-5 w-5" />
                        </span>
                        <span className="mt-4 text-base font-semibold text-slate-900">{item.title[locale]}</span>
                        <span className="mt-2 flex-1 text-sm leading-relaxed text-slate-600">{item.desc[locale]}</span>
                        <span className="mt-4 inline-flex items-center gap-1.5 text-sm font-medium text-blue-600">
                          {copy.readLink}
                          <ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-0.5" />
                        </span>
                      </Link>
                    );
                  })}
                </div>
              </div>
            ))}
          </div>
        </div>
      </section>

      <section className="bg-white py-16 lg:py-20">
        <div className="mx-auto max-w-5xl px-4 sm:px-6 lg:px-8">
          <h2 className="text-3xl font-bold tracking-tight text-slate-900">{copy.buildTitle}</h2>
          <div className="mt-8 space-y-4">
            {copy.intro.map((par) => (
              <p key={par.slice(0, 24)} className="max-w-3xl text-base text-slate-600">
                {par}
              </p>
            ))}
          </div>
          <div className="mt-10 grid gap-6 md:grid-cols-3">
            <div className="min-w-0 rounded-xl border border-slate-200 bg-white p-6">
              <h3 className="text-lg font-semibold text-slate-900">{copy.apiTitle}</h3>
              <p className="mt-2 text-sm text-slate-600">{copy.apiDesc}</p>
              <pre className="mt-4 max-w-full overflow-x-auto rounded-lg bg-slate-900 p-4 text-xs text-slate-100">{API_EXAMPLE}</pre>
            </div>
            <Link
              href={localeHref("/developer/mcp-ai-agents", locale)}
              className="group rounded-xl border border-slate-200 bg-white p-6 transition-shadow hover:shadow-md"
            >
              <h3 className="text-lg font-semibold text-slate-900">{copy.mcpTitle}</h3>
              <p className="mt-2 text-sm text-slate-600">{copy.mcpDesc}</p>
              <span className="mt-4 inline-flex items-center gap-1.5 text-sm font-medium text-blue-600">
                {copy.mcpLink}
                <ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-0.5" />
              </span>
            </Link>
            <Link
              href={consoleHref("/projects")}
              className="rounded-xl border border-slate-200 bg-white p-6 transition-shadow hover:shadow-md"
            >
              <h3 className="text-lg font-semibold text-slate-900">{copy.consoleTitle}</h3>
              <p className="mt-2 text-sm text-slate-600">{copy.consoleDesc}</p>
            </Link>
          </div>
        </div>
      </section>
      <FaqList title={copy.faqTitle} items={copy.faq} />
      <CtaBand />
    </>
  );
}
