---
id: 0516
status: closed
prio: P0
stream: 2
hypothesis: H02
title: fqdn_taken 409 - тупик: юзер aria не может поднять свой домен после успешной верификации
created: 2026-09-23
sess: sess-0923a
closed_at: 2026-09-23
closed_commit: 1dc89631
closed_note: CreateEndpoint получил anti-hijack гейт requireVerifiedApex (403 no_verified_apex) - премьерный мёртвый endpoint на неподтверждённый домен больше не создаётся; reject() отдаёт машинный code, консоль классифицирует 409 fqdn_taken / 403 no_verified_apex и ведёт ссылкой на страницу Домены вместо сырой английской фразы. go build+vet чисто, 4 новых go-теста RUN+PASS, tsc 0 / test:unit 515 (было 508) / lint 0
---
[live psql 09-23, audit_events юзера drariaatashin@gmail.com, рег 2026-09-22 08:23]
Самый вовлечённый новичок окна (80 audit-строк за один час) прошёл ВЕСЬ поток 1 успешно
(upload архива 08:25 -> билд success 08:27 -> апп aria живой на aria-f9b214.dada-tuda.ru)
и УПЁРСЯ НАСМЕРТЬ на своём домене. Последние три действия его сессии:
CreatePublicApi ariatala.ariaatashin.ir -> failure reason=fqdn_taken status=409, ТРИ РАЗА
(08:49:12, 08:49:21, 08:49:53), после чего тишина. В 10:09 вернулся, посмотрел ViewApps/
ViewProject и ушёл. Домен до сих пор не работает.

Порядок событий, из которого родился тупик:
- 08:34/08:35 CreatePublicApi ariatala.ariaatashin.ir -> success. ДО всякой авторизации домена.
  Снапшот PublicApi 'ariatala-ariaatashin-ir' создан, dns.target=159.194.204.57.
- 08:36 AddDomainAuthorization -> 08:36..08:41 VerifyDomainAuthorization failure x53
  (reason: DNS lookup failed ... no such host) -> 08:41:18 SUCCESS, домен верифицирован.
- 08:49 юзер вернулся сделать endpoint "как надо" (теперь домен подтверждён) -> 409 fqdn_taken.

Дыра механизма [code backend/internal/api/endpoints.go:225-237]: uniqueness-проверка
считает ЛЮБОЙ существующий снапшот PublicApi с тем же именем конфликтом и отдаёт голый
409 "a domain with that FQDN already exists in this environment". Проверка не различает
"занято ЧУЖИМ" и "это ваш же собственный endpoint, созданный 14 минут назад до верификации".
Во втором случае единственное осмысленное поведение - не отказ, а путь вперёд.

Что чинить (продукт, не сообщение):
1. 409 перестаёт быть тупиком: при конфликте с СВОИМ же endpoint в этом же проекте/окружении
   ответ несёт признак own_endpoint + имя ресурса, а UI ведёт на существующий endpoint
   ("он у вас уже есть") вместо повтора формы, которая гарантированно падает.
2. Разобраться, живой ли endpoint, созданный ДО верификации домена: если авторизация,
   став verified, не пере-применяет уже созданный PublicApi, то юзер получает мёртвый
   endpoint и НИКАКОГО способа его оживить через продукт (пересоздать нельзя - 409).
