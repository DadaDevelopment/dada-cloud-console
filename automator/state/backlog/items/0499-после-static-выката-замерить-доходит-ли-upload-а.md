---
id: 0499
status: open
prio: P1
stream: 1
hypothesis: H11
title: После static-выката: замерить, доходит ли upload-архив без манифеста до живого URL
created: 2026-09-10
sess: sess-0910b
---
Отгружено 86d64424: Detect распознаёт статику (index.html без манифестов), UploadSourceArchive вписывает сгенерённый Dockerfile (nginx:1.27-alpine, EXPOSE 80) прямо в архив, web-root становится корнем контекста.

M2 в цикле закрыт локально: docker build отгруженного архива + curl 200 на index.html и style.css. НО живого прохода через прод-Jenkins ещё не было - на момент выката ни одного нового upload со статикой.

Замерить (source_of_truth = live psql):
1) builds где archive_framework='static' - появились ли вообще (знаменатель!);
2) их статус: succeeded vs failed, и если failed - fail_reason (ожидание: framework_undetected по этому классу больше не появляется);
3) дошёл ли хоть один такой билд до живого URL (apps-строка + HandoffDeploy).
Ловушка: ноль строк скорее всего значит 'никто не грузил статику за окно', а НЕ 'фича не работает' - сначала снимать знаменатель.
