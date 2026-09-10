---
id: 0498
status: closed
prio: P1
stream: 1
hypothesis: H11
title: framework_undetected на zip/folder-upload не даёт пути вперёд новичку-вайбкодеру
created: 2026-09-10
sess: sess-0910a
closed_at: 2026-09-10
closed_commit: 86d64424
closed_note: static-детект + генерация Dockerfile на upload-пути: архив с index.html и без манифеста теперь собирается и отдаётся nginx (live: docker build + curl 200), а не падает framework_undetected
---
Наблюдение [live]: юзер y4ndex.danila@yandex.ru (рег 09-09 12:03) первым действием загрузил архив (UploadSourceArchive, app smirad) - билд упал framework_undetected: нет package.json/requirements.txt/pyproject.toml/go.mod/pom.xml/build.gradle и нет Dockerfile. Единственный путь сейчас = юзер сам угадывает, что добавить.

Идея (поток 1, H11): при framework_undetected для upload-путей предлагать авто-обёртку: (1) если в архиве только статика (html/css/js) - сгенерить nginx/static-server конфиг и собрать; (2) иначе - показать выбор стека с генерацией минимального Dockerfile от имени платформы (по образцу build-agent dadaBuildPipeline). UX: не голый error, а карточка "мы не узнали стек - вот 2 пути".

Файлы: build-agent детект стека (framework_undetected - где error_message формируется), frontend страница билда (показ ошибки -> CTA). Осторожно с полюсом: не маскировать честную ошибку, предлагать опцию.

Гипотеза-основание: поток 1 = единственный измеренно-работающий активационный механизм; danila дошёл до upload за 76с и упёрся здесь. Это текущий leak первого действия.
