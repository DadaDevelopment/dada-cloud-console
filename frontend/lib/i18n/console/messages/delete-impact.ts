import type { Messages } from "./common";

/** DeleteImpactModal — shared cluster-truth impact preview for app + project delete. */
export const deleteImpact: Messages = {
  "deleteImpact.title.app": { ru: "Удалить приложение", en: "Delete app" },
  "deleteImpact.title.project": { ru: "Удалить проект", en: "Delete project" },

  "deleteImpact.loading": { ru: "Проверяем кластер…", en: "Scanning the cluster…" },
  "deleteImpact.error.load": { ru: "Не удалось получить список ресурсов", en: "Failed to load the resource impact" },
  "deleteImpact.error.submit.app": { ru: "Не удалось удалить приложение", en: "Failed to delete app" },
  "deleteImpact.error.submit.project": { ru: "Не удалось удалить проект", en: "Failed to delete project" },

  "deleteImpact.empty": { ru: "Ресурсы не найдены — можно удалять.", en: "No resources found — safe to delete." },

  "deleteImpact.group.domain": { ru: "Домены", en: "Domains" },
  "deleteImpact.group.database": { ru: "Базы данных", en: "Databases" },
  "deleteImpact.group.storage": { ru: "Хранилище", en: "Storage" },
  "deleteImpact.group.ingress": { ru: "Входящий трафик", en: "Ingress" },
  "deleteImpact.group.certificate": { ru: "Сертификаты", en: "Certificates" },
  "deleteImpact.group.other": { ru: "Прочее", en: "Other" },

  "deleteImpact.source.console": { ru: "известно консоли", en: "tracked by console" },
  "deleteImpact.source.clusterOnly": { ru: "только в кластере", en: "cluster-only" },

  "deleteImpact.banner.clusterOnly": {
    ru: "Эти ресурсы есть в кластере, но консоль их не отслеживала — при удалении они тоже будут удалены.",
    en: "These resources exist in the cluster but the console never tracked them — deleting will remove them too.",
  },
  "deleteImpact.banner.noScan": {
    ru: "Не удалось просканировать кластер — список ресурсов может быть неполным.",
    en: "The cluster could not be scanned — this list may be incomplete.",
  },

  "deleteImpact.confirm.label.app": {
    ru: "Введите {name}, чтобы подтвердить удаление приложения",
    en: "Type {name} to confirm deleting this app",
  },
  "deleteImpact.confirm.label.project": {
    ru: "Введите {name}, чтобы подтвердить удаление проекта",
    en: "Type {name} to confirm deleting this project",
  },
  "deleteImpact.confirm.placeholder": { ru: "{name}", en: "{name}" },

  "deleteImpact.submit.app": { ru: "Удалить приложение", en: "Delete app" },
  "deleteImpact.submit.project": { ru: "Удалить проект", en: "Delete project" },
  "deleteImpact.submitting": { ru: "Удаление…", en: "Deleting…" },
};
