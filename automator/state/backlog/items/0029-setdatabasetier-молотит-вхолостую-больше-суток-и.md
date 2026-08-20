---
id: 0029
status: open
prio: P1
hypothesis: platform-truth
title: SetDatabaseTier МОЛОТИТ ВХОЛОСТУЮ БОЛЬШЕ СУТОК И ФУНКЦИЯ НЕ РАБОТАЕТ НИ ДЛЯ ОДНОЙ БАЗЫ
created: 2026-08-16
sess: sess-0816a
section: Backlog (execution-bet)
---
- [ ] 🟠 `SetDatabaseTier` МОЛОТИТ ВХОЛОСТУЮ БОЛЬШЕ СУТОК И ФУНКЦИЯ НЕ РАБОТАЕТ НИ ДЛЯ ОДНОЙ БАЗЫ (sess-0816a, 2026-08-16, [live psql `audit_events` за 48ч], hypothesis: platform-truth, origin/main@8ef4b802) — за окно 178 `pending` / 167 `failure` / 11 `success`, актор system, непрерывно с 2026-08-14 13:55. Ошибка одна на всех: `no ServiceDatabaseV2 "<name>" anywhere under clusters/beget-prod/projects/.../environments/prod/apps` — джоба ГАДАЕТ путь к `resources.values.yaml` и не находит ресурс. Затронуты десятки баз во ВСЕХ наших внутренних проектах (`mlflow-v2`, `reels`, `user`, `jira-app`, `telemost-bot`, `nexus`, `mydatabase`, `zerkalo`, `dadagent`, `n8n`, `powerdns`). Юзерского аппа среди них НЕТ, поэтому это не №1, но по существу: авто-смена тира БД сейчас не работает нигде, а 167 подряд одинаковых `failure` никого не разбудили — это ещё и дыра в алертинге (провал системной джобы обязан гореть, а не копиться в аудите). Первый шаг: найти, откуда джоба берёт путь, и сверить с реальной раскладкой в argo-infra; чинить не путь у одной базы, а способ его получения.
  **Заземлено sess-0816b [code], НЕ БЕРИ КАК СВЕЖИЙ ПОЖАР:** штормом управлял `StartDBTierReconciler` из `1ab72b2e` (08-14 13:29), который на старте бэкенда гонит все `ServiceDatabaseV2` из `resource_snapshots` и строит путь ДОГАДКОЙ из `summary_json.spec.appRef` (`gitops-agent/internal/worker/db_values_locator.go:29-69`, ошибка на :68 — та самая строка из аудита). По этому уже отгружены три правки: `38da7e96` (exempt-орги), `04952aea` (fallback-скан + 6ч backoff), `634062e0` (пишем снапшот даже на no-op, чтобы тик не перезапрашивал). То есть 52/24ч — хвост ДО этих правок, а не текущий темп; **первым делом мерить сегодняшнее окно, а не чинить**. Настоящий остаток, если он ещё живой: путь берётся из соглашения, а не из записанной привязки база↔git — ср. [[project_db_app_binding_never_recorded_disarms_dsn_seeding]], тот же класс. Второй остаток независим от первого и не закрыт ничем: 167 одинаковых `failure` системной джобы никого не разбудили (`prometheusrule.yaml:15-26` — общий `DadaOperationFailed`, без порога на серию).
