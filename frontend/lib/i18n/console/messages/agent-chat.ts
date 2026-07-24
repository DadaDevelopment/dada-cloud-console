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
};
