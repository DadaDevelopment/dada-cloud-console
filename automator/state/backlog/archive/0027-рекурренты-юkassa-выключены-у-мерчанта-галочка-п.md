---
id: 0027
status: closed
prio: P0
title: РЕКУРРЕНТЫ ЮKASSA ВЫКЛЮЧЕНЫ У МЕРЧАНТА — ГАЛОЧКА «ПРОДЛЕВАТЬ АВТОМАТИЧЕСКИ» ОСТАЁТСЯ ЛОВУШКОЙ ДЛЯ ТОГО, КТО ЕЁ ПОСТАВИТ САМ — живо
created: 2026-08-16
sess: sess-0816c
section: Backlog (execution-bet)
closed_at: 2026-08-21
closed_commit: d1c18033
closed_note: Возможность рекуррентов объявляется конфигом YOOKASSA_RECURRING_ENABLED (default false): autopay.supported в /billing/account, консоль не рисует галочку и кнопку включения, бэкенд отказывает 422 до провайдера и до записи в БД; выключение и отвязка карты безусловны. Ловушка снята без owner-действия; включение рекуррентов у ЮMoney остаётся за владельцем.
---
- [ ] 🔴 РЕКУРРЕНТЫ ЮKASSA ВЫКЛЮЧЕНЫ У МЕРЧАНТА — ГАЛОЧКА «ПРОДЛЕВАТЬ АВТОМАТИЧЕСКИ» ОСТАЁТСЯ ЛОВУШКОЙ ДЛЯ ТОГО, КТО ЕЁ ПОСТАВИТ САМ (sess-0816c, 2026-08-16, [live psql `audit_events`], hypothesis: платить нечем ≠ незачем, origin/main@b49fe2a8) — живой ответ провайдера 2026-08-15 21:45:43 UTC по `artempro2021@bk.ru`: `403 forbidden: This store can't make recurring payments. Contact the YooMoney manager to learn more`, `error_class=yk_forbidden`, plan business 2900₽. Дефолт галочки снят и ошибка теперь человеческая (`b49fe2a8`), но включить рекурренты можно только на стороне ЮMoney → **owner-action**, см. `owner-actions.md`. До этого момента: либо владелец включает рекурренты, либо галочку надо убирать из интерфейса совсем — обещать то, чего магазин не умеет, нечестно.
  Файлы: `frontend/app/(console)/projects/[projectId]/billing/page.tsx:82`, `backend/internal/billing/yookassa/provider.go` (`ErrRecurringNotSupported`), `backend/internal/api/billing_payments.go` (422 `recurring_not_supported`).
