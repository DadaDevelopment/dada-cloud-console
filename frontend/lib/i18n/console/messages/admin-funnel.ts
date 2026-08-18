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
  "adminFunnel.channel.title": { ru: "Канальная воронка: лендинг → регистрация", en: "Channel funnel: landing → signup" },
  "adminFunnel.channel.body": {
    ru: "Живой Reporting API Яндекс.Метрики. Источник — классификация визита Метрикой, не UTM-идентификатор пользователя.",
    en: "Live Yandex.Metrika Reporting API. Source is Metrika visit classification, not a UTM user identifier.",
  },
  "adminFunnel.channel.unavailable": { ru: "Метрика временно недоступна", en: "Metrika is temporarily unavailable" },
  "adminFunnel.channel.source": { ru: "Источник", en: "Source" },
  "adminFunnel.channel.visits": { ru: "Визиты", en: "Visits" },
  "adminFunnel.channel.register": { ru: "Открыли /register", en: "Opened /register" },
  "adminFunnel.channel.started": { ru: "Выбрали способ", en: "Chose a method" },
  "adminFunnel.channel.complete": { ru: "Регистрация завершена", en: "Registration complete" },
  "adminFunnel.channel.deploy": { ru: "Успешный деплой", en: "Successful deploy" },
  "adminFunnel.channel.total": { ru: "Все источники", en: "All sources" },
  "adminFunnel.channel.note": {
    ru: "«Успешный деплой» — отдельное действие авторизованных пользователей в том же окне; он не считается конверсией из регистрации.",
    en: "“Successful deploy” is a separate action by authenticated users in the same window; it is not treated as signup conversion.",
  },

  "adminFunnel.cohort.label": { ru: "Когорта", en: "Cohort" },
};
