import type { Messages } from "./common";

/** Project overview page — action dashboard with onboarding checklist and resource status. */
export const overview: Messages = {
  "overview.error.load": { ru: "Не удалось загрузить проект", en: "Failed to load project" },

  "overview.deployApp": { ru: "Развернуть приложение", en: "Deploy app" },

  "overview.chip.ready": { ru: "prod · работает", en: "prod · running" },
  "overview.chip.needsAction": { ru: "prod · требует внимания", en: "prod · needs attention" },
  "overview.chip.noApps": { ru: "prod · нет приложений", en: "prod · no apps" },
  "overview.chip.apps": { ru: "{count} прил.", en: "{count} apps" },
  "overview.chip.dbs": { ru: "{count} БД", en: "{count} DBs" },
  "overview.chip.domainVerified": { ru: "{count} домен", en: "{count} domain" },
  "overview.chip.domainDns": { ru: "домен: проверка DNS", en: "domain: DNS verification" },
  "overview.chip.domainNone": { ru: "домен не подключён", en: "no domain connected" },

  "overview.checklist.title": { ru: "Запуск проекта", en: "Project launch" },
  "overview.checklist.progress": { ru: "{done} из {total}", en: "{done} of {total}" },
  "overview.checklist.deploy": { ru: "Разверните приложение из GitHub", en: "Deploy an app from GitHub" },
  "overview.checklist.db": { ru: "Добавьте базу данных Postgres", en: "Add a Postgres database" },
  "overview.checklist.domain": { ru: "Подключите домен и HTTPS", en: "Connect a domain and HTTPS" },
  "overview.checklist.go": { ru: "Перейти →", en: "Go →" },

  "overview.onramp.git.title": {
    ru: "Разверните приложение из GitHub",
    en: "Deploy an app from GitHub",
  },
  "overview.onramp.git.hint": {
    ru: "Подключите репозиторий — платформа соберёт его и выкатит, а каждый следующий push будет деплоиться сам.",
    en: "Connect a repository — the platform builds and ships it, and every later push deploys itself.",
  },
  "overview.onramp.git.cta": { ru: "Подключить репозиторий", en: "Connect a repository" },
  "overview.onramp.demo.title": {
    ru: "Просто посмотреть, как это работает",
    en: "Just to see how it works",
  },
  "overview.onramp.demo.hint": {
    ru: "Демо-приложение из нашего репозитория, без GitHub. Удалится автоматически через 24 часа — если захотите оставить, нажмите «Оставить» в карточке приложения.",
    en: "A demo app from our repository, no GitHub needed. It is deleted automatically after 24 hours — press «Keep» on the app card to hold on to it.",
  },

  "overview.templates.title": { ru: "Готовый проект — GitHub не нужен", en: "A ready-made project — no GitHub needed" },
  "overview.templates.hint": {
    ru: "Открытый проект собирается из исходников и запускается в один клик, без подключения аккаунта.",
    en: "An open-source project builds from source and runs in one click, with no account connected.",
  },
  "overview.templates.heroTitle": {
    ru: "Начни здесь — задеплой первое приложение за 1 клик",
    en: "Start here — deploy your first app in one click",
  },
  "overview.templates.heroHint": {
    ru: "Настоящий открытый проект соберётся из исходников и запустится — без GitHub и без настройки. Тем же путём, которым потом поедет ваш код.",
    en: "A real open-source project builds from source and runs — no GitHub, no setup. The same path your own code will take.",
  },
  "overview.templates.deploying": { ru: "Разворачиваем…", en: "Deploying…" },
  "overview.templates.error": { ru: "Не удалось развернуть проект", en: "Failed to deploy the project" },
  "overview.templates.cta": { ru: "Развернуть", en: "Deploy" },

  "overview.templates.ask.title": {
    ru: "Или напишите, что нужно запустить",
    en: "Or say what you want to run",
  },
  "overview.templates.ask.hint": {
    ru: "Название проекта, ссылка на GitHub или просто «база данных» — найдём и соберём.",
    en: "A project name, a GitHub link, or just «database» — we find it and build it.",
  },
  "overview.templates.ask.placeholder": {
    ru: "n8n, база данных, https://github.com/owner/repo",
    en: "n8n, database, https://github.com/owner/repo",
  },
  "overview.templates.ask.searching": { ru: "Ищем на GitHub…", en: "Searching GitHub…" },
  "overview.templates.ask.empty": {
    ru: "Ничего не нашли. Попробуйте другое слово или вставьте ссылку на репозиторий.",
    en: "Nothing found. Try another word or paste a repository link.",
  },
  "overview.templates.ask.searchFailed": {
    ru: "Поиск по GitHub сейчас недоступен — показываем только наши проекты.",
    en: "GitHub search is unavailable right now — showing our own projects only.",
  },
  "overview.templates.ask.fromCatalog": { ru: "проверенный", en: "verified" },
  "overview.templates.ask.fromSearch": { ru: "с GitHub", en: "from GitHub" },
  "overview.templates.ask.fromLink": { ru: "по ссылке", en: "from your link" },
  "overview.templates.ask.managed": { ru: "управляемая", en: "managed" },
  "overview.templates.ask.archived": { ru: "архив", en: "archived" },
  "overview.templates.ask.openDatabases": { ru: "Создать", en: "Create" },

  "overview.boxes.col.name": { ru: "Имя", en: "Name" },
  "overview.boxes.col.status": { ru: "Статус", en: "Status" },
  "overview.boxes.col.profile": { ru: "Профиль", en: "Profile" },
  "overview.boxes.col.active": { ru: "Активность", en: "Last active" },
  "overview.boxes.readonly": {
    ru: "Боксами управляет агент через MCP — консоль их только показывает.",
    en: "Boxes are driven by agents over MCP; the console only shows them.",
  },

  "overview.health.title": { ru: "Приложения", en: "Apps" },
  "overview.health.unhealthyCount": { ru: "{count} с проблемой", en: "{count} unhealthy" },
  "overview.health.view": { ru: "Смотреть →", en: "View →" },
  "overview.health.unknown": { ru: "нет данных", en: "no data" },
  "overview.health.reason.crash": { ru: "падает в цикле (CrashLoopBackOff)", en: "crash looping (CrashLoopBackOff)" },
  "overview.health.reason.oom": { ru: "убито нехваткой памяти (OOMKilled)", en: "killed for running out of memory (OOMKilled)" },
  "overview.health.reason.image": { ru: "не может скачать образ (ImagePullBackOff)", en: "can't pull its image (ImagePullBackOff)" },
  "overview.health.reason.volume": { ru: "заканчивается место на диске", en: "running out of disk space" },
  "overview.health.reason.failed": { ru: "сборка не удалась", en: "build failed" },

  "overview.section.recentOps": { ru: "Последние операции", en: "Recent operations" },
  "overview.allOps": { ru: "Все операции →", en: "All operations →" },

  "overview.ops.empty.title": { ru: "Пока нет операций", en: "No operations yet" },
  "overview.ops.empty.description": {
    ru: "Деплои, создание баз и доменов появятся здесь после первого действия.",
    en: "Deploys, database and domain creations will appear here after your first action.",
  },
  "overview.ops.empty.action": { ru: "Развернуть приложение", en: "Deploy an app" },
};
