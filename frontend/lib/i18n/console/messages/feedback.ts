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
};
