import type { Messages } from "./common";

/** Git / Builds page — repositories that build and deploy apps from source. */
export const git: Messages = {
  "git.title": { ru: "Сборки", en: "Builds" },
  "git.subtitle": {
    ru: "Репозитории, которые собирают и деплоят приложения из исходников при каждом push",
    en: "Repositories that build & deploy apps from source on every push",
  },

  "git.importRepo": { ru: "Импорт репозитория", en: "Import repository" },

  "git.empty.title": {
    ru: "Нет подключённых репозиториев в {env}",
    en: "No repositories connected in {env}",
  },
  "git.empty.connect": {
    ru: "Подключить первый репозиторий →",
    en: "Connect your first repository →",
  },
  "git.empty.hint": {
    ru: "Подключите репозиторий — приложение создастся автоматически после первой успешной сборки.",
    en: "Connect a repo and the app is created automatically by its first successful build.",
  },

  "git.repo.app": {
    ru: "app {name}",
    en: "app {name}",
  },
  "git.repo.branch": {
    ru: "ветка {name}",
    en: "branch {name}",
  },
  "git.repo.root": {
    ru: "root {path}",
    en: "root {path}",
  },
  "git.repo.autoDeploy": { ru: "auto-deploy", en: "auto-deploy" },
  "git.repo.viewBuilds": { ru: "Смотреть сборки →", en: "View builds →" },
  "git.repo.connected": { ru: "Подключено {ago}", en: "Connected {ago}" },

  "git.error.load": {
    ru: "Не удалось загрузить репозитории",
    en: "Failed to load repos",
  },

  "git.import.title": { ru: "Импорт репозитория", en: "Import repository" },

  "git.import.step.account": { ru: "Аккаунт", en: "Account" },
  "git.import.step.repository": { ru: "Репозиторий", en: "Repository" },
  "git.import.step.configure": { ru: "Настройка", en: "Configure" },

  "git.import.noPermission": {
    ru: "У вас нет прав на подключение репозиториев.",
    en: "You don't have permission to connect repositories.",
  },

  "git.import.noAccounts.title": { ru: "Нет подключённых git-аккаунтов", en: "No git accounts connected yet" },
  "git.import.noAccounts.hint": {
    ru: "Авторизуйте провайдера, чтобы открыть доступ к репозиториям.",
    en: "Authorize a provider to grant access to your repositories.",
  },
  "git.import.connectGitHub": { ru: "Подключить GitHub", en: "Connect GitHub" },
  "git.import.connectGitLab": { ru: "Подключить GitLab", en: "Connect GitLab" },
  "git.import.connectAnotherGitHub": { ru: "+ Подключить ещё один аккаунт GitHub", en: "+ Connect another GitHub account" },
  "git.import.connectAnotherGitLab": { ru: "+ GitLab", en: "+ GitLab" },

  "git.import.select": { ru: "Выбрать →", en: "Select →" },
  "git.import.backToAccounts": { ru: "← Назад к аккаунтам", en: "← Back to accounts" },
  "git.import.backToRepos": { ru: "← Назад к репозиториям", en: "← Back to repositories" },

  "git.import.accountOrg.label": { ru: "GitHub аккаунт / организация", en: "GitHub account / organization" },

  "git.import.reposUnavailable": {
    ru: "Подсистема сборки не настроена в этой среде — репозитории недоступны. Попробуйте снова после деплоя build-agent.",
    en: "The build subsystem is not configured in this environment yet, so repositories can't be listed. Try again once the build-agent is deployed.",
  },
  "git.import.noRepos": {
    ru: "В этом аккаунте нет доступных репозиториев.",
    en: "No repositories available on this account.",
  },
  "git.import.importArrow": { ru: "Импорт →", en: "Import →" },
  "git.import.private": { ru: "private", en: "private" },

  "git.import.detectedFramework": { ru: "Обнаруженный фреймворк", en: "Detected framework" },
  "git.import.detecting": { ru: "Определение…", en: "Detecting…" },
  "git.import.unknownFramework": { ru: "Неизвестно — выберите ниже", en: "Unknown — pick one below" },

  "git.import.appName.label": { ru: "Имя приложения", en: "Application name" },
  "git.import.appName.hint": { ru: "(имя ресурса Kubernetes)", en: "(Kubernetes resource name)" },
  "git.import.appName.placeholder": { ru: "my-service", en: "my-service" },
  "git.import.appName.help": {
    ru: "Приложение создаётся автоматически при первой успешной сборке — плейсхолдер не деплоится.",
    en: "The app is created automatically by the first successful build — no placeholder is deployed.",
  },

  "git.import.port.label": { ru: "Порт", en: "Port" },
  "git.import.profile.label": { ru: "Профиль", en: "Profile" },
  "git.import.branch.label": { ru: "Ветка для продакшна", en: "Production branch" },
  "git.import.rootDir.label": { ru: "Корневая директория", en: "Root directory" },
  "git.import.frameworkOverride.label": { ru: "Переопределить фреймворк", en: "Framework override" },

  "git.import.autoDeploy.label": { ru: "Авто-деплой", en: "Auto-deploy" },
  "git.import.autoDeploy.hint": {
    ru: "Собирать и деплоить автоматически при каждом push в продакшн-ветку",
    en: "Build & deploy automatically on every push to the production branch",
  },

  "git.import.connect": { ru: "Подключить репозиторий", en: "Connect repository" },
  "git.import.connecting": { ru: "Подключение…", en: "Connecting…" },

  "git.import.alreadyConnected": {
    ru: "Этот репозиторий уже подключён к приложению в данной среде.",
    en: "This repository is already connected to an app in this environment.",
  },
  "git.import.error.loadInstalls": {
    ru: "Не удалось загрузить установки",
    en: "Failed to load installations",
  },
  "git.import.unavailable": {
    ru: "Git-интеграция пока недоступна — подсистема сборки (build-agent) не задеплоена в этой среде.",
    en: "Git integration isn't available yet — the build subsystem (build-agent) is not deployed in this environment.",
  },
  "git.import.error.startInstall": { ru: "Не удалось начать установку", en: "Failed to start install" },
  "git.import.error.loadRepos": { ru: "Не удалось загрузить репозитории", en: "Failed to load repositories" },
  "git.import.error.connect": { ru: "Не удалось подключить репозиторий", en: "Failed to connect repository" },
};
