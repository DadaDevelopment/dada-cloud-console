import type { Messages } from "./common";

/** Persistent chrome: top bar, sidebar nav, switchers, command palette, account menu. */
export const shell: Messages = {
  "shell.search": { ru: "Поиск…", en: "Search…" },
  "shell.openSearch": { ru: "Открыть поиск", en: "Open search" },

  "shell.palette.placeholder": {
    ru: "Поиск проектов и ресурсов…",
    en: "Search projects and resources…",
  },
  "shell.palette.goTo": { ru: "Перейти", en: "Go to" },
  "shell.palette.project": { ru: "Проект", en: "Project" },
  "shell.palette.noMatches": { ru: "Ничего не найдено", en: "No matches" },
  "shell.palette.navigate": { ru: "навигация", en: "navigate" },
  "shell.palette.open": { ru: "открыть", en: "open" },
  "shell.palette.close": { ru: "закрыть", en: "close" },
  "shell.palette.label": { ru: "Командная палитра", en: "Command palette" },

  "shell.project.select": { ru: "Выберите проект", en: "Select project" },
  "shell.project.none": { ru: "Нет проектов", en: "No projects" },
  "shell.project.viewAll": { ru: "Все проекты →", en: "View all projects →" },
  "shell.project.create": { ru: "Создать проект", en: "Create project" },

  "shell.nav.openMenu": { ru: "Открыть меню", en: "Open menu" },
  "shell.nav.closeMenu": { ru: "Закрыть меню", en: "Close menu" },

  "shell.nav.advanced": { ru: "Дополнительно", en: "Advanced" },
  "shell.nav.admin": { ru: "Администрирование", en: "Admin" },
  "shell.nav.comingSoon": { ru: "Скоро", en: "Coming soon" },
  "shell.nav.soon": { ru: "Скоро", en: "Soon" },

  "shell.org.activeTitle": {
    ru: "Активная организация (переключение скоро)",
    en: "Active organization (switching coming soon)",
  },

  "shell.account.label": { ru: "Меню аккаунта", en: "Account menu" },
  "shell.account.approvals": { ru: "Согласования", en: "Approvals" },
  "shell.account.audit": { ru: "Журнал аудита", en: "Audit log" },
  "shell.account.overview": { ru: "Обзор платформы", en: "Platform overview" },
  "shell.account.signOut": { ru: "Выйти", en: "Sign out" },
  "shell.account.fallbackName": { ru: "Аккаунт", en: "Account" },

  "shell.lang.label": { ru: "Сменить язык", en: "Change language" },
};
