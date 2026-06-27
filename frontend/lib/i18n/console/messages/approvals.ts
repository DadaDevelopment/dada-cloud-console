import type { Messages } from "./common";

/** Admin — pending approvals page. */
export const approvals: Messages = {
  "approvals.crumb.admin": { ru: "Администратор", en: "Admin" },
  "approvals.crumb.approvals": { ru: "Согласования", en: "Approvals" },

  "approvals.title": { ru: "Ожидающие согласования", en: "Pending approvals" },
  "approvals.subtitle": {
    ru: "Операции в статусе WaitingForApproval. Первый потребитель — шлюз GPU в AI Studio.",
    en: "Operations parked in WaitingForApproval. First consumer is the AI Studio GPU gate.",
  },

  "approvals.col.project": { ru: "Проект", en: "Project" },
  "approvals.col.resource": { ru: "Ресурс", en: "Resource" },
  "approvals.col.action": { ru: "Действие", en: "Action" },
  "approvals.col.requestedBy": { ru: "Запросил", en: "Requested by" },
  "approvals.col.age": { ru: "Возраст", en: "Age" },
  "approvals.col.summary": { ru: "Сводка", en: "Summary" },
  "approvals.col.decision": { ru: "Решение", en: "Decision" },

  "approvals.action.approve": { ru: "Одобрить", en: "Approve" },
  "approvals.action.reject": { ru: "Отклонить", en: "Reject" },

  "approvals.search.placeholder": { ru: "Поиск согласований…", en: "Search approvals…" },

  "approvals.empty.title": { ru: "Нет ожидающих согласований", en: "No pending approvals" },
  "approvals.empty.body": {
    ru: "Запросы GPU-моделей и другие привилегированные операции появятся здесь.",
    en: "GPU model requests and other privileged operations will appear here.",
  },

  "approvals.reject.title": { ru: "Отклонить операцию", en: "Reject operation" },
  "approvals.reject.body": {
    ru: "Причина будет сохранена на операции и показана инициатору в временной шкале операций.",
    en: "The reason will be stored on the operation and shown to the requester in the operations timeline.",
  },
  "approvals.reject.reasonLabel": { ru: "Причина", en: "Reason" },
  "approvals.reject.reasonPlaceholder": {
    ru: "Мощности GPU зарезервированы для переноса прод на этой неделе. Попробуйте в понедельник.",
    en: "GPU capacity reserved for prod migration this week. Try again Monday.",
  },
  "approvals.reject.submitting": { ru: "Отклонение…", en: "Rejecting..." },

  "approvals.accessDenied": {
    ru: "Согласования доступны только администраторам платформы.",
    en: "Approvals are available to platform admins only.",
  },

  "approvals.error.load": { ru: "Не удалось загрузить согласования", en: "Failed to load approvals" },
  "approvals.error.approve": { ru: "Не удалось одобрить", en: "Failed to approve" },
  "approvals.error.reject": { ru: "Не удалось отклонить", en: "Failed to reject" },
};
