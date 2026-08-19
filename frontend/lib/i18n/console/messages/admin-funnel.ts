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
  "adminFunnel.channel.table": { ru: "Показать таблицей", en: "Show as table" },
  "adminFunnel.channel.note": {
    ru: "«Успешный деплой» — отдельное действие авторизованных пользователей в том же окне; он не считается конверсией из регистрации.",
    en: "“Successful deploy” is a separate action by authenticated users in the same window; it is not treated as signup conversion.",
  },

  "adminFunnel.channel.entered": { ru: "Пришли на сайт", en: "Arrived" },
  "adminFunnel.channel.left": { ru: "Ушли, не открыв регистрацию", en: "Left without opening signup" },
  "adminFunnel.channel.abandoned": { ru: "Не выбрали способ", en: "Chose no method" },
  "adminFunnel.channel.unfinished": { ru: "Не завершили регистрацию", en: "Did not finish signup" },
  "adminFunnel.channel.dropped": { ru: "Отвал", en: "Drop-off" },
  "adminFunnel.channel.clamped": {
    ru: "Метрика вернула по источникам {sources} этап больше предыдущего (цели сэмплируются независимо) — на схеме он подрезан до предыдущего.",
    en: "Metrika returned a stage larger than the one above it for {sources} (goals are sampled independently); the chart clamps it to the stage above.",
  },
  "adminFunnel.channel.deployAside": {
    ru: "{count} чел. успешно задеплоили за окно — считаются отдельно от воронки: в них есть и те, кто зарегистрировался раньше окна.",
    en: "{count} people deployed successfully in the window — counted outside the funnel: it includes people who signed up before the window.",
  },
  "adminFunnel.channel.visitsAside": {
    ru: "{visits} визитов за окно. Ступени воронки — уникальные пользователи Метрики, а не события: один человек с пятью деплоями — это один.",
    en: "{visits} visits in the window. Funnel stages are unique Metrika users, not events: one person with five deploys counts once.",
  },

  "adminFunnel.reg.title": {
    ru: "Регистрация в Keycloak: открыл форму → зарегистрировался",
    en: "Keycloak registration: opened the form → signed up",
  },
  "adminFunnel.reg.body": {
    ru: "Этапы — из отдельного счётчика Метрики на id.dada-tuda.ru (не общий счётчик консоли). «Зарегистрировано» — реальные строки в базе за то же окно, не сэмпл Метрики. Этапы видят только форму email/пароль — вход через Яндекс/Google/GitHub уходит на сторону провайдера и минует эту форму, поэтому его считает блок «По каналу» ниже.",
    en: "Stages come from a separate Metrika counter on id.dada-tuda.ru (not the console counter). “Registered” is real DB rows for the same window, not a Metrika sample. The stages only see the email/password form — Yandex/Google/GitHub logins leave for the provider and never touch it, so the channel block below is what counts them.",
  },
  "adminFunnel.reg.registered": { ru: "Зарегистрировано (БД)", en: "Registered (DB)" },
  "adminFunnel.reg.unavailable": { ru: "Метрика недоступна", en: "Metrika unavailable" },
  "adminFunnel.reg.allZero": {
    ru: "Форму email/пароль за окно не открывал никто: единственная открытая дверь регистрации — Яндекс ID, а он минует эту форму. Разбивка по дверям — в блоке ниже.",
    en: "Nobody opened the email/password form in this window: the only open signup door is Yandex ID, which bypasses the form. The door breakdown is in the block below.",
  },

  "adminFunnel.door.title": { ru: "Регистрация по каналу: пароль vs провайдер", en: "Signup by door: password vs provider" },
  "adminFunnel.door.body": {
    ru: "Реальные строки БД, по тому, как родился аккаунт — email/пароль или брокер (Яндекс и т.п. не требуют подтверждения почты, конверсия там обычно выше). Аккаунты, заведённые до этой метки, в разбивку не попадают — их канал не записан.",
    en: "Real DB rows, by how the account was born — email/password or a broker (Yandex and friends need no email confirmation and usually convert better). Accounts created before this field existed have no recorded door and are left out.",
  },
  "adminFunnel.door.empty": {
    ru: "Нет данных за окно — либо ещё не было регистраций, либо все они старше метки канала.",
    en: "No data for this window — either there were no signups, or they all predate the door field.",
  },

  "adminFunnel.cohort.label": { ru: "Когорта", en: "Cohort" },
};
