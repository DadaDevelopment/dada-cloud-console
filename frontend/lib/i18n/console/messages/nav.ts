import type { Messages } from "./common";

/**
 * Project resource registry labels (lib/resources PROJECT_NAV). Rendered by the
 * sidebar, the command palette and page breadcrumbs via `nav.<item.key>`, so
 * the key suffixes must match the registry keys exactly.
 */
export const nav: Messages = {
  "nav.overview": { ru: "Обзор", en: "Overview" },
  "nav.apps": { ru: "Приложения", en: "Applications" },
  "nav.databases": { ru: "Базы данных", en: "Databases" },
  "nav.storage": { ru: "Объектное хранилище", en: "Object Storage" },
  "nav.boxes": { ru: "Боксы", en: "Boxes" },
  "nav.domains": { ru: "Домены", en: "Domains" },
  "nav.monitoring": { ru: "Мониторинг", en: "Monitoring" },
  "nav.ai": { ru: "AI API", en: "AI API" },
  "nav.models": { ru: "AI-модели", en: "AI Models" },
  "nav.app-servers": { ru: "Managed VM", en: "Managed VM" },
  "nav.git": { ru: "Сборки", en: "Builds" },
  "nav.operations": { ru: "Операции", en: "Operations" },
  "nav.redis": { ru: "Redis", en: "Redis" },
  "nav.queues": { ru: "Очереди сообщений", en: "Message Queues" },
  "nav.members": { ru: "Участники", en: "Members" },
  "nav.approvals": { ru: "Согласования", en: "Approvals" },
  "nav.billing": { ru: "Биллинг", en: "Billing" },
  "env.runtime.vm": { ru: "VM", en: "VM" },
  "env.runtime.cloud": { ru: "Облако", en: "Cloud" },
};
