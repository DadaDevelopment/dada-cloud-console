import type { Messages } from "./common";

/** Admin — platform state overview (god-admin dashboard). */
export const adminOverview: Messages = {
  "adminOverview.crumb.overview": { ru: "Обзор", en: "Overview" },

  "adminOverview.title": { ru: "Обзор платформы", en: "Platform overview" },
  "adminOverview.subtitle": {
    ru: "Текущее состояние платформы: пользователи, проекты, деньги и динамика. Доступно только администраторам платформы.",
    en: "Current platform state: users, projects, money and dynamics. Platform-admin only.",
  },

  "adminOverview.accessDenied": {
    ru: "Нет доступа. Обзор платформы доступен только администраторам платформы.",
    en: "No access. The platform overview is available to platform admins only.",
  },
  "adminOverview.error.load": { ru: "Не удалось загрузить обзор платформы", en: "Failed to load the platform overview" },

  "adminOverview.kpi.users": { ru: "Пользователи", en: "Users" },
  "adminOverview.kpi.usersSub": { ru: "+{d} за 24ч · +{w} за 7д · +{m} за 30д", en: "+{d} in 24h · +{w} in 7d · +{m} in 30d" },
  "adminOverview.kpi.active48h": { ru: "Активны за 48ч", en: "Active in 48h" },
  "adminOverview.kpi.apps": { ru: "Приложения", en: "Apps" },
  "adminOverview.kpi.appsSub": { ru: "{ready} готовы · {broken} сломаны", en: "{ready} ready · {broken} broken" },
  "adminOverview.kpi.builds7d": { ru: "Сборки за 7д", en: "Builds, 7d" },
  "adminOverview.kpi.builds7dSub": { ru: "{ok} успешно · {failed} ошибки", en: "{ok} success · {failed} failed" },
  "adminOverview.kpi.cost30d": { ru: "Затраты за 30д", en: "Cost, 30d" },
  "adminOverview.kpi.projects": { ru: "Проекты", en: "Projects" },
  "adminOverview.kpi.databases": { ru: "Базы данных", en: "Databases" },
  "adminOverview.kpi.domains": { ru: "Домены", en: "Domains" },
  "adminOverview.kpi.domainsSub": { ru: "{active} активны · {pending} в ожидании · {failed} ошибки", en: "{active} active · {pending} pending · {failed} failed" },

  "adminOverview.chart.signups": { ru: "Регистрации в день", en: "Signups per day" },
  "adminOverview.chart.builds": { ru: "Сборки в день", en: "Builds per day" },
  "adminOverview.chart.newApps": { ru: "Новые приложения в день", en: "New apps per day" },
  "adminOverview.chart.buildsSuccess": { ru: "успешно", en: "success" },
  "adminOverview.chart.buildsFailed": { ru: "ошибки", en: "failed" },

  "adminOverview.money.title": { ru: "Деньги", en: "Money" },
  "adminOverview.money.hardware": { ru: "Железо (30д)", en: "Hardware (30d)" },
  "adminOverview.money.revenue": { ru: "Прайс (30д)", en: "List price (30d)" },
  "adminOverview.money.margin": { ru: "Маржа (30д)", en: "Margin (30d)" },
  "adminOverview.money.metered": { ru: "Наюзано (30д)", en: "Consumed (30d)" },
  "adminOverview.money.paid": { ru: "Собрано (30д)", en: "Collected (30d)" },
  "adminOverview.money.uncollected": { ru: "Не оплачено (30д)", en: "Uncollected (30d)" },
  "adminOverview.money.top": { ru: "Клиенты с наименьшей маржой", en: "Clients with the lowest margin" },
  "adminOverview.money.unavailable": {
    ru: "Данные по затратам временно недоступны (OpenCost).",
    en: "Cost data temporarily unavailable (OpenCost).",
  },
  "adminOverview.money.empty": { ru: "Нет данных по затратам", en: "No cost data" },
  "adminOverview.money.fullBreakdown": { ru: "Откуда берутся цифры →", en: "Where the numbers come from →" },

  "adminOverview.notReady.title": { ru: "Что сломано сейчас", en: "What's broken right now" },
  "adminOverview.notReady.subtitle": {
    ru: "Приложения не в статусе Ready",
    en: "Apps not in the Ready phase",
  },
  "adminOverview.notReady.empty": { ru: "Все приложения в порядке", en: "All apps are healthy" },
  "adminOverview.notReady.col.name": { ru: "Приложение", en: "App" },
  "adminOverview.notReady.col.project": { ru: "Проект", en: "Project" },
  "adminOverview.notReady.col.phase": { ru: "Статус", en: "Phase" },
  "adminOverview.notReady.col.reason": { ru: "Причина", en: "Reason" },
  "adminOverview.notReady.col.owner": { ru: "Владелец", en: "Owner" },

  "adminOverview.linkAudit": { ru: "Журнал аудита", en: "Audit log" },
};
