---
id: 0376
status: closed
prio: P0
stream: 3
hypothesis: H08
title: TriggerAutofix вернул success, а апп единственного нового юзера так и лежит в CrashLoop
created: 2026-08-19
sess: sess-0820a
closed_at: 2026-08-19
closed_commit: 0901f914
closed_note: TriggerAutofix пишет pending на запуск; терминальный вердикт applied/no_change/failed пишется в ResolveAutofix тем же стейтментом, что и улика (pr_url/status/reason), через уже существующий вебхук DadaAgent. Аудит отвязан от исправности email-notifier. Real-DB тест TestRecordAutofixResolution_NoChangeIsQueryableSeparatelyFromApplied прогнан на риге — PASS. НЕ закрыто: связь вердикта со здоровьем пода.
---
**Заземлено [live psql audit_events + live kubectl], 2026-08-19/20.**

Единственный signup окна — `lifecoachrussia@yandex.ru` (`d229ec6b`, 09:37:20Z). Полный путь из
`audit_events`:

```
09:37:20  SignUp → success
09:37:20  SessionStart → success
09:37:20  CreateProject pending → 09:39:31 success
09:38:13–33  StartGitAppInstall / FinishGitAppInstall (github) → success
09:39:31  ConnectGitRepo gulyaev-ai-core → success
09:39:31–09:41:48  TriggerBuild → BuildFinished FAILURE ×4 подряд
09:41:19  TriggerAutofix → success   ← ВРАНЬЁ
11:26:50  BuildFinished success, CreateApp success   (спустя 1ч49м борьбы)
```

Терминальное действие = `CreateApp` 11:26:50. Дальше ~12 часов тишины: ни билда, ни логина, ни
фидбека (перепроверено по `builds`, `git_repos`, `agent_chat_messages`, `feedback` — не «дыра в
инструментировании», а реальный ноль).

Приз за 1ч49м борьбы: апп `gulyaev-ai-core` в CrashLoopBackOff, 117 рестартов, причина в первой же
строке лога — `ModuleNotFoundError: No module named 'app'`.

**Дефект.** `TriggerAutofix` записал `success`, хотя под продолжил падать с той же ошибкой. Это тот
же класс, что `project_autofix_button_had_no_cause` — платформа врёт про саму себя. `success` у
авто-фикса обязан означать «апп поднялся», а не «агент отработал без исключения».

**Плюс: сигнала о смерти нет вообще.** Единственный апп нового юзера мёртв, и об этом знает только
`not_ready` в админ-панели. Юзеру никто не сказал. Silent death = churn — ровно то, на чём мы уже
теряли artem дважды.

Работа: (1) вердикт авто-фикса брать из состояния пода ПОСЛЕ попытки, а не из завершения агента;
(2) когда авто-фикс не смог — сказать юзеру честно, с причиной и рычагом, а не молча оставить
`success`.
