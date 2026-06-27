import type { Messages } from "./common";

/** Project members page + add-member modal (PRD-IAM "Members page"). */
export const members: Messages = {
  "members.title": { ru: "Участники", en: "Members" },
  "members.subtitle": {
    ru: "Членство управляется вашей организацией. Роли определяют доступ в рамках этого проекта.",
    en: "Membership is managed by your organization. Roles control access across this project.",
  },

  "members.col.member": { ru: "Участник", en: "Member" },
  "members.col.type": { ru: "Тип", en: "Type" },
  "members.col.role": { ru: "Роль", en: "Role" },

  "members.type.serviceAccount": { ru: "Сервисный аккаунт", en: "Service account" },
  "members.type.user": { ru: "Пользователь", en: "User" },

  "members.search.placeholder": { ru: "Поиск участников…", en: "Search members…" },

  "members.empty": { ru: "Участников пока нет.", en: "No members yet." },

  "members.modal.title": { ru: "Добавить участника", en: "Add member" },
  "members.modal.email.label": { ru: "Email", en: "Email" },
  "members.modal.email.placeholder": { ru: "teammate@company.com", en: "teammate@company.com" },
  "members.modal.role.label": { ru: "Роль", en: "Role" },
  "members.modal.invite.label": { ru: "Отправить приглашение по email", en: "Send email invitation" },
  "members.modal.invite.help": {
    ru: "Для пользователей, которые ещё не зарегистрированы. Иначе существующий пользователь будет добавлен немедленно.",
    en: "For users who haven't registered yet. Otherwise the existing user is added immediately.",
  },
  "members.modal.invite.noOrg": {
    ru: " Нет активной организации в сессии — приглашение недоступно.",
    en: " No active org in your session — invite unavailable.",
  },
  "members.modal.submit.invite": { ru: "Отправить приглашение", en: "Send invite" },

  "members.error.noOrg": {
    ru: "Нет активной организации в токене — невозможно отправить приглашение",
    en: "No active org in token — cannot send invite",
  },
  "members.error.load": { ru: "Не удалось загрузить участников", en: "Failed to load members" },
  "members.error.changeRole": { ru: "Не удалось изменить роль", en: "Failed to change role" },
  "members.error.remove": { ru: "Не удалось удалить участника", en: "Failed to remove member" },
  "members.error.add": { ru: "Не удалось добавить участника", en: "Failed to add member" },

  "members.confirm.remove": {
    ru: "Удалить {email} из этого проекта?",
    en: "Remove {email} from this project?",
  },

  "members.accessDenied": {
    ru: "Вам нужна роль Владельца или Администратора для управления участниками.",
    en: "You need an Owner or Admin role to manage members.",
  },
};
