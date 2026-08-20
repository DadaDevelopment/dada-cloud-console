import type { Messages } from "./common";

/**
 * `BuildWatcher` (global shell): the bottom-right notice reporting a
 * tracked build's terminal status to a user who has already left the build
 * page.
 */
export const buildWatcher: Messages = {
  "buildWatcher.success.title": { ru: "Приложение развёрнуто", en: "App deployed" },
  "buildWatcher.success.body": {
    ru: "Сборка {app} завершилась успешно.",
    en: "The build for {app} finished successfully.",
  },
  "buildWatcher.success.openApp": { ru: "Открыть приложение", en: "Open application" },
  "buildWatcher.failure.build.title": { ru: "Сборка не удалась", en: "Build failed" },
  "buildWatcher.failure.build.body": {
    ru: "Сборка {app} завершилась с ошибкой.",
    en: "The build for {app} failed.",
  },
  "buildWatcher.failure.platform.title": { ru: "Сбой на нашей стороне", en: "Our platform failed" },
  "buildWatcher.failure.platform.body": {
    ru: "Сборка {app} упала не из-за вашего кода — сломалось у нас. «Пересобрать» работает всегда.",
    en: "The build for {app} failed on our side, not in your code. Rebuild always works.",
  },
  "buildWatcher.failure.appDeleted.title": { ru: "Сборка прервана", en: "Build stopped" },
  "buildWatcher.failure.appDeleted.body": {
    ru: "Приложение {app} удалили, пока шла сборка, поэтому её прервали.",
    en: "The app {app} was deleted while this build was running, so it was stopped.",
  },
  "buildWatcher.openBuild": { ru: "Открыть сборку", en: "Open build" },
  "buildWatcher.dismiss": { ru: "Закрыть", en: "Dismiss" },
  "buildWatcher.notify.success.title": { ru: "Приложение развёрнуто", en: "App deployed" },
  "buildWatcher.notify.success.body": {
    ru: "Сборка {app} завершилась успешно. Нажмите, чтобы открыть.",
    en: "The build for {app} finished successfully. Click to open.",
  },
  "buildWatcher.notify.failure.build.title": { ru: "Сборка не удалась", en: "Build failed" },
  "buildWatcher.notify.failure.build.body": {
    ru: "Сборка {app} завершилась с ошибкой. Нажмите, чтобы посмотреть.",
    en: "The build for {app} failed. Click to see why.",
  },
  "buildWatcher.notify.failure.platform.title": { ru: "Сбой на нашей стороне", en: "Our platform failed" },
  "buildWatcher.notify.failure.platform.body": {
    ru: "Сборка {app} упала не из-за вашего кода. Нажмите, чтобы посмотреть.",
    en: "The build for {app} failed on our side, not in your code. Click to see why.",
  },
  "buildWatcher.notify.failure.appDeleted.title": { ru: "Сборка прервана", en: "Build stopped" },
  "buildWatcher.notify.failure.appDeleted.body": {
    ru: "Приложение {app} удалили, пока шла сборка. Нажмите, чтобы посмотреть.",
    en: "The app {app} was deleted while this build was running. Click to see why.",
  },
};
