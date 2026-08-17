import type { Messages } from "./common";

/**
 * Admin — product adoption funnel (signup -> App/DB/VM/Box/S3/Model -> paid).
 */
export const adminFunnel: Messages = {
  "adminFunnel.crumb.funnel": { ru: "Воронка", en: "Funnel" },
  "adminFunnel.title": { ru: "Воронка продукта", en: "Product funnel" },
  "adminFunnel.subtitle": {
    ru: "Регистрация → какой ресурс завёл → оплата. Реальные данные из БД, окно и когорта фильтруются.",
    en: "Signup → which resource they set up → paid. Real DB data, filterable by window and cohort.",
  },
  "adminFunnel.accessDenied": {
    ru: "Нет доступа. Воронка доступна администраторам и аналитикам платформы.",
    en: "No access. The funnel is available to platform admins and analysts only.",
  },
  "adminFunnel.error.load": { ru: "Не удалось загрузить воронку", en: "Failed to load the funnel" },

  "adminFunnel.window.7d": { ru: "7 дней", en: "7d" },
  "adminFunnel.window.30d": { ru: "30 дней", en: "30d" },
  "adminFunnel.window.90d": { ru: "90 дней", en: "90d" },
  "adminFunnel.window.all": { ru: "Всё время", en: "All time" },

  "adminFunnel.stage.signups": { ru: "Регистрации", en: "Signups" },
  "adminFunnel.stage.app": { ru: "App", en: "App" },
  "adminFunnel.stage.db": { ru: "БД", en: "DB" },
  "adminFunnel.stage.vm": { ru: "VM", en: "VM" },
  "adminFunnel.stage.box": { ru: "Box", en: "Box" },
  "adminFunnel.stage.s3": { ru: "S3", en: "S3" },
  "adminFunnel.stage.model": { ru: "Model", en: "Model" },
  "adminFunnel.stage.paid": { ru: "Оплата", en: "Paid" },

  "adminFunnel.note.paid": {
    ru: "Модель считается по факту наличия строки (фаза не доходит до Ready). VM/Box считаются по признаку «когда-либо был доступен», а не по текущему статусу.",
    en: "Model counts row presence only (its phase never reaches Ready). VM/Box count \"ever became reachable\", not current status.",
  },
  "adminFunnel.metrikaGap.title": { ru: "Канальная воронка не здесь", en: "Channel funnel not here" },
  "adminFunnel.metrikaGap.body": {
    ru: "Верх воронки по каналам (Метрика) не подключён к бэкенду живым запросом — нужна отдельная интеграция с Reporting API. Здесь только продуктовая часть на реальных данных БД.",
    en: "Channel-based top-of-funnel (Metrika) has no live backend query yet — needs a separate Reporting API integration. This page is the DB-backed product part only.",
  },

  "adminFunnel.cohort.label": { ru: "Когорта", en: "Cohort" },
};
