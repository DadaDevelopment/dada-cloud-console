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
  "buildWatcher.failure.title": { ru: "Сборка не удалась", en: "Build failed" },
  "buildWatcher.failure.body": {
    ru: "Сборка {app} завершилась с ошибкой.",
    en: "The build for {app} failed.",
  },
  "buildWatcher.openBuild": { ru: "Открыть сборку", en: "Open build" },
  "buildWatcher.dismiss": { ru: "Закрыть", en: "Dismiss" },
  "buildWatcher.notify.success.title": { ru: "Приложение развёрнуто", en: "App deployed" },
  "buildWatcher.notify.success.body": {
    ru: "Сборка {app} завершилась успешно. Нажмите, чтобы открыть.",
    en: "The build for {app} finished successfully. Click to open.",
  },
  "buildWatcher.notify.failure.title": { ru: "Сборка не удалась", en: "Build failed" },
  "buildWatcher.notify.failure.body": {
    ru: "Сборка {app} завершилась с ошибкой. Нажмите, чтобы посмотреть.",
    en: "The build for {app} failed. Click to see why.",
  },
};
