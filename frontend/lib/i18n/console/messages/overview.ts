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

  "overview.card.app.title": { ru: "Развернуть приложение", en: "Deploy an app" },
  "overview.card.app.hint": {
    ru: "Подключите репозиторий GitHub — сборка и деплой без YAML.",
    en: "Connect a GitHub repository — build and deploy without YAML.",
  },
  "overview.card.app.cta": { ru: "Подключить GitHub", en: "Connect GitHub" },

  "overview.card.db.title": { ru: "Добавить базу данных", en: "Add a database" },
  "overview.card.db.hint": {
    ru: "Создайте Postgres и подключите строку соединения к приложению.",
    en: "Create Postgres and wire the connection string to your app.",
  },
  "overview.card.db.cta": { ru: "Создать Postgres", en: "Create Postgres" },

  "overview.card.domain.title": { ru: "Добавить домен", en: "Add a domain" },
  "overview.card.domain.hint": {
    ru: "Свой домен с автоматическим выпуском HTTPS-сертификата.",
    en: "Your own domain with automatic HTTPS certificate issuance.",
  },
  "overview.card.domain.cta": { ru: "Добавить домен", en: "Add domain" },

  "overview.section.more": { ru: "Ещё в проекте", en: "More in project" },

  "overview.secondary.monitoring.hint": { ru: "Логи и метрики", en: "Logs and metrics" },
  "overview.secondary.operations.hint": { ru: "История деплоев", en: "Deploy history" },
  "overview.secondary.storage.hint": { ru: "S3-совместимые бакеты", en: "S3-compatible buckets" },
  "overview.secondary.models.hint": { ru: "Инференс KServe", en: "KServe inference" },
  "overview.secondary.appServers.hint": { ru: "VM-хосты", en: "VM hosts" },
  "overview.secondary.git.hint": { ru: "Репозитории и билды", en: "Repositories and builds" },

  "overview.section.recentOps": { ru: "Последние операции", en: "Recent operations" },
  "overview.allOps": { ru: "Все операции →", en: "All operations →" },

  "overview.ops.empty.title": { ru: "Пока нет операций", en: "No operations yet" },
  "overview.ops.empty.description": {
    ru: "Деплои, создание баз и доменов появятся здесь после первого действия.",
    en: "Deploys, database and domain creations will appear here after your first action.",
  },
  "overview.ops.empty.action": { ru: "Развернуть приложение", en: "Deploy an app" },
};
