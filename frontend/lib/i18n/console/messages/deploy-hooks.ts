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
  "deployHooks.name.label": { ru: "Название", en: "Name" },
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

  "deployHooks.snippet.actionTitle": { ru: "GitHub Actions", en: "GitHub Actions" },
  "deployHooks.snippet.actionHint": {
    ru: "Добавьте этот шаг в свой workflow:",
    en: "Add this step to your workflow:",
  },
  "deployHooks.snippet.curlTitle": { ru: "Или просто curl", en: "Or plain curl" },
  "deployHooks.snippet.curlHint": {
    ru: "Без зависимостей — вызовите это из любого CI:",
    en: "Zero dependencies — call this from any CI:",
  },
  "deployHooks.snippet.secretHint": {
    ru: "Сохраните токен как секрет репозитория с именем DADA_DEPLOY_TOKEN.",
    en: "Store the token as a repo secret named DADA_DEPLOY_TOKEN.",
  },

  "deployHooks.wizard.cta": {
    ru: "Уже собираете образы в своём CI? Деплой из готового образа →",
    en: "Already build images in your own CI? Deploy a prebuilt image →",
  },
};
