import type { Messages } from "./common";

/**
 * Shown instead of the console spinner when sign-in cannot settle on its
 * own (token refresh keeps failing, the SSO module failed to load, or the
 * loading watchdog gave up waiting). Deliberately does not promise a fix on
 * its own - it hands the user a manual retry and a manual sign-out, because
 * an automatic redirect back to the login screen is what causes the loop
 * this screen exists to break.
 */
export const authError: Messages = {
  "authError.title": { ru: "Не получилось войти", en: "Sign-in did not go through" },
  "authError.body": {
    ru: "Похоже, соединение с сервером входа оборвалось. Попробуй ещё раз или выйди и зайди заново.",
    en: "The connection to the sign-in server seems to have dropped. Try again, or sign out and back in.",
  },
  "authError.body.denied": {
    ru: "Доступ не подтверждён — вход остановился на согласии. Нажми «Войти заново», если это вышло случайно.",
    en: "Access was not granted - sign-in stopped at the consent step. Hit “Sign in again” if that was not on purpose.",
  },
  "authError.body.callback": {
    ru: "Вход не завершился: ссылка возврата уже использована или устарела. Начни вход заново — это обычно помогает.",
    en: "Sign-in did not finish: the return link was already used or has expired. Starting over usually fixes it.",
  },
  "authError.retry": { ru: "Повторить", en: "Retry" },
  "authError.retryLogin": { ru: "Войти заново", en: "Sign in again" },
  "authError.logout": { ru: "Выйти", en: "Sign out" },

  "authError.title.signupClosed": { ru: "Регистрация закрыта", en: "Registration is closed" },
  "authError.body.signupClosed": {
    ru: "Вход прошёл нормально — дело не в этом. Мы временно не открываем новые аккаунты, попробуй зайти позже.",
    en: "Sign-in went through fine - this isn't a login problem. We aren't opening new accounts right now, check back later.",
  },

  "authError.title.bootstrapFailed": { ru: "Не удалось создать проект", en: "Could not create a project" },
  "authError.body.bootstrapFailed": {
    ru: "Вход прошёл нормально — мы просто не смогли завести тебе первый проект. Попробуй ещё раз.",
    en: "Sign-in went through fine - we just couldn't set up your first project. Try again.",
  },
};
