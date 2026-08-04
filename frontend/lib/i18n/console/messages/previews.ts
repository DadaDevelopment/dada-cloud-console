import type { Messages } from "./common";

/**
 * The embedded live-preview pane, plus the one PR label that outlived preview
 * environments: builds still carry a pr_number, and the deployments page shows it.
 * Preview (PR) environments themselves are no longer a product feature, so their
 * strings (teardown, TTL countdown, per-PR panel) are gone.
 */
export const previews: Messages = {
  "previews.pr": { ru: "PR #{n}", en: "PR #{n}" },

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

  "previewPane.card.open": { ru: "Открыть живой просмотр", en: "Open live preview" },
  "previewPane.card.hide": { ru: "Свернуть живой просмотр", en: "Hide live preview" },
  "previewPane.card.loading": { ru: "Читаем страницу…", en: "Reading the page…" },
  "previewPane.card.empty": {
    ru: "Страница не отдала описание. Откройте живой просмотр, чтобы увидеть приложение.",
    en: "The page returned no summary. Open the live preview to see the app.",
  },
  "previewPane.card.down": {
    ru: "Приложение не ответило страницей.",
    en: "The app did not answer with a page.",
  },
};
