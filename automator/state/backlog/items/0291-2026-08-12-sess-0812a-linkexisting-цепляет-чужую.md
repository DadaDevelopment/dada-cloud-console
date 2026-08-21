---
id: 0291
status: open
prio: P2
title: 2026-08-12 sess-0812a · 🟡 linkExisting ЦЕПЛЯЕТ ЧУЖУЮ IDENTITY ПО СОВПАДЕНИЮ EMAIL
created: 2026-08-12
sess: sess-0812a
section: Открытые долги (не терять)
---
- [ ] 2026-08-12 sess-0812a · 🟡 `linkExisting` ЦЕПЛЯЕТ ЧУЖУЮ IDENTITY ПО СОВПАДЕНИЮ EMAIL [code, origin/main@70c826b2] — `backend/internal/auth/provision.go` в ОБОИХ путях (открытая рега: 23505-фоллбек :123-133; закрытая: `resolveExistingOnly` :161-170) делает `UPDATE users SET keycloak_sub=$1 WHERE username=$3 OR email=$4`. Значит новый Keycloak-sub с чужим email садится на существующую строку юзера и получает его проекты. Не регрессия (в открытом пути так было всегда), но с закрытой регой это единственный способ вообще войти незнакомцу. Чинить: линковать только когда у строки `keycloak_sub IS NULL` (легаси-строка без SSO) И email верифицирован (`email_verified` в claims). Замер: сколько строк `users` c непустым `keycloak_sub` меняли sub — сейчас нет журнала, добавить аудит-строку на link.
