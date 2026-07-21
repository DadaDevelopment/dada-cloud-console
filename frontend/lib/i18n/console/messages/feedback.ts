import type { Messages } from "./common";

/** In-product feedback surfaces: crash boundary and the floating support link. */
export const feedback: Messages = {
  "feedback.crash.title": { ru: "Что-то пошло не так", en: "Something went wrong" },
  "feedback.crash.body": {
    ru: "Эта часть консоли неожиданно упала. Остальной интерфейс не затронут.",
    en: "This part of the console crashed unexpectedly. The rest of the interface is unaffected.",
  },
  "feedback.crash.reload": { ru: "Перезагрузить", en: "Reload" },
  "feedback.crash.contactSupport": { ru: "Написать в поддержку", en: "Contact support" },
  "feedback.crash.mailSubject": { ru: "Ошибка в консоли Dada Cloud", en: "Dada Cloud console error" },

  "feedback.support.label": { ru: "Поддержка", en: "Support" },
  "feedback.support.mailSubject": { ru: "Вопрос по консоли Dada Cloud", en: "Dada Cloud console question" },

  "feedback.widget.button": { ru: "Обратная связь", en: "Feedback" },
  "feedback.widget.title": { ru: "Обратная связь", en: "Feedback" },
  "feedback.widget.placeholder": {
    ru: "Что можно улучшить или что сломалось?",
    en: "What could be better, or what broke?",
  },
  "feedback.widget.submit": { ru: "Отправить", en: "Send" },
  "feedback.widget.sending": { ru: "Отправка…", en: "Sending…" },
  "feedback.widget.success": { ru: "Спасибо! Мы получили ваше сообщение.", en: "Thanks! We got your message." },
  "feedback.widget.error": { ru: "Не удалось отправить. Попробуйте ещё раз.", en: "Couldn't send it. Try again." },
  "feedback.widget.retry": { ru: "Повторить", en: "Retry" },
};
