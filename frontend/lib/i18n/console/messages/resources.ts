import type { Messages } from "./common";

/** Strings for first-class VM (compose) Resources: Ingress + ServiceDatabase. */
export const resources: Messages = {
  "resources.type.ingress": { ru: "Ingress", en: "Ingress" },
  "resources.type.database": { ru: "База данных", en: "Database" },
  "resources.type.app": { ru: "Приложение", en: "Application" },
  "resources.badge.managed": { ru: "Управляемый", en: "Managed" },
  "resources.card.routes": { ru: "{count} правил", en: "{count} routes" },

  // Ingress
  "resources.ingress.title": { ru: "Маршрутизация", en: "Routing" },
  "resources.ingress.subtitle": { ru: "Домен, TLS и правила проксирования", en: "Domain, TLS and proxy rules" },
  "resources.ingress.host": { ru: "Домен", en: "Domain" },
  "resources.ingress.aliases": { ru: "Алиасы", en: "Aliases" },
  "resources.ingress.tls": { ru: "TLS", en: "TLS" },
  "resources.ingress.tls.on": { ru: "Включён", en: "Enabled" },
  "resources.ingress.tls.off": { ru: "Выключен", en: "Disabled" },
  "resources.ingress.tls.min": { ru: "мин. {v}", en: "min {v}" },
  "resources.ingress.sslRedirect": { ru: "HTTP → HTTPS", en: "HTTP → HTTPS" },
  "resources.ingress.basicAuth": { ru: "Basic-авторизация", en: "Basic auth" },
  "resources.ingress.on": { ru: "Да", en: "On" },
  "resources.ingress.off": { ru: "Нет", en: "Off" },
  "resources.ingress.routes.title": { ru: "Правила маршрутизации", en: "Route rules" },
  "resources.ingress.routes.path": { ru: "Путь", en: "Path" },
  "resources.ingress.routes.target": { ru: "Назначение", en: "Target" },
  "resources.ingress.routes.empty": {
    ru: "Правила заданы в сгенерированной конфигурации",
    en: "Rules live in the generated config",
  },
  "resources.ingress.cert": { ru: "Сертификат", en: "Certificate" },

  // ServiceDatabase
  "resources.db.title": { ru: "Подключение", en: "Connection" },
  "resources.db.subtitle": { ru: "Реквизиты доступа к базе данных", en: "Database access details" },
  "resources.db.engine": { ru: "Движок", en: "Engine" },
  "resources.db.version": { ru: "Версия", en: "Version" },
  "resources.db.database": { ru: "База", en: "Database" },
  "resources.db.user": { ru: "Пользователь", en: "User" },
  "resources.db.host": { ru: "Хост", en: "Host" },
  "resources.db.port": { ru: "Порт", en: "Port" },
  "resources.db.dsn": { ru: "Строка подключения", en: "Connection string" },
  "resources.db.password.managed": { ru: "Пароль управляется платформой", en: "Password managed by platform" },
  "resources.db.storage.title": { ru: "Хранилище", en: "Storage" },
  "resources.db.storage.volume": { ru: "Том данных", en: "Data volume" },
  "resources.db.storage.external": { ru: "Внешний (сохраняется при пересоздании)", en: "External (survives recreation)" },
  "resources.db.copy": { ru: "Копировать", en: "Copy" },
  "resources.db.copied": { ru: "Скопировано", en: "Copied" },

  // Shared runtime section labels
  "resources.section.runtime": { ru: "Состояние контейнера", en: "Container state" },
  "resources.section.metrics": { ru: "Ресурсы", en: "Resources" },
  "resources.section.logs": { ru: "Логи", en: "Logs" },
};
