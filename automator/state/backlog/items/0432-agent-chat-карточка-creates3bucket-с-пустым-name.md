---
id: 0432
status: open
prio: P1
stream: 3
hypothesis: H08
title: Agent-chat: карточка createS3Bucket с пустым name одобряется и мгновенно ловит 400 missing_name
created: 2026-08-21
sess: sess-0821g
locked_by: sess-0821g
locked_until: 2026-08-21T14:16Z
---
Разбор аудита sess-0821g [live psql + code].

`michaelharlam@yandex.ru` (живой, сессии идут прямо сейчас) 2026-08-20 10:44:06.327
одобрил в agent-chat действие `createS3Bucket` с args
`{"bucket_name":"dating-service-assets","public":true}` -- поле `name` ПУСТОЕ.
Через 11 миллисекунд `CreateS3Bucket` вернул FAILURE
`{"reason":"missing_name","status":400}`. После этого 25.5 часов, 9 сессий подряд,
только `ViewProject`/`ViewApps` -- ни одного write-действия.

Тот же `missing_name` уже стрелял 2026-08-04, но тогда его поймал внутренний
тестовый аккаунт (`michaelharlam@dada-tuda.ru`), и он пролежал 16 дней.
За 30 дней `CreateS3Bucket`: 8 success / 2 failure, оба failure -- этот класс.

Корень двойной [code]:
1. `backend/internal/api/s3buckets.go:229-238` -- жёсткий reject `missing_name`
   при `Name==""`, без фолбэка на `BucketName`, хотя `BucketName` заполнен и
   из него выводится корректное kube-имя.
2. `backend/internal/api/agent_chat.go:648-651` -- текст карточки строится как
   `fmt.Sprintf("Create an S3 storage bucket %q (bucket=%q", name, bucketName)`,
   то есть при пустом `name` юзеру показывается `бакет ""` и кнопка «одобрить»
   остаётся активной. Фронт (`frontend/components/agent-chat-panel.tsx`) не
   валидирует обязательные поля карточки перед approve.

Что сделать (обе стороны, иначе дыра остаётся):
- бэк: вывести `name` из `bucket_name` (и симметрично) когда одно из двух пусто,
  с сохранением `validateKubeName`; 400 оставить только когда вывести нечего.
- фронт: не пускать approve на карточке с пустым обязательным аргументом,
  показывать чего не хватает.

Приёмка двухполюсная: RED -- запрос с пустым `name` и заполненным `bucket_name`
даёт 400 `missing_name`; GREEN -- даёт 202 и созданный бакет с выведенным именем.
Плюс обратный полюс: запрос, где пусты ОБА поля, обязан по-прежнему давать 400.
