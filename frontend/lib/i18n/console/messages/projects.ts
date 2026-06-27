import type { Messages } from "./common";

/** Projects list page + create-project modal. */
export const projects: Messages = {
  "projects.title": { ru: "Проекты", en: "Projects" },
  "projects.subtitle": {
    ru: "Ваши проекты облачной инфраструктуры",
    en: "Your cloud infrastructure projects",
  },
  "projects.new": { ru: "Новый проект", en: "New project" },
  "projects.view": { ru: "Открыть", en: "View" },

  "projects.empty.title": { ru: "Пока нет проектов", en: "No projects yet" },
  "projects.empty.subtitle": {
    ru: "Создайте первый проект, чтобы начать.",
    en: "Create your first project to get started.",
  },

  "projects.modal.title": { ru: "Новый проект", en: "New project" },
  "projects.modal.subtitle": {
    ru: "Оставьте организацию пустой, чтобы создать проект в личном пространстве — вы станете его владельцем.",
    en: "Leave the organization blank to create it in your personal space — you become its Owner.",
  },
  "projects.slug.label": { ru: "Слаг", en: "Slug" },
  "projects.slug.help": {
    ru: "3–40 символов, строчные буквы/цифры/дефисы, начинается с буквы. Используется как префикс пространства имён.",
    en: "3–40 chars, lowercase letters/digits/dashes, starts with a letter. Used as the namespace prefix.",
  },
  "projects.displayName.label": { ru: "Отображаемое имя", en: "Display name" },
  "projects.displayName.placeholder": { ru: "Моё приложение", en: "My App" },
  "projects.org.label": { ru: "Организация", en: "Organization" },
  "projects.org.placeholder": { ru: "личная", en: "personal" },
  "projects.org.helpPre": {
    ru: "Общая организация, которой вы управляете (например, ",
    en: "A shared org you administer (e.g. ",
  },
  "projects.org.helpPost": {
    ru: "). Пусто = личная организация.",
    en: "). Blank = personal org.",
  },
  "projects.submit": { ru: "Создать проект", en: "Create project" },

  "projects.error.create": { ru: "Не удалось создать проект", en: "Failed to create project" },
  "projects.error.load": { ru: "Не удалось загрузить проекты", en: "Failed to load projects" },
};
