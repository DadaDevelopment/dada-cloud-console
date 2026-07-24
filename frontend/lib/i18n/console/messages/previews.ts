import type { Messages } from "./common";

/** Preview environments (PR-scoped ephemeral deploys) + the embedded live-preview pane. */
export const previews: Messages = {
  "previews.title": { ru: "Превью", en: "Previews" },
  "previews.subtitle": {
    ru: "Эфемерные окружения для открытых pull request'ов этого приложения.",
    en: "Ephemeral environments for this app's open pull requests.",
  },
  "previews.pr": { ru: "PR #{n}", en: "PR #{n}" },
  "previews.branch": { ru: "Ветка {branch}", en: "Branch {branch}" },
  "previews.expiresIn": { ru: "истекает {time}", en: "expires {time}" },
  "previews.expiresIn.expired": { ru: "истёк", en: "expired" },
  "previews.expiresIn.days": { ru: "через {n} д", en: "in {n}d" },
  "previews.expiresIn.hours": { ru: "через {n} ч", en: "in {n}h" },
  "previews.expiresIn.minutes": { ru: "через {n} мин", en: "in {n}m" },
  "previews.openUrl": { ru: "Открыть", en: "Open" },
  "previews.openPreview": { ru: "Живой просмотр", en: "Live preview" },
  "previews.delete": { ru: "Снести", en: "Tear down" },
  "previews.deleting": { ru: "Снос…", en: "Tearing down…" },
  "previews.delete.confirm.title": { ru: "Снести превью?", en: "Tear down preview?" },
  "previews.delete.confirm.body": {
    ru: "Окружение превью для PR #{n} и всё его содержимое будут удалены. Это необратимо.",
    en: "The preview environment for PR #{n} and everything in it will be deleted. This cannot be undone.",
  },
  "previews.error.delete": { ru: "Не удалось снести превью", en: "Failed to tear down the preview" },

  "previewPane.title": { ru: "Живой просмотр", en: "Live preview" },
  "previewPane.reload": { ru: "Обновить", en: "Reload" },
  "previewPane.openNewTab": { ru: "Открыть в новой вкладке", en: "Open in new tab" },
  "previewPane.viewport.mobile": { ru: "Моб.", en: "Mobile" },
  "previewPane.viewport.tablet": { ru: "Планшет", en: "Tablet" },
  "previewPane.viewport.full": { ru: "Во всю ширину", en: "Full" },
  "previewPane.checking": { ru: "Проверяем встраивание…", en: "Checking embeddability…" },
  "previewPane.blocked.title": { ru: "Приложение блокирует встраивание", en: "This app blocks embedding" },
  "previewPane.blocked.body": {
    ru: "Приложение отправляет заголовки, которые запрещают показывать его во встроенном окне. Откройте в новой вкладке.",
    en: "The app sends headers that forbid showing it in an embedded frame. Open it in a new tab instead.",
  },
  "previewPane.gatewayError.title": { ru: "Приложение не отвечает по HTTP", en: "The app is not responding over HTTP" },
  "previewPane.gatewayError.body": {
    ru: "Это бот без веб-сервера? Пересоздайте приложение в worker-режиме — ему не нужен домен.",
    en: "Is this a bot with no web server? Recreate the app as a background worker — it does not need a domain.",
  },
};
