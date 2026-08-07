import type { ConsoleLocale } from "../locale";

export type Messages = Record<string, Record<ConsoleLocale, string>>;

/**
 * Strings shared across many console screens (buttons, breadcrumbs, generic
 * status words). Page fragments should reuse these `common.*` keys instead of
 * redefining "Cancel"/"Create" so the wording stays consistent everywhere.
 */
export const common: Messages = {
  "common.cancel": { ru: "Отмена", en: "Cancel" },
  "common.create": { ru: "Создать", en: "Create" },
  "common.creating": { ru: "Создание…", en: "Creating…" },
  "common.delete": { ru: "Удалить", en: "Delete" },
  "common.deleting": { ru: "Удаление…", en: "Deleting…" },
  "common.remove": { ru: "Удалить", en: "Remove" },
  "common.removing": { ru: "Удаление…", en: "Removing…" },
  "common.add": { ru: "Добавить", en: "Add" },
  "common.save": { ru: "Сохранить", en: "Save" },
  "common.apply": { ru: "Применить", en: "Apply" },
  "common.refresh": { ru: "Обновить", en: "Refresh" },
  "common.copy": { ru: "Копировать", en: "Copy" },
  "common.copied": { ru: "Скопировано", en: "Copied" },
  "common.optional": { ru: "(необязательно)", en: "(optional)" },
  "common.learnMore": { ru: "Подробнее", en: "Learn more" },

  "common.crumb.projects": { ru: "Проекты", en: "Projects" },
  "common.crumb.overview": { ru: "Обзор", en: "Overview" },
  "common.crumb.console": { ru: "Консоль", en: "Console" },

  "common.status.name": { ru: "Название", en: "Name" },
  "common.status.status": { ru: "Статус", en: "Status" },
  "common.status.action": { ru: "Действие", en: "Action" },
  "common.status.synced": { ru: "Синхронизировано {ago}", en: "Synced {ago}" },

  "common.commit.branchLatest": { ru: "последний коммит ветки {branch}", en: "latest commit on branch {branch}" },
  "common.commit.archive": { ru: "загруженный архив", en: "uploaded archive" },

  "common.time.agoSeconds": { ru: "{n} сек назад", en: "{n}s ago" },
  "common.time.agoMinutes": { ru: "{n} мин назад", en: "{n}m ago" },
  "common.time.agoHours": { ru: "{n} ч назад", en: "{n}h ago" },
  "common.time.agoDays": { ru: "{n} дн назад", en: "{n}d ago" },
};
