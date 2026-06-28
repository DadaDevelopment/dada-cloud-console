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
    ru: "Проект создаётся в личном пространстве — вы станете его владельцем.",
    en: "Projects are created in your personal space by default — you become the Owner.",
  },
  "projects.name.label": { ru: "Имя проекта", en: "Project name" },
  "projects.name.placeholder": { ru: "my-project", en: "my-project" },
  "projects.name.help": {
    ru: "3–40 символов, строчные буквы/цифры/дефисы, начинается с буквы. Используется как slug и префикс пространства имён.",
    en: "3–40 chars, lowercase letters/digits/dashes, starts with a letter. Used as the slug and namespace prefix.",
  },
  "projects.submit": { ru: "Создать проект", en: "Create project" },

  "projects.error.create": { ru: "Не удалось создать проект", en: "Failed to create project" },
  "projects.error.load": { ru: "Не удалось загрузить проекты", en: "Failed to load projects" },
};
