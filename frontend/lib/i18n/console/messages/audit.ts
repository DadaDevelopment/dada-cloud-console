import type { Messages } from "./common";

/** Admin — audit events dashboard. */
export const audit: Messages = {
  "audit.crumb.audit": { ru: "Аудит", en: "Audit" },

  "audit.title": { ru: "Журнал аудита", en: "Audit log" },
  "audit.subtitle": {
    ru: "Значимые действия во всех проектах платформы. Доступно только администраторам платформы.",
    en: "Significant actions across every project on the platform. Platform-admin only.",
  },

  "audit.col.time": { ru: "Время", en: "Time" },
  "audit.col.user": { ru: "Пользователь", en: "User" },
  "audit.col.action": { ru: "Действие", en: "Action" },
  "audit.col.resource": { ru: "Ресурс", en: "Resource" },
  "audit.col.project": { ru: "Проект", en: "Project" },

  "audit.filter.actionPlaceholder": { ru: "Действие (например CreateApp)", en: "Action (e.g. CreateApp)" },
  "audit.filter.userPlaceholder": { ru: "Email пользователя", en: "User email" },
  "audit.filter.kind.all": { ru: "Все когорты", en: "All cohorts" },
  "audit.filter.kind.customer": { ru: "Клиенты", en: "Customers" },
  "audit.filter.kind.internal": { ru: "Свои", en: "Internal" },
  "audit.filter.kind.synthetic": { ru: "Тестовые", en: "Test accounts" },
  "audit.filter.kind.platform": { ru: "Платформа", en: "Platform" },
  "audit.filter.apply": { ru: "Применить", en: "Apply" },
  "audit.filter.clear": { ru: "Сбросить", en: "Clear" },

  "audit.empty.title": { ru: "Событий не найдено", en: "No events found" },
  "audit.empty.body": {
    ru: "Создание приложений, проектов, баз данных и другие значимые действия появятся здесь.",
    en: "App/project/database creates and other significant actions will appear here.",
  },

  "audit.accessDenied": {
    ru: "Нет доступа. Журнал аудита доступен только администраторам платформы.",
    en: "No access. The audit log is available to platform admins only.",
  },

  "audit.error.load": { ru: "Не удалось загрузить журнал аудита", en: "Failed to load the audit log" },

  "audit.total": { ru: "Всего событий: {count}", en: "{count} events total" },
  "audit.pager.prev": { ru: "Назад", en: "Previous" },
  "audit.pager.next": { ru: "Вперёд", en: "Next" },
};
