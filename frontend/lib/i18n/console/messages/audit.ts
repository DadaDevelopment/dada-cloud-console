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
  "audit.facet.users": { ru: "Пользователи", en: "Users" },
  "audit.facet.actions": { ru: "События", en: "Events" },
  "audit.facet.cohorts": { ru: "Клиенты", en: "Cohorts" },
  "audit.facet.hiddenCount": { ru: "скрыто {count}", en: "{count} hidden" },
  "audit.facet.searchPlaceholder": { ru: "Поиск…", en: "Search…" },
  "audit.facet.noMatches": { ru: "Ничего не найдено", en: "Nothing found" },
  "audit.facet.showAll": { ru: "Показать все", en: "Show all" },
  "audit.facet.hideAll": { ru: "Скрыть все", en: "Hide all" },
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

  "audit.coverage.title": { ru: "Покрытие аудита", en: "Audit coverage" },
  "audit.coverage.subtitle": {
    ru: "Операции за {days} дн., у которых нет ни одной строки аудита с их operation_id.",
    en: "Operations over the last {days} days with no audit row carrying their operation_id.",
  },
  "audit.coverage.clean": {
    ru: "За {days} дн. каждая операция оставила след в аудите.",
    en: "Every operation in the last {days} days left an audit trail.",
  },
  "audit.coverage.col.action": { ru: "Действие", en: "Action" },
  "audit.coverage.col.operations": { ru: "Операций", en: "Operations" },
  "audit.coverage.col.audited": { ru: "С аудитом", en: "Audited" },
  "audit.coverage.col.missing": { ru: "Без следа", en: "Missing" },
  "audit.coverage.totalMissing": { ru: "Без следа всего: {count}", en: "{count} missing in total" },

  "audit.total": { ru: "Всего событий: {count}", en: "{count} events total" },
  "audit.pager.prev": { ru: "Назад", en: "Previous" },
  "audit.pager.next": { ru: "Вперёд", en: "Next" },
};
