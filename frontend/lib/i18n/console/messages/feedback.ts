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

  "feedback.mine.heading": { ru: "Мои обращения", en: "My tickets" },
  "feedback.mine.status.new": { ru: "Новое", en: "New" },
  "feedback.mine.status.inProgress": { ru: "В работе", en: "In progress" },
  "feedback.mine.status.resolved": { ru: "Решено", en: "Resolved" },
  "feedback.mine.age.hours": { ru: "{count} ч назад", en: "{count}h ago" },
  "feedback.mine.age.days": { ru: "{count} дн назад", en: "{count}d ago" },
  "feedback.mine.resolutionLabel": { ru: "Ответ поддержки:", en: "Support's answer:" },
  "feedback.mine.empty": { ru: "Обращений пока нет.", en: "No tickets yet." },

  "adminFeedback.crumb.feedback": { ru: "Обращения", en: "Feedback" },
  "adminFeedback.title": { ru: "Обращения пользователей", en: "User feedback" },
  "adminFeedback.subtitle": {
    ru: "Всё, что люди присылают из консоли. Доступно только администраторам платформы.",
    en: "Everything people send from inside the console. Platform-admin only.",
  },

  "adminFeedback.filter.all": { ru: "Все", en: "All" },
  "adminFeedback.filter.new": { ru: "Новые", en: "New" },
  "adminFeedback.filter.inProgress": { ru: "В работе", en: "In progress" },
  "adminFeedback.filter.resolved": { ru: "Закрытые", en: "Resolved" },

  "adminFeedback.status.new": { ru: "новое", en: "new" },
  "adminFeedback.status.in_progress": { ru: "в работе", en: "in progress" },
  "adminFeedback.status.resolved": { ru: "закрыто", en: "resolved" },

  "adminFeedback.age.hours": { ru: "{count} ч назад", en: "{count}h ago" },
  "adminFeedback.age.days": { ru: "{count} дн назад", en: "{count}d ago" },
  "adminFeedback.anonymous": { ru: "аноним", en: "anonymous" },

  "adminFeedback.action.autofix": { ru: "Починить AI", en: "Auto-fix with AI" },
  "adminFeedback.action.autofixRunning": { ru: "Запускаем…", en: "Starting…" },
  "adminFeedback.action.resolve": { ru: "Закрыть", en: "Resolve" },
  "adminFeedback.resolvePrompt": { ru: "Что сделали по обращению?", en: "What was done about this ticket?" },
  "adminFeedback.autofixStarted": {
    ru: "Запустили авто-исправление. Письмо с PR придёт, когда агент закончит.",
    en: "Auto-fix started. An email with the PR arrives when the agent finishes.",
  },
  "adminFeedback.notAutofixable": {
    ru: "Нечего чинить автоматически: обращение не привязано к приложению с подключённым репозиторием.",
    en: "Nothing to auto-fix: this ticket is not tied to an app with a connected repo.",
  },

  "adminFeedback.empty.title": { ru: "Обращений нет", en: "No feedback" },
  "adminFeedback.empty.body": {
    ru: "Сообщения из виджета обратной связи появятся здесь.",
    en: "Messages from the feedback widget will appear here.",
  },

  "adminFeedback.accessDenied": {
    ru: "Нет доступа. Обращения видны только администраторам платформы.",
    en: "No access. Feedback is available to platform admins only.",
  },
  "adminFeedback.error.load": { ru: "Не удалось загрузить обращения", en: "Failed to load feedback" },
  "adminFeedback.error.action": { ru: "Действие не выполнено", en: "The action failed" },
};
