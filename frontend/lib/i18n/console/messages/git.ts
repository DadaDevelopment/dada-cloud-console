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
  "git.import.step.deploy": { ru: "Деплой", en: "Deploy" },

  "git.import.subtitle": {
    ru: "От репозитория до задеплоенного приложения — на одном экране.",
    en: "From repository to a deployed app — all on one screen.",
  },
  "git.import.section.source": { ru: "Репозиторий", en: "Repository" },
  "git.import.section.configure": { ru: "Настройка", en: "Configure" },
  "git.import.section.deploy": { ru: "Деплой", en: "Deploy" },
  "git.import.changeRepo": { ru: "Изменить", en: "Change" },
  "git.import.selectedButton": { ru: "Выбрано", en: "Selected" },

  "git.import.deploy.button": { ru: "Задеплоить", en: "Deploy" },
  "git.import.deploy.starting": { ru: "Запуск сборки…", en: "Starting build…" },
  "git.import.deploy.triggerFailed": { ru: "Не удалось запустить сборку", en: "Failed to start the build" },
  "git.import.deploy.success": {
    ru: "Готово — приложение собрано и задеплоено.",
    en: "Done — the app is built and deployed.",
  },
  "git.import.deploy.retry": { ru: "Повторить", en: "Retry" },
  "git.import.deploy.viewDeployments": { ru: "Все деплои", en: "View deployments" },
  "git.import.deploy.openApp": { ru: "Открыть приложение", en: "Open app" },

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
  "git.import.connectAnotherGitHub": { ru: "Подключить ещё один аккаунт GitHub", en: "Connect another GitHub account" },
  "git.import.openGithub": {
    ru: "Ничего не произошло? Открыть GitHub →",
    en: "Nothing happened? Open GitHub →",
  },
  "git.import.orTemplate": {
    ru: "или без GitHub: загрузите архив или разверните шаблон",
    en: "or without GitHub: upload an archive or deploy a template",
  },

  "git.import.select": { ru: "Выбрать →", en: "Select →" },
  "git.import.backToAccounts": { ru: "← Назад к аккаунтам", en: "← Back to accounts" },
  "git.import.backToRepos": { ru: "← Назад к репозиториям", en: "← Back to repositories" },

  "git.import.accountOrg.label": { ru: "GitHub аккаунт / организация", en: "GitHub account / organization" },
  "git.import.searchPlaceholder": { ru: "Поиск репозитория…", en: "Search repository…" },
  "git.import.noMatch": { ru: "Нет репозиториев по запросу.", en: "No repositories match your search." },
  "git.import.importButton": { ru: "Импорт", en: "Import" },

  "git.import.reposUnavailable": {
    ru: "Сборка временно недоступна в этой среде, поэтому список репозиториев не загрузился. Мы уже разбираемся — попробуйте чуть позже.",
    en: "Builds are temporarily unavailable in this environment, so repositories couldn't be loaded. We're on it — please try again shortly.",
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
  "git.import.detectFailed": { ru: "Не удалось определить фреймворк", en: "Framework detection failed" },
  "git.import.detectRetry": { ru: "Повторить", en: "Retry" },

  "git.import.appName.label": { ru: "Имя приложения", en: "Application name" },
  "git.import.appName.hint": { ru: "(строчные буквы, цифры и дефисы)", en: "(lowercase letters, digits and hyphens)" },
  "git.import.appName.placeholder": { ru: "my-service", en: "my-service" },
  "git.import.appName.help": {
    ru: "Приложение создаётся автоматически при первой успешной сборке — плейсхолдер не деплоится.",
    en: "The app is created automatically by the first successful build — no placeholder is deployed.",
  },

  "git.import.port.label": { ru: "Порт", en: "Port" },
  "git.import.worker.label": {
    ru: "Фоновый процесс без HTTP-порта (бот, воркер)",
    en: "Background process with no HTTP port (bot, worker)",
  },
  "git.import.worker.hint": {
    ru: "Публичный адрес не выдаётся, порт не слушается, и консоль не будет требовать от приложения ответа по HTTP.",
    en: "No public address is issued, no port is exposed, and the console will not expect the app to answer over HTTP.",
  },
  "git.import.profile.label": { ru: "Профиль", en: "Profile" },
  "git.import.branch.label": { ru: "Ветка для продакшна", en: "Production branch" },
  "git.import.rootDir.label": { ru: "Корневая директория", en: "Root directory" },
  "git.import.frameworkOverride.label": { ru: "Переопределить фреймворк", en: "Framework override" },
  "git.import.framework.label": { ru: "Фреймворк", en: "Framework" },
  "git.import.framework.auto": { ru: "Автоопределение", en: "Auto-detect" },
  "git.import.framework.hint": {
    ru: "Детект подставляет семейство сборки, package manager, команды и порт по умолчанию — ниже можно изменить вручную.",
    en: "Detection fills in the build family, package manager, commands, and port by default — you can still edit them below.",
  },

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
    ru: "Подключение репозиториев временно недоступно в этой среде. Мы уже чиним — попробуйте чуть позже.",
    en: "Connecting repositories is temporarily unavailable in this environment. We're fixing it — please try again shortly.",
  },
  "git.import.error.startInstall": { ru: "Не удалось начать установку", en: "Failed to start install" },
  "git.import.error.loadRepos": { ru: "Не удалось загрузить репозитории", en: "Failed to load repositories" },
  "git.import.error.connect": { ru: "Не удалось подключить репозиторий", en: "Failed to connect repository" },
  "git.import.error.githubAccessRequired": {
    ru: "Этот репозиторий приватный или недоступен нам - нет ни установки GitHub App, ни токена для доступа к нему.",
    en: "This repository is private or unavailable to us - there is no GitHub App installation and no token that grants access to it.",
  },
  "git.import.error.githubAccessRequired.cta": {
    ru: "Подключить доступ к GitHub",
    en: "Connect GitHub access",
  },

  "git.import.byUrl.open": { ru: "Подключить по URL", en: "Connect by URL" },
  "git.import.byUrl.title": { ru: "Подключить репозиторий по URL", en: "Connect a repository by URL" },
  "git.import.byUrl.subtitle": {
    ru: "Для GitLab (включая self-hosted), Gitea, Bitbucket и любого другого https-репозитория.",
    en: "For GitLab (including self-hosted), Gitea, Bitbucket, and any other https repository.",
  },
  "git.import.byUrl.cloneUrl.label": { ru: "Clone URL", en: "Clone URL" },
  "git.import.byUrl.cloneUrl.placeholder": { ru: "https://gitlab.com/owner/repo.git", en: "https://gitlab.com/owner/repo.git" },
  "git.import.byUrl.cloneUrl.hint": {
    ru: "Только https. github.com определится как GitHub, любой другой хост - как GitLab, в том числе self-hosted.",
    en: "https only. github.com is detected as GitHub, any other host as GitLab, including self-hosted.",
  },
  "git.import.byUrl.token.label": { ru: "Токен доступа", en: "Access token" },
  "git.import.byUrl.token.hint": {
    ru: "Нужен только для приватного репозитория. Хранится в зашифрованном виде.",
    en: "Only needed for a private repository. Stored encrypted.",
  },
  "git.import.byUrl.appName.label": { ru: "Имя приложения", en: "Application name" },
  "git.import.byUrl.branch.label": { ru: "Ветка для продакшна", en: "Production branch" },
  "git.import.byUrl.rootDir.label": { ru: "Корневая директория", en: "Root directory" },
  "git.import.byUrl.port.label": { ru: "Порт", en: "Port" },
  "git.import.byUrl.port.hint": {
    ru: "Оставьте пустым - определим по репозиторию (Dockerfile или фреймворк), не выйдет - подставим 8080.",
    en: "Leave blank to detect it from the repository (Dockerfile or framework); falls back to 8080 if detection fails.",
  },
  "git.import.byUrl.worker.label": {
    ru: "Фоновый процесс без HTTP-порта (бот, воркер)",
    en: "Background process with no HTTP port (bot, worker)",
  },
  "git.import.byUrl.submit": { ru: "Подключить", en: "Connect" },
  "git.import.byUrl.submitting": { ru: "Подключение…", en: "Connecting…" },
  "git.import.byUrl.cancel": { ru: "Отмена", en: "Cancel" },
  "git.import.byUrl.error.appNameTaken": {
    ru: "Приложение с таким именем уже есть в этом окружении.",
    en: "An app with this name already exists in this environment.",
  },
  "git.import.byUrl.error.repoAlreadyConnected": {
    ru: "Этот репозиторий уже подключен к этому приложению. Повторно подключать не нужно - откройте приложение.",
    en: "This repository is already connected to this app. No need to reconnect it - open the app instead.",
  },
  "git.import.byUrl.error.repoLinkedToOtherProject": {
    ru: "Этот репозиторий уже подключен в другом вашем проекте. Откройте его там, а не подключайте повторно - иначе получится второе, конкурирующее приложение.",
    en: "This repository is already connected in another one of your projects. Open it there instead of connecting it again - otherwise you get a second, competing app.",
  },
  "git.import.byUrl.alreadyConnected.rebuild": { ru: "Пересобрать и задеплоить", en: "Rebuild and deploy" },
  "git.import.byUrl.alreadyConnected.open": { ru: "Открыть приложение", en: "Open the app" },
  "git.import.byUrl.error.empty": { ru: "Введите clone URL репозитория", en: "Enter the repository's clone URL" },
  "git.import.byUrl.error.sshNotSupported": {
    ru: "ssh-адреса не поддерживаются - используйте https-адрес с токеном.",
    en: "ssh URLs are not supported - use an https URL with a token.",
  },
  "git.import.byUrl.error.httpNotSupported": {
    ru: "Только https - токен передаётся лишь по https.",
    en: "https only - the token is only sent over https.",
  },
  "git.import.byUrl.error.invalidUrl": { ru: "Не похоже на URL репозитория", en: "Doesn't look like a repository URL" },
  "git.import.byUrl.error.incompletePath": {
    ru: "В URL не хватает пути до репозитория, например owner/repo",
    en: "The URL is missing the repository path, e.g. owner/repo",
  },
};
