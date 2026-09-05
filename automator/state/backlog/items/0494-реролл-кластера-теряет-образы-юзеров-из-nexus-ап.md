---
id: 0494
status: open
prio: P1
stream: 2
title: Реролл кластера теряет образы юзеров из nexus: апп мёртв до ре-пуша юзера
created: 2026-09-05
---
- Платформа потеряла образы юзеров в fleet-reroll 09-04 (nexus-данные
  восстановлены из бэкапа; пушы ПОСЛЕ точки бэкапа исчезли).
  gulyaev-ai-core: деплой указывает на digest, которого больше нет ->
  ImagePullBackOff; юзер мёртв до своего ре-пуша. Self-heal
  (platform_selfheal.go) лечит только известные закрытые сигнатуры кода,
  класса "образ потерян платформой" нет. Проблема НЕ юзерская - платформа
  потеряла его артефакт; честный фикс: детектор digest-missing ->
  автоматический rebuild из последнего коммита (или баннер 'образ потерян,
  пересобрать в 1 клик').
- Grounding: [live] kubectl describe pod lifecoachrussia-yandex-ru-prod
  gulyaev-ai-core-deploy-7f7d5bdfcc-q5mrc -> Failed pull digest d68666b3
  NotFound; [live] nexus API tags/list: max выживший main-b731 =
  digest 31fbe508 (билд 09-02 07:50); builds f34b532d/679337e3 (09-04)
  success, образы отсутствуют. app_health_alerts: уведомление ушло
  (last_send_ok=t).
