import type { Messages } from "./common";

/** DeployHooksCard (app detail page) + the git-import wizard's "deploy a prebuilt image" callout. */
export const deployHooks: Messages = {
  "deployHooks.title": { ru: "Деплой из CI", en: "Deploy from CI" },
  "deployHooks.subtitle": {
    ru: "Токен для пуша нового образа из вашего CI (GitHub Actions и т.п.) без доступа к консоли.",
    en: "A token your own CI (GitHub Actions, etc.) can use to push a new image without console access.",
  },
  "deployHooks.create": { ru: "Создать токен", en: "Create token" },
  "deployHooks.creating": { ru: "Создание…", en: "Creating…" },
  "deployHooks.name.placeholder": { ru: "например, github-actions", en: "e.g. github-actions" },

  "deployHooks.empty": {
    ru: "Токенов пока нет. Создайте один, чтобы деплоить из своего CI.",
    en: "No tokens yet. Create one to deploy from your own CI.",
  },

  "deployHooks.error.load": { ru: "Не удалось загрузить токены", en: "Failed to load tokens" },
  "deployHooks.error.create": { ru: "Не удалось создать токен", en: "Failed to create token" },
  "deployHooks.error.revoke": { ru: "Не удалось отозвать токен", en: "Failed to revoke token" },

  "deployHooks.unnamed": { ru: "Без названия", en: "Unnamed" },
  "deployHooks.createdAt": { ru: "создан {ago}", en: "created {ago}" },
  "deployHooks.lastUsed": { ru: "использован {ago}", en: "last used {ago}" },
  "deployHooks.neverUsed": { ru: "ещё не использован", en: "never used" },
  "deployHooks.revoked": { ru: "Отозван", en: "Revoked" },
  "deployHooks.revoke": { ru: "Отозвать", en: "Revoke" },
  "deployHooks.revoke.confirm": {
    ru: "Отозвать этот токен деплоя? CI, который его использует, перестанет мочь деплоить.",
    en: "Revoke this deploy token? Any CI using it will no longer be able to deploy.",
  },

  "deployHooks.created.title": { ru: "Токен создан", en: "Token created" },
  "deployHooks.created.warning": {
    ru: "Скопируйте токен сейчас — он показывается один раз и больше нигде не будет виден.",
    en: "Copy this token now — it is shown once and cannot be retrieved again.",
  },
  "deployHooks.created.done": { ru: "Готово, я сохранил токен", en: "Done, I saved the token" },

  "deployHooks.snippet.curlTitle": { ru: "Через curl (работает сразу)", en: "With curl (works today)" },
  "deployHooks.snippet.curlHint": {
    ru: "Без зависимостей — вставьте этот шаг в свой workflow:",
    en: "Zero dependencies — add this step to your workflow:",
  },
  "deployHooks.snippet.actionTitle": { ru: "Или короче — GitHub Action", en: "Or shorter — GitHub Action" },
  "deployHooks.snippet.actionHint": {
    ru: "То же самое одним шагом:",
    en: "The same, as a single step:",
  },
  "deployHooks.snippet.secretHint": {
    ru: "Сохраните токен как секрет репозитория с именем DADA_DEPLOY_TOKEN.",
    en: "Store the token as a repo secret named DADA_DEPLOY_TOKEN.",
  },

  "deployHooks.wizard.cta": {
    ru: "Уже собираете образы в своём CI? Деплой из готового образа →",
    en: "Already build images in your own CI? Deploy a prebuilt image →",
  },

  "deployHooks.wizard.gha.title": { ru: "Обнаружен GitHub Actions", en: "GitHub Actions detected" },
  "deployHooks.wizard.gha.body": {
    ru: "Найдено {n} workflow. Можно деплоить прямо из вашего CI — мы не будем пересобирать.",
    en: "Found {n} workflow(s). You can deploy straight from your CI instead of us rebuilding.",
  },
  "deployHooks.wizard.gha.showGuide": { ru: "Показать гайд", en: "Show guide" },
  "deployHooks.wizard.gha.hideGuide": { ru: "Скрыть гайд", en: "Hide guide" },
  "deployHooks.wizard.gha.step1": {
    ru: "Создайте приложение кнопкой ниже",
    en: "Create the app using the button below",
  },
  "deployHooks.wizard.gha.step2": {
    ru: "В приложении → Deploy from CI создайте токен",
    en: "In the app → Deploy from CI, create a token",
  },
  "deployHooks.wizard.gha.step3": {
    ru: "Добавьте его в GitHub → Settings → Secrets как DADA_DEPLOY_TOKEN",
    en: "Add it to GitHub → Settings → Secrets as DADA_DEPLOY_TOKEN",
  },
  "deployHooks.wizard.gha.step4": {
    ru: "Вставьте этот шаг в свой workflow",
    en: "Paste this step into your workflow",
  },
  "deployHooks.wizard.gha.actionAlt": { ru: "или короче — GitHub Action", en: "or shorter — GitHub Action" },
  "deployHooks.wizard.gha.agentCta": { ru: "Доверить агенту", en: "Let an agent do it" },
  "deployHooks.wizard.gha.agentSoon": {
    ru: "скоро — агент откроет PR с готовым шагом",
    en: "coming soon — an agent will open a PR with the step",
  },
};
