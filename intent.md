# Цель: воронка регистрации — детальная аналитика (kc-счётчик) + причина просевшей конверсии

Author: владелец (запрос в чате 2026-09-01). Status: in-progress (аналитика).
Предыдущий агент: собрал фактбазу (контекст в чате), не перепроверять — ДОПОЛНЯТЬ.

## Уточнение задачи (важно)
Владелец: «проблемы между началом и концом регистрации за последние 7 дней».
1) Детальная аналитика на основе Keycloak-счётчика (111697724, цели уже заведены).
2) Найти причину просевшей конверсии.

## Что известно от предыдущего агента (принято как данное, не перепроверено)
- Скриншот /admin/funnel с окном 30d: 50 → 41 → 6 (консольный счётчик 110158915).
- За 7 дней консольные цели = 0 (окно скриншота = 30d, не 7d).
- kc-цели 7d (у агента): kc_register_submit=3, kc_yandex_start=4.
- Коммит argo-infra c814c1bf (23.08, owner): registrationAllowed:true + кнопка
  «Регистрация» + kc_login_view/kc_native_registration_start цели, theme v19.
- С 13.08 единственная дверь была Яндекс-IDP; NEXT_PUBLIC_EMAIL_SIGNUP_ENABLED=false в прода.

## Мой прогресс (sess-0902, дополнение)
- [x] KC realm registrationAllowed=true ЗАПУЩЕНО В ПРОДЕ (kcadm live) — нативная форма открыта.
- [x] KC-цели 7d/30d сняты через Stat API (goal*users): login_view 21/23,
      native_reg_start 3/3, register_view 3/4, email_filled 3/3, password_filled 3/3,
      submit 3/3, register_error 0/1, yandex_start 3/5, yandex_registration_view 0/0,
      yandex_submit 0/0, login_submit 10/11.
- [x] PG ground truth 7d: 3 customer-аккаунта (2 password + 1 yandex), 14d: 6 (7 рядов окно 08-19..08-28).
      Zero-activity в 7d-когорте = 0 (все 3 что-то сделали).
- [x] KC events live (kcadm): REGISTER = 4 события (08-26 m206rv159, 08-26 shrunk@waifu.club,
      08-28 kof97zip, 08-28 messiajit4). REGISTER_ERROR = 6 (08-27 12:54..13:11, пустые details).
      IDP_FIRST_LOGIN_ERROR = 2 (08-26 07:37). LOGIN_ERROR 70 — почти весь шум (admin-cli 29,
      mcp/harness, dada-console 15).
- [x] ux_events 30d: signup_started=173 (66 уник), но ПОСЛЕДНИЙ = 08-23. В окне 7 дней — НОЛЬ.
      registration_complete 30d = 6 (6 уник) — метч скриншоту. callback_failed 30d = 36.
- [x] Консольный счётчик: 7d ВСЕ цели = 0 (кроме callback_failed=3). 30d: 50/41/6/10.
- [x] /login → /callback (ux pageviews, 7d): 29 уников на /login, дошли до /callback только 4,
      при этом все 29 с user_id — то есть это ВОЗВРАЩАЮЩИЕСЯ, не новые. Анон на /login 7d = 0!
- [x] /login pageview по дням 14d: 239→хвост 8-9/день к 08-29+, спад объёма к 09-01 (2).
- [x] kc-счётчик посещаемость 14д: всего 89 визитов (Link 57 / Direct 32), 1 юзер 09-01.
- [x] Деплой коммита c814c1bf в KC: theme v19 в values.yaml того же коммита,
      keycloak-config-prod Argo Synced/Healthy; registrationAllowed=true подтверждён live.

## Отекшая конверсия — ЧТО ВИДИМ
- «Начало регистрации» 30d-скриншота = signup_started (СТАРЫЙ goal, стрелял на /register-экране выбора,
  а /register теперь 302→/login). После 23.08 цель физически не стреляет (страницы больше нет).
- Реальный путь сейчас: лендинг → /login (KC) → Яндекс-кнопка или «Регистрация» → KC-формы → callback.
- kc-топ: login_view 21 уник/7д, из них start native reg 3, yandex start 3, submit 3.
  Значит ~15/21 даже не кликают «Регистрация» — это в основном СУЩЕСТВУЮЩИЕ юзеры логинятся.
- Регистраций 7d = 3, все 3 дошли до submit и аккаунт создан → в воротах формы НЕ текут.
- ТЕЧЁТ ВЕРХ: почти нет НОВЫХ людей на /login (анон-визиты 7d = 0 в ux_events;
  kc 14д = 89 визитов, из них 57 линк + 32 директ — это возвраты и перелёты).

## Root-cause (рабочая версия, основания выше)
1) Главный «просевший» кусок = АРТЕФАКТ ИНСТРУМЕНТИРОВАНИЯ: signup_started (41)
   принадлежит мёртвому экрану /register (выбор способа), убитому 302-редиректом.
   50→41 — «начали регистрацию» не было вообще у новых юзеров после 23.08; цель не стреляет.
2) Реальная воронка 7д = KC-счётчик: 21 открыли вход → 6 нажали хоть что-то регистрационное →
   3 сабмита → 3 аккаунта. На шаге «вход→клик регистрация» теряется большинство —
   потому что на /login приходят существующие юзеры, а новых почти нет (верх воронки пуст).
3) callback_failed 30d=36 (state_entry=missing) — oidc-сташ в localStorage между табами;
   7d=3. Мелкий, но реальный течь на возврате.

## Дизайн бэклог-фикса (из контекста агента, продолжаю)
- Backend: новый adminKcFunnelReport (admin_kc_funnel.go) + поле kc_funnel в adminFunnelResponse;
  fetchMetrikaGoalUsers (users, не reaches); переиспользовать overviewRegisteredCount/Channels;
  переписать admin_registration_funnel.go (мёртвый overviewRegistrationFunnel), сохранив
  metrikaStatHTTPClient/metrikaStatTimeout (нужны admin_funnel.go + тесты).
- Frontend: тип AdminFunnelKcFunnel, стрим «id.dada-tuda.ru (Keycloak)» в Sankey /admin/funnel,
  i18n ru/en. Убрать AdminOverviewRegistrationFunnel из types.ts (мёртвый).
- Цели ног: yandex 601042593..97, native 598690125..144/161, login 601095017/601095084/601095085.

## Открытые вопросы
- Зачем KC REGISTER_ERROR 08-27 (6 подряд) — details пустые; возможно кто-то долбил форму.
  Некритично (0 в последние дни), в отчёт как шум.
- shrunk@waifu.club зарегистрирован в KC 08-26, но в user_accounts нет → залогинился,
  консоль-аккаунт не создан (lazy provisioning не сработал = не делал запросов). Учесть в отчёте.
