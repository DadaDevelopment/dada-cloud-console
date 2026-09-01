---
id: 0455
status: closed
prio: P0
stream: 2
hypothesis: H02
title: pickCause берёт трейлер npm «см. лог-файл» вместо реальной причины падения
created: 2026-08-21
sess: sess-0821h
closed_at: 2026-08-21
closed_commit: 7e324db5
closed_note: pickCause больше не отдаёт npm-трейлер "A complete log of this run can be found in" вместо причины: фильтр поднят в isAdvisoryLine (покрывает и npm error, и npm ERR!). RED-доказательство мутацией печатает вербатим строку, которую видел живой юзер. Замер E190, measure_after 2026-09-04.
---
Заземлено sess-0821h [live psql + code].

**Что видел юзер.** `tarotreaderhimu@gmail.com`, апп `best-marriage-astrologer-in-guwahati`,
3 билда 13:58-14:09 UTC 08-21, все три с ОДНИМ текстом (менялся только таймстемп):
`[build 5/6] RUN npm install: npm error A complete log of this run can be found in:
/root/.npm/_logs/<ts>-debug-0.log`. После третьей попытки ушёл.

**Что система знала.** В `builds_logs` всех трёх билдов (билд №1 — 507 строк) лежит настоящая
причина: `npm error code ENOENT` / `npm error enoent Could not read package.json: Error:
ENOENT: no such file or directory, open '/app/package.json'`. То есть `package.json` не в том
месте, где его ждёт сборка. Ни ретрай, ни заведение БД (он завёл) этого не чинят.

**Корень [code].** `build-agent/internal/worker/runner.go:495-513` `pickCause` идёт по выводу
упавшего шага НАЗАД и возвращает ПОСЛЕДНЮЮ строку, матчнувшую
`causeErrorRe = (?i)\b(error|fatal|panic|cannot)\b` (`runner.go:414`). npm всегда печатает
свой трейлер `npm error A complete log of this run can be found in: ...` последним, и в нём
есть слово «error» — поэтому трейлер СТРУКТУРНО побеждает реальную строку причины, стоящую
выше в том же блоке. `isAdvisoryLine` (`runner.go:517`) уже отсекает `[notice]`/`npm notice`,
но про этот трейлер не знает. Дальше `failureMessageAndReason` (`runner.go:1865`) пишет
результат прямо в `error_message`, фронт (`frontend/lib/build-failure.ts:15-19`,
страница билда `builds/[buildId]/page.tsx:433-443`) отдаёт его дословно.

**Масштаб.** Не про одного юзера: промах срабатывает на КАЖДОМ падении `npm install`,
дошедшем до этого пути. Терминальные точки графа аудита за 60д: `ViewApps` 5 + `TriggerBuild` 4
из 43 акторов = **56%** сдавшихся уходят именно на провале сборки.

**Что НЕ переделывать.** `50e773cd` (уже в проде) закрыл смежное: `repeat_count` нормализует
пер-прогонный таймстемп, и с второго повтора фронт говорит «ретрай не поможет, откройте лог,
смотрите манифест зависимостей». Это правильный патч, но он посылает читать лог, чей
ЗАГОЛОВОК и есть шум. Правка нужна одна и в одной функции, новый UI не нужен.

M2: `error_message` билда, упавшего на `npm install` без `package.json`, содержит подстроку
`ENOENT` / `Could not read package.json`, а НЕ `A complete log of this run can be found in`.
RED обязан быть мутационным: возврат старого поведения роняет тест дословно.
