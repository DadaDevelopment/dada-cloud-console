---
id: 0441
status: open
prio: P1
stream: 2
title: Сборка юзера падает с "queue item no longer known to jenkins" -- три билда у двух живых юзеров за 48ч
created: 2026-08-21
sess: sess-0821d
---
Замечено в пульсе sess-0821d [live psql `builds`, окно 48ч].

Три отказа сборки с текстом `queue item no longer known to jenkins`:
`sevarateambot` x2 (`bruzas.85@mail.ru`), `megafactory` x1. Это НАШ отказ, не юзерский:
репозиторий и код юзера ни при чём, задача просто пропала из очереди Jenkins.

Отличать от соседнего класса в том же окне: `best-marriage-astrologer-in-guwahati` x3
падает на `npm install` -- вот это юзерское, трогать не надо.

Смежная память: `project_jenkins_cloud_container_cap_starves_our_builds` (потолок
контейнеров облака морит наши сборки) и
`project_jenkins_agent_pod_killed_midbuild_reads_as_publish_failure`. Сначала проверить,
не тот же ли это механизм, прежде чем заводить новый диагноз.

Почему это болит: юзер видит красную сборку и читает её как СВОЮ ошибку. Ни ретрая, ни
объяснения продукт не даёт. Заземлить числом: доля билдов с этой сигнатурой за 30 дней и
сколько РАЗНЫХ живых юзеров задето.
