import type { Messages } from "./common";

/** RBAC role labels (lib/rbac MemberRole). Keyed by `roles.<MemberRole>`. */
export const roles: Messages = {
  "roles.Owner": { ru: "Владелец", en: "Owner" },
  "roles.Admin": { ru: "Администратор", en: "Admin" },
  "roles.Developer": { ru: "Разработчик", en: "Developer" },
  "roles.ReadOnly": { ru: "Только чтение", en: "Read Only" },
};
