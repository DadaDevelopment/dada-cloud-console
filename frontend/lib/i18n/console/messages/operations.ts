import type { Messages } from "./common";

/** Operations page — deployment and provisioning history. */
export const operations: Messages = {
  "operations.title": { ru: "Операции", en: "Operations" },
  "operations.live": { ru: "Live", en: "Live" },
  "operations.subtitle": {
    ru: "История развёртывания и provisioning",
    en: "Deployment and provisioning history",
  },

  "operations.search.placeholder": {
    ru: "Поиск по действию, ресурсу или виду…",
    en: "Search by action, resource, or kind…",
  },
  "operations.filter.all": { ru: "Все статусы", en: "All statuses" },
  "operations.filter.inProgress": { ru: "В процессе", en: "In progress" },
  "operations.filter.ready": { ru: "Готово", en: "Ready" },
  "operations.filter.failed": { ru: "Ошибка", en: "Failed" },
  "operations.filter.waitingForApproval": {
    ru: "Ожидает согласования",
    en: "Waiting for approval",
  },
  "operations.countOf": {
    ru: "{count} из {total}",
    en: "{count} of {total}",
  },

  "operations.empty.title": { ru: "Операций пока нет", en: "No operations yet" },
  "operations.empty.subtitle": {
    ru: "Операции появятся здесь при создании или изменении ресурсов.",
    en: "Operations appear here when you create or modify resources.",
  },
  "operations.noMatch": {
    ru: "Нет операций, подходящих под фильтры.",
    en: "No operations match your filters.",
  },

  "operations.detail.operationId": { ru: "ID операции", en: "Operation ID" },
  "operations.detail.action": { ru: "Действие", en: "Action" },
  "operations.detail.resource": { ru: "Ресурс", en: "Resource" },
  "operations.detail.gitCommit": { ru: "Git Commit", en: "Git Commit" },
  "operations.detail.gitPath": { ru: "Git Path", en: "Git Path" },
  "operations.detail.created": { ru: "Создано", en: "Created" },
  "operations.detail.updated": { ru: "Обновлено", en: "Updated" },
  "operations.detail.error": { ru: "Ошибка", en: "Error" },

  "operations.error.load": {
    ru: "Не удалось загрузить операции",
    en: "Failed to load operations",
  },
};
