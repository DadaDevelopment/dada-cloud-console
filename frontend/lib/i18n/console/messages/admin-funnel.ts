import type { Messages } from "./common";

/**
 * Admin — product adoption funnel (signup -> App/DB/VM/Box/S3/Model -> paid).
 */
export const adminFunnel: Messages = {
  "adminFunnel.crumb.funnel": { ru: "Воронка", en: "Funnel" },
  "adminFunnel.title": { ru: "Воронки продукта", en: "Product funnels" },
  "adminFunnel.subtitle": {
    ru: "Два подробных пути: веб-источник → аккаунт → первый вход и все customer-аккаунты → ресурсы → оплата.",
    en: "Two detailed paths: web source to account to first entry, and all customer accounts to resources to payment.",
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
  "adminFunnel.stage.activated": { ru: "Есть готовый ресурс", en: "Have a ready resource" },
  "adminFunnel.stage.paid": { ru: "Оплата", en: "Paid" },

  "adminFunnel.note.paid": {
    ru: "Model считается по факту наличия строки: у него нет готовой фазы. В готовый ресурс попадают только App/БД/S3 Ready, VM Ready и Box Ready/Idle.",
    en: "Model counts row presence only: it has no ready phase. Ready resources include only App/DB/S3 Ready, VM Ready, and Box Ready/Idle.",
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
  "adminFunnel.acquisition.title": { ru: "1. От лендинга до аккаунта и первого входа", en: "1. Landing to account and first entry" },
  "adminFunnel.acquisition.body": {
    ru: "Единая схема содержит три точные ленты: Метрика, UX браузера и подтверждённый сервером аккаунт. Анонимные идентификаторы не склеиваются с аккаунтами: граница показана без выдуманной конверсии.",
    en: "One diagram contains three exact ribbons: Metrika, browser UX and server-confirmed accounts. Anonymous identities are not merged with accounts, so the boundary does not invent conversion.",
  },
  "adminFunnel.acquisition.metrikaBoundary": {
    ru: "{visits} визитов за окно. Успешных деплоев: {deploys}; это отдельное действие авторизованных людей и не является переходом регистрации.",
    en: "{visits} visits in the window. Successful deploys: {deploys}; this is a separate authenticated action, not a registration transition.",
  },
  "adminFunnel.acquisition.confirmedTitle": { ru: "Подтверждённый путь до входа", en: "Confirmed path to entry" },
  "adminFunnel.acquisition.metrikaStream": { ru: "Метрика: все источники", en: "Metrika: all sources" },
  "adminFunnel.acquisition.metrikaUsers": { ru: "уникальные пользователи", en: "unique users" },
  "adminFunnel.acquisition.uxStream": { ru: "UX: браузерные идентичности", en: "UX: browser identities" },
  "adminFunnel.acquisition.accountStream": { ru: "Сервер: созданные аккаунты", en: "Server: created accounts" },
  "adminFunnel.acquisition.visitsDetail": { ru: "визиты не смешиваются с пользователями", en: "visits are not mixed with users" },
  "adminFunnel.acquisition.deployDetail": { ru: "отдельное действие, не переход регистрации", en: "separate action, not a signup transition" },
  "adminFunnel.acquisition.confirmedBody": {
    ru: "UX-события считаются по браузерной идентичности; аккаунт — по строке users; первый вход — по серверному SessionStart после создания аккаунта. Процент показан только для сопоставимой пары этапов.",
    en: "UX events count browser identities; an account is a users row; first entry is the server SessionStart after account creation. A rate appears only for comparable stages.",
  },
  "adminFunnel.acquisition.landing": { ru: "Открыли лендинг", en: "Opened landing" },
  "adminFunnel.acquisition.started": { ru: "Начали регистрацию", en: "Started signup" },
  "adminFunnel.acquisition.account": { ru: "Создан аккаунт", en: "Account created" },
  "adminFunnel.acquisition.firstEntry": { ru: "Первый авторизованный вход", en: "First authenticated entry" },
  "adminFunnel.acquisition.uxSource": { ru: "UX: уникальные браузерные идентичности", en: "UX: unique browser identities" },
  "adminFunnel.acquisition.dbSource": { ru: "БД: реальная строка аккаунта", en: "DB: actual account row" },
  "adminFunnel.acquisition.auditSource": { ru: "Аудит: первый SessionStart после регистрации", en: "Audit: first SessionStart after signup" },
  "adminFunnel.lifecycle.title": { ru: "2. От всех пользователей до ресурсов, квот и оплаты", en: "2. All users to resources, quotas, and payment" },
  "adminFunnel.lifecycle.body": {
    ru: "Единая схема: путь customer-пользователя, параллельные ветки каждого ресурса и отдельная billing-лента до оплаты. До Ready единица — пользователь; в оплате — организация. Ready — текущий срез, не дата первого успеха.",
    en: "One diagram: the customer-user path, parallel resource ribbons and a separate billing ribbon to payment. The unit is a user through Ready and an organization in billing. Ready is current state, not a first-success date.",
  },
  "adminFunnel.lifecycle.usersUnit": { ru: "уникальные пользователи", en: "unique users" },
  "adminFunnel.lifecycle.orgsUnit": { ru: "уникальные организации", en: "unique organizations" },
  "adminFunnel.lifecycle.accounts": { ru: "Все customer-аккаунты", en: "All customer accounts" },
  "adminFunnel.lifecycle.projects": { ru: "Создали проект", en: "Created a project" },
  "adminFunnel.lifecycle.requested": { ru: "Запросили ресурс", en: "Requested a resource" },
  "adminFunnel.lifecycle.ready": { ru: "Есть ресурс Ready сейчас", en: "Have a resource Ready now" },
  "adminFunnel.lifecycle.resourceOrgs": { ru: "Организации с ресурсом", en: "Organizations with a resource" },
  "adminFunnel.lifecycle.checkout": { ru: "Создали checkout", en: "Created a checkout" },
  "adminFunnel.lifecycle.paid": { ru: "Успешно оплатили", en: "Paid successfully" },
  "adminFunnel.lifecycle.resourcesTitle": { ru: "Какие ресурсы запросили и довели до Ready", en: "Resources requested and brought to Ready" },
  "adminFunnel.lifecycle.resourcesBody": {
    ru: "Это параллельные ветки: один пользователь может создавать несколько типов и несколько ресурсов. «Ready» — число пользователей с таким ресурсом в текущем рабочем состоянии.",
    en: "These are parallel branches: one user can create several types and resources. “Ready” is the number of users with that type currently usable.",
  },
  "adminFunnel.lifecycle.userStream": { ru: "Путь customer-пользователя", en: "Customer-user path" },
  "adminFunnel.lifecycle.billingStream": { ru: "Billing-организации", en: "Billing organizations" },
  "adminFunnel.lifecycle.resourcesOverlap": { ru: "ветви пересекаются", en: "branches overlap" },
  "adminFunnel.lifecycle.quotaUsersDetail": { ru: "реальные люди с отказом", en: "actual people with a refusal" },
  "adminFunnel.lifecycle.quotaAttemptsDetail": { ru: "отклонённые операции", en: "refused operations" },
  "adminFunnel.lifecycle.quotaGraceDetail": { ru: "разрешённое превышение", en: "allowed breach" },
  "adminFunnel.lifecycle.resourceUsers": { ru: "Запросили, чел.", en: "Requesting users" },
  "adminFunnel.lifecycle.resourceRequests": { ru: "Запросов", en: "Requests" },
  "adminFunnel.lifecycle.resourceReady": { ru: "Ready сейчас, чел.", en: "Ready users now" },
  "adminFunnel.resource.app": { ru: "App", en: "App" },
  "adminFunnel.resource.db": { ru: "БД", en: "DB" },
  "adminFunnel.resource.vm": { ru: "VM", en: "VM" },
  "adminFunnel.resource.box": { ru: "Box", en: "Box" },
  "adminFunnel.resource.s3": { ru: "S3", en: "S3" },
  "adminFunnel.lifecycle.quotaTitle": { ru: "Квотная ветка", en: "Quota branch" },
  "adminFunnel.lifecycle.quotaBody": {
    ru: "Отказы — реальные audit-события с причиной quota/consumption/storage exceeded. Grace показан отдельно: это разрешённое превышение, а не отказ.",
    en: "Refusals are actual audit events with quota/consumption/storage exceeded reasons. Grace is separate: it is an allowed breach, not a refusal.",
  },
  "adminFunnel.lifecycle.quotaUsers": { ru: "Пользователи с отказом", en: "Users with a refusal" },
  "adminFunnel.lifecycle.quotaAttempts": { ru: "Попытки, отклонённые квотой", en: "Attempts refused by quota" },
  "adminFunnel.lifecycle.quotaGrace": { ru: "Организации с grace-превышением", en: "Organizations with grace breach" },
  "adminFunnel.acquisition.evidence.title": { ru: "Проверка регистрации", en: "Registration evidence" },
  "adminFunnel.acquisition.evidence.body": {
    ru: "Санкей показывает веб-события Метрики. Ниже — независимая сверка с реальными аккаунтами в БД и способом регистрации.",
    en: "The Sankey shows Metrika web events. Below is an independent check against real DB accounts and the signup door.",
  },
  "adminFunnel.acquisition.dbRegistered": { ru: "Создано аккаунтов в БД", en: "Accounts created in DB" },
  "adminFunnel.acquisition.doors": { ru: "Известные двери регистрации", en: "Known signup doors" },
  "adminFunnel.acquisition.form": { ru: "Форма email/пароль", en: "Email/password form" },
  "adminFunnel.acquisition.formZero": {
    ru: "не использовалась: открыта только Яндекс ID",
    en: "not used: only Yandex ID is open",
  },
  "adminFunnel.acquisition.formOpened": { ru: "открыли: {count}", en: "opened: {count}" },
  "adminFunnel.acquisition.formErrors": { ru: "ошибок формы: {count}", en: "form errors: {count}" },

  "adminFunnel.product.title": { ru: "Активация: регистрация → готовый ресурс", en: "Activation: signup → ready resource" },
  "adminFunnel.product.body": {
    ru: "Вторая ступень — пользователи с доступным сейчас App, БД, VM, Box или S3. Это единственный доказуемый переход: данные не хранят время первой готовности для всех типов ресурсов.",
    en: "The second stage is users with a currently available App, DB, VM, Box, or S3. This is the only provable transition: the data does not store a first-ready timestamp for every resource type.",
  },
  "adminFunnel.product.notActivated": { ru: "Нет готового ресурса", en: "No ready resource" },
  "adminFunnel.product.paidAside": {
    ru: "Среди этой когорты успешную оплату имеют: {count}. Платёж сопоставлен по email и показан отдельно: порядок оплаты и готовности ресурса не известен.",
    en: "{count} people in this cohort have a successful payment. It is matched by email and shown separately: the order of payment and resource readiness is unknown.",
  },
  "adminFunnel.product.mix.title": { ru: "Что активировали", en: "What they activated" },
  "adminFunnel.product.mix.body": {
    ru: "Разрез ресурсов в когорте, не ступени: один пользователь может быть сразу в нескольких типах. Model показан по факту создания, потому что у него нет готовой фазы.",
    en: "Resource breakdown within the cohort, not stages: one user may have several types. Model is shown by row presence because it has no ready phase.",
  },

  "adminFunnel.cohort.empty": {
    ru: "За окно нет ни одной регистрации — воронке не из чего вырасти.",
    en: "No signups in this window — the funnel has nothing to grow from.",
  },
  "adminFunnel.sankey.dropAt": { ru: "Не дошли до «{stage}»", en: "Did not reach “{stage}”" },
  "adminFunnel.door.stageDoor": { ru: "Дверь", en: "Door" },
  "adminFunnel.door.stageRegistered": { ru: "Зарегистрировались", en: "Signed up" },
  "adminFunnel.reg.errorAside": { ru: "Ошибок формы за окно: {count}", en: "Form errors in the window: {count}" },

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
