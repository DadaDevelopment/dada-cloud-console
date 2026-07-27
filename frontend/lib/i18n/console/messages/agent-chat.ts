import type { Messages } from "./common";

/** In-console agent chat panel (agentChat.*). */
export const agentChat: Messages = {
  "agentChat.title": { ru: "Агент", en: "Agent" },
  "agentChat.open": { ru: "Открыть чат с агентом", en: "Open agent chat" },
  "agentChat.close": { ru: "Свернуть чат", en: "Collapse chat" },
  "agentChat.placeholder": { ru: "Спросите что-нибудь про проект…", en: "Ask something about your project…" },
  "agentChat.send": { ru: "Отправить", en: "Send" },
  "agentChat.emptyState": {
    ru: "Агент отвечает на вопросы о вашем проекте. Пока это черновик — ответы эхо, реальный разум подключим следующим шагом.",
    en: "The agent answers questions about your project. This is a skeleton for now — replies echo back; real reasoning lands next.",
  },
  "agentChat.errorGeneric": {
    ru: "Не удалось получить ответ. Попробуйте ещё раз.",
    en: "Could not get a response. Please try again.",
  },
  "agentChat.thinking": { ru: "Печатает…", en: "Thinking…" },
  "agentChat.toolCall": { ru: "Вызван инструмент: {name}", en: "Used tool: {name}" },
  "agentChat.error.notConfigured": {
    ru: "Агент пока не настроен на этом сервере.",
    en: "The agent is not configured on this server yet.",
  },
  "agentChat.error.dailyCap": {
    ru: "Достигнут дневной лимит запросов к агенту. Попробуйте завтра.",
    en: "Daily agent request limit reached. Please try again tomorrow.",
  },
  "agentChat.error.upstream": {
    ru: "Агент временно недоступен. Попробуйте ещё раз чуть позже.",
    en: "The agent is temporarily unavailable. Please try again shortly.",
  },
  "agentChat.confirm.title": { ru: "Требуется подтверждение", en: "Confirmation required" },
  "agentChat.confirm.approve": { ru: "Подтвердить", en: "Approve" },
  "agentChat.confirm.reject": { ru: "Отклонить", en: "Reject" },
  "agentChat.confirm.approved": { ru: "Подтверждено", en: "Approved" },
  "agentChat.confirm.rejected": { ru: "Отклонено", en: "Rejected" },
  "agentChat.confirm.running": { ru: "Выполняется…", en: "Running…" },
  "agentChat.confirm.blockedHint": {
    ru: "Подтвердите или отклоните действие выше, чтобы продолжить.",
    en: "Approve or reject the action above to continue.",
  },
  "agentChat.confirm.priceEstimate": {
    ru: "Ориентировочная стоимость: {price}",
    en: "Estimated cost: {price}",
  },
  "agentChat.confirm.priceAck": {
    ru: "Я понимаю, что это создаст ежемесячный платёж",
    en: "I understand this will create a recurring monthly charge",
  },
  "agentChat.tool.restartApp": { ru: "Перезапустить приложение", en: "Restart app" },
  "agentChat.tool.triggerBuild": { ru: "Запустить сборку", en: "Trigger build" },
  "agentChat.tool.deployTrigger": { ru: "Запустить деплой", en: "Trigger deploy" },
  "agentChat.tool.cancelBuild": { ru: "Отменить сборку", en: "Cancel build" },
  "agentChat.tool.retryOperation": { ru: "Повторить операцию", en: "Retry operation" },
  "agentChat.tool.setEnvVar": { ru: "Установить переменную окружения", en: "Set environment variable" },
  "agentChat.tool.deleteEnvVar": { ru: "Удалить переменную окружения", en: "Delete environment variable" },
  "agentChat.tool.rollbackApp": { ru: "Откатить приложение", en: "Roll back app" },
  "agentChat.tool.rollbackDeployment": { ru: "Откатить деплой", en: "Roll back deployment" },
  "agentChat.tool.promoteDeployment": { ru: "Промоутнуть деплой", en: "Promote deployment" },
  "agentChat.tool.updateAppImage": { ru: "Обновить образ приложения", en: "Update app image" },
  "agentChat.tool.updateAppProfile": { ru: "Изменить профиль приложения", en: "Update app profile" },
  "agentChat.tool.updateAppStorage": { ru: "Изменить хранилище приложения", en: "Update app storage" },
  "agentChat.tool.createDatabase": { ru: "Создать базу данных", en: "Create database" },
  "agentChat.tool.createEndpoint": { ru: "Создать публичный эндпоинт", en: "Create public endpoint" },
  "agentChat.tool.createS3Bucket": { ru: "Создать S3-бакет", en: "Create S3 bucket" },
};
