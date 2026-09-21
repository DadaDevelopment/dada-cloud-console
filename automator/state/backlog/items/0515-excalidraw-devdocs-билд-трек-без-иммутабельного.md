---
id: 0515
status: open
prio: P2
stream: 2
hypothesis: H02
title: excalidraw/devdocs: билд-трек без иммутабельного пина, тот же класс ротации что убил it-tools
created: 2026-09-21
sess: sess-0921a
---
[live registry 09-21] После перевода it-tools на образ на билд-треке остались 3 карточки:
excalidraw, gitingest, devdocs. Все собираются из апстрим-Dockerfile, который мы не контролируем,
и ломаются молча в день, когда апстрим-тулчейн уедет (механизм it-tools: незапиненный
`npm install -g pnpm` против lockfile pnpm@9).

Проверенные образы (манифест 200, порт из конфига образа):
- devdocs: ghcr.io/freecodecamp/devdocs:20260201, порт 9292 (НЕ 80), датированные иммутабельные теги - лучший кандидат
- excalidraw: docker.io/excalidraw/excalidraw, порт 80, semver-тегов НЕТ, только latest и sha-<commit>;
  тест catalog_test запрещает :latest, значит пинить sha-<commit>
- gitingest: см отдельный пункт (переезд организации)

Примечание: excalidraw сейчас единственная карточка, на которой держится
TestIsCatalogRepo - при переводе её на image-track тест надо перевести на другой
билд-трековый репозиторий, а сам репозиторий добавить в api.legacyDemoTemplateRepos,
иначе уже задеплоенные демо перестанут подхватываться жнецом (ровно эта ловушка
поймалась тестом в 556c2a8b).
