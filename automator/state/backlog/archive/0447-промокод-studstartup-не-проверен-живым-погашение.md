---
id: 0447
status: closed
prio: P1
title: промокод STUDSTARTUP не проверен живым погашением на проде
created: 2026-08-21
sess: sess-0821f
closed_at: 2026-08-21
closed_note: Проверено живым погашением на проде 2026-08-21 [live]. Образ 659e9f53, под Ready. Фаза A: 404 promo_code_not_found -- роут и таблица promo_codes доехали. Фаза B в орге dada: погашение 200 {applied:true,days:30}, plan_expires_at 2026-08-24 -> 2026-09-23, повтор 409 promo_already_redeemed (отказ по коду, не по тексту). Песочница откачена полностью: план startup, срок 2026-08-24, promo_redemptions 0, redeemed_count 0/2000 -- тест не попал в счётчик кампании.
---
Владелец пообещал промокод в переговорах о рекламе в чате «Студенческий стартап» (2802
участника) РАНЬШЕ, чем фича существовала: grep `promo_code|promocode|coupon` по
`backend/internal` и `frontend` давал ноль совпадений.

Рычаг строится в sess-0821f. Пост нельзя публиковать, пока `STUDSTARTUP` не погашен реальным
аккаунтом на ЖИВОМ проде — мерж и даже зелёный CI не есть доставка.

Проверка: погасить код тестовым аккаунтом в `agent-sandbox`, убедиться что
`billing_accounts.plan_expires_at` сдвинулся и что повторное погашение тем же оргом отбито.

Черновик поста: `automator/state/growth/ad-post-studstartup.md`
