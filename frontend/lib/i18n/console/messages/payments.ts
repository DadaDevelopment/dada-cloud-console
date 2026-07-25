import type { Messages } from "./common";

/** App settings "Payments" tab — YooKassa OAuth connect (apps.payments.*). */
export const payments: Messages = {
  "apps.payments.title": { ru: "Оплата", en: "Payments" },
  "apps.payments.subtitle": {
    ru: "Подключите свой магазин ЮKassa, чтобы приложение могло принимать платежи.",
    en: "Connect your YooKassa shop so this app can accept payments.",
  },

  "apps.payments.connect": { ru: "Подключить оплату ЮKassa", en: "Connect YooKassa payments" },
  "apps.payments.connecting": { ru: "Подключение…", en: "Connecting…" },
  "apps.payments.openAuthorize": { ru: "Открыть страницу авторизации ЮKassa →", en: "Open YooKassa authorization →" },
  "apps.payments.notConfigured": {
    ru: "Оплата пока не настроена на платформе. Обратитесь в поддержку.",
    en: "Payments aren't configured on this platform yet. Contact support.",
  },
  "apps.payments.error.connect": { ru: "Не удалось начать подключение", en: "Failed to start connecting" },
  "apps.payments.error.load": { ru: "Не удалось загрузить статус оплаты", en: "Failed to load payments status" },

  "apps.payments.connected.success": { ru: "Оплата подключена", en: "Payments connected" },
  "apps.payments.error.query": { ru: "Не удалось подключить оплату", en: "Failed to connect payments" },

  "apps.payments.status.label": { ru: "Статус", en: "Status" },
  "apps.payments.status.active": { ru: "Активно", en: "Active" },
  "apps.payments.status.error": { ru: "Ошибка", en: "Error" },
  "apps.payments.status.disconnected": { ru: "Отключено", en: "Disconnected" },

  "apps.payments.accountId": { ru: "ID аккаунта", en: "Account ID" },
  "apps.payments.expiresAt": { ru: "Токен действителен до", en: "Token valid until" },
  "apps.payments.connectedAt": { ru: "Подключено", en: "Connected" },

  "apps.payments.webhooks.title": { ru: "Вебхуки", en: "Webhooks" },
  "apps.payments.webhooks.none": { ru: "Вебхуки не зарегистрированы", en: "No webhooks registered" },
  "apps.payments.webhooks.noHostnameWarning": {
    ru: "У приложения нет публичного адреса, поэтому вебхуки ЮKassa не зарегистрированы. Уведомления об оплате нужно будет получать другим способом.",
    en: "This app has no public hostname, so YooKassa webhooks were not registered. You will need another way to receive payment notifications.",
  },

  "apps.payments.envKeys.title": { ru: "Переменные окружения", en: "Environment variables" },
  "apps.payments.envKeys.hint": {
    ru: "Переменные применятся при следующем деплое приложения.",
    en: "Variables take effect on the app's next deploy.",
  },

  "apps.payments.snippet.title": { ru: "Пример кода", en: "Code example" },
  "apps.payments.snippet.python": { ru: "Python", en: "Python" },
  "apps.payments.snippet.node": { ru: "Node.js", en: "Node.js" },

  "apps.payments.disconnect": { ru: "Отключить оплату", en: "Disconnect payments" },
  "apps.payments.disconnecting": { ru: "Отключение…", en: "Disconnecting…" },
  "apps.payments.disconnect.confirm": {
    ru: "Отключить оплату ЮKassa для этого приложения? Переменные окружения будут удалены, вебхуки отменены.",
    en: "Disconnect YooKassa payments for this app? Environment variables will be removed and webhooks cancelled.",
  },
  "apps.payments.disconnect.error": { ru: "Не удалось отключить оплату", en: "Failed to disconnect payments" },
  "apps.payments.disconnect.success": { ru: "Оплата отключена", en: "Payments disconnected" },
};
