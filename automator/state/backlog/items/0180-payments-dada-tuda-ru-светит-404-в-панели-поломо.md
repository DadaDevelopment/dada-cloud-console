---
id: 0180
status: open
prio: P2
title: payments.dada-tuda.ru СВЕТИТ 404 В ПАНЕЛИ ПОЛОМОК, НО ДЕНЕГ НЕ ТЕРЯЕТ
created: 2026-08-11
sess: sess-0811f
section: 🔴 P0 08-15 (sess-0815b) — «ПОСЛЕДНЯЯ МИЛЯ» ОТДАЁТ ЛОЖНО-ЗЕЛЁНОЕ: 17/17 ok ПРИ ТР
---
- [ ] 🟡 `payments.dada-tuda.ru` СВЕТИТ 404 В ПАНЕЛИ ПОЛОМОК, НО ДЕНЕГ НЕ ТЕРЯЕТ (sess-0811f, 2026-08-11, [live kubectl + psql + code]) — запись `not_ready_other` в `/api/v1/admin/overview`: DNS указывает на ingress, Ingress-правила нет, `probe-external.sh` → 404 на 6/6 узлов. ПАНИКА «теряем платежи» НЕ ПОДТВЕРЖДЕНА: webhook ЮKassa висит на консольном роутере `POST /api/v1/webhooks/yookassa` (`backend/internal/api/router.go:244`), return_url = `https://console.dada-tuda.ru/billing/return` по дефолту (`backend/internal/config/config.go:594,870`, `YOOKASSA_RETURN_URL` в env не задан) — оба на живом хосте. `payments.dada-tuda.ru` — отдельный `PublicApi` CR под будущую фичу «мерчант подключает свой ЮKassa», upstream `serviceName: gateway` в нужном namespace НЕ СУЩЕСТВУЕТ (единственный `gateway` в кластере — Kasten K10 backup gateway в ns `databases`), таблицы `payment_connections`/`payment_oauth_states`/`pay_service_keys`/`service_charges` = 0 строк, фичей никто не пользовался. Платежей всего 1 за всю историю (`succeeded`, 2026-07-25), зависших pending = 0. Решение НЕ «починить Ingress», а выбрать: убрать запись `payments` из `argo-infra/.../crossplane-platform-api/chart/values.yaml:96-118` вместе с DNS, либо доделать upstream. Пока запись жива, она занимает место в панели поломок и приучает читать её как шум.
