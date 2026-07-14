import type { Messages } from "./common";

/** MoveAppModal — move a stateless app to another project (ADR-014 Phase 1). */
export const moveApp: Messages = {
  "moveApp.button": { ru: "Перенести в другой проект", en: "Move to another project" },
  "moveApp.title": { ru: "Перенести приложение", en: "Move app" },

  "moveApp.target.label": { ru: "Проект назначения", en: "Target project" },
  "moveApp.target.placeholder": { ru: "Выберите проект…", en: "Select a project…" },
  "moveApp.target.errorLoad": { ru: "Не удалось загрузить список проектов", en: "Failed to load projects" },
  "moveApp.target.empty": { ru: "Нет других проектов для переноса", en: "No other projects available" },

  "moveApp.loading": { ru: "Проверяем перенос…", en: "Checking move…" },
  "moveApp.error.load": { ru: "Не удалось проверить перенос", en: "Failed to check the move" },
  "moveApp.error.submit": { ru: "Не удалось перенести приложение", en: "Failed to move the app" },

  "moveApp.summary.title": { ru: "Будет перенесено", en: "Will move" },
  "moveApp.summary.namespace": { ru: "Целевой namespace: {namespace}", en: "Target namespace: {namespace}" },
  "moveApp.summary.empty": { ru: "Ничего кроме самого приложения", en: "Nothing besides the app itself" },

  "moveApp.banner.blocked.title": { ru: "Перенос заблокирован", en: "Move blocked" },
  "moveApp.banner.nameCollision": {
    ru: "Приложение с таким именем уже существует в целевом проекте.",
    en: "An app with this name already exists in the target project.",
  },

  "moveApp.downtime.hint": {
    ru: "Домены приложения кратко вернут 404, пока сертификат перевыпускается в новом namespace.",
    en: "The app's domains will briefly 404 while the certificate re-issues in the new namespace.",
  },

  "moveApp.submit": { ru: "Перенести приложение", en: "Move app" },
  "moveApp.submitting": { ru: "Перенос…", en: "Moving…" },
};
