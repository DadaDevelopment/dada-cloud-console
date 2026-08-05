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
  "authError.retry": { ru: "Повторить", en: "Retry" },
  "authError.logout": { ru: "Выйти", en: "Sign out" },
};
