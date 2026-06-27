import type { Messages } from "./common";

/** AI Studio — MLflow model registry page. */
export const aiStudio: Messages = {
  "aiStudio.crumb.aiStudio": { ru: "AI Studio", en: "AI Studio" },
  "aiStudio.crumb.registry": { ru: "Реестр", en: "Registry" },

  "aiStudio.title": { ru: "MLflow registry", en: "MLflow registry" },
  "aiStudio.subtitle": {
    ru: "Просматривайте зарегистрированные модели, отфильтрованные по префиксу хранилища вашего проекта. Нажмите на строку, чтобы развернуть эту версию.",
    en: "Browse registered models filtered by your project’s storage prefix. Click any row to deploy that version.",
  },

  "aiStudio.project.label": { ru: "Проект:", en: "Project:" },

  "aiStudio.col.name": { ru: "Название", en: "Name" },
  "aiStudio.col.version": { ru: "Последняя версия", en: "Latest version" },
  "aiStudio.col.stage": { ru: "Стадия", en: "Stage" },
  "aiStudio.col.updated": { ru: "Обновлено", en: "Last updated" },
  "aiStudio.col.action": { ru: "Действие", en: "Action" },

  "aiStudio.search.placeholder": { ru: "Поиск моделей…", en: "Search models…" },

  "aiStudio.empty.title": {
    ru: "Нет зарегистрированных моделей с префиксом хранилища этого проекта",
    en: "No registered models match this project’s storage prefix",
  },
  "aiStudio.empty.hint": {
    ru: "Зарегистрируйте модель в MLflow, URI источника которой начинается с",
    en: "Register a model in MLflow whose source URI starts with",
  },

  "aiStudio.deploy": { ru: "Развернуть v{version} →", en: "Deploy v{version} →" },

  "aiStudio.error.projects": { ru: "Не удалось загрузить проекты", en: "Failed to load projects" },
  "aiStudio.error.mlflow": { ru: "MLflow registry недоступен", en: "MLflow registry unreachable" },
};
