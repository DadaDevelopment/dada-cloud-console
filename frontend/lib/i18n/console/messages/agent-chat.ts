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
};
