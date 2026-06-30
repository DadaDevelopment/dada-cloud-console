import type { Messages } from "./common";

/** Monitoring page + create-monitoring modal + onboarding card. */
export const monitoring: Messages = {
  "monitoring.title": { ru: "Мониторинг", en: "Monitoring" },
  "monitoring.subtitle": {
    ru: "Передавайте телеметрию с любого устройства через OpenTelemetry — дашборды и алерты уже включены.",
    en: "Push telemetry from any device via OpenTelemetry — dashboards and alerts included.",
  },

  "monitoring.create": { ru: "Создать мониторинг", en: "Create Monitoring" },

  "monitoring.modal.title": { ru: "Создать приложение мониторинга", en: "Create Monitoring App" },
  "monitoring.modal.name.label": { ru: "Название", en: "Name" },
  "monitoring.modal.name.placeholder": { ru: "my-service-monitor", en: "my-service-monitor" },
  "monitoring.modal.name.validation": {
    ru: "Только строчные буквы, цифры и дефисы",
    en: "Lowercase letters, numbers, and hyphens only",
  },

  "monitoring.error.load": {
    ru: "Не удалось загрузить приложения мониторинга",
    en: "Failed to load monitoring apps",
  },
  "monitoring.error.create": {
    ru: "Не удалось создать приложение мониторинга",
    en: "Failed to create monitoring app",
  },

  "monitoring.status.checking": { ru: "Проверка…", en: "Checking…" },
  "monitoring.status.receiving": { ru: "Получение данных", en: "Receiving" },
  "monitoring.status.waiting": { ru: "Ожидание первой телеметрии", en: "Waiting for first telemetry" },
  "monitoring.status.readyToReceive": { ru: "готово к приёму телеметрии", en: "ready to receive telemetry" },

  "monitoring.onboarding.label": { ru: "Начало работы", en: "Getting started" },
  "monitoring.dismiss": { ru: "Скрыть", en: "Dismiss" },

  "monitoring.step1.title": { ru: "Создать ресурс мониторинга", en: "Create monitoring resource" },
  "monitoring.step1.body": {
    ru: "Ресурс {name} создан.",
    en: "Resource {name} created.",
  },

  "monitoring.step2.title": { ru: "Скопируйте ваш API key", en: "Copy your API key" },
  "monitoring.step2.keyStored": { ru: "Ключ уже сохранён.", en: "Key was already stored." },
  "monitoring.step2.warning": {
    ru: "Сохраните этот ключ сейчас — он больше не будет показан.",
    en: "Store this key now — it will not be shown again.",
  },
  "monitoring.step2.needRotate": { ru: "Нужно сменить ключ?", en: "Need to rotate?" },
  "monitoring.step2.manageKeys": { ru: "Управление ключами →", en: "Manage keys →" },

  "monitoring.step3.title": { ru: "Подключите ваше устройство", en: "Connect your device" },
  "monitoring.step3.body": {
    ru: "Используйте OTel SDK с указанным эндпоинтом и ключом, или задайте переменные окружения для инструментирования без кода.",
    en: "Use the OTel SDK with the endpoint and key below, or set env vars for zero-code instrumentation.",
  },
  "monitoring.card.createdAt": { ru: "Создано {date}", en: "Created {date}" },
  "monitoring.card.monitoringApp": { ru: "приложение мониторинга", en: "monitoring app" },

  "monitoring.zero.title": { ru: "Начните получать телеметрию", en: "Start receiving telemetry" },
  "monitoring.zero.body": {
    ru: "Создайте ресурс мониторинга, скопируйте API key и отправляйте метрики или логи с любого устройства, используя стандартный OpenTelemetry SDK.",
    en: "Create a monitoring resource, copy your API key, and push metrics or logs from any device using the stock OpenTelemetry SDK.",
  },
  "monitoring.zero.createBtn": { ru: "Создать приложение мониторинга", en: "Create Monitoring App" },

  "monitoring.zero.step1": {
    ru: "Создайте ресурс мониторинга — он получит API key с ограниченным доступом.",
    en: "Create a monitoring resource — gets a scoped API key.",
  },
  "monitoring.zero.step2": {
    ru: "Скопируйте ключ (показывается один раз) и сохраните его в безопасном месте.",
    en: "Copy the key (shown once) and store it securely.",
  },
  "monitoring.zero.step3": {
    ru: "Направьте ваш OTel SDK на эндпоинт приёма — живой дашборд появится через секунды.",
    en: "Point your OTel SDK at the ingest endpoint — live dashboard in seconds.",
  },

  "monitoring.detail.notFound": { ru: "Приложение мониторинга не найдено", en: "Monitoring app not found" },
  "monitoring.detail.missingEnv": { ru: "Отсутствует envId", en: "Missing envId" },
  "monitoring.detail.error.loadApp": {
    ru: "Не удалось загрузить приложение мониторинга",
    en: "Failed to load monitoring app",
  },

  "monitoring.detail.openInGrafana": { ru: "Открыть в Grafana", en: "Open in Grafana" },

  "monitoring.detail.tab.overview": { ru: "Обзор", en: "Overview" },
  "monitoring.detail.tab.dashboard": { ru: "Дашборд", en: "Dashboard" },
  "monitoring.detail.tab.metrics": { ru: "Метрики", en: "Metrics" },
  "monitoring.detail.tab.logs": { ru: "Логи", en: "Logs" },
  "monitoring.detail.tab.alerts": { ru: "Алерты", en: "Alerts" },

  "monitoring.detail.health.title": { ru: "Здоровье", en: "Health" },
  "monitoring.detail.health.state": { ru: "Состояние", en: "State" },
  "monitoring.detail.health.lastSeen": { ru: "Последнее получение", en: "Last seen" },
  "monitoring.detail.health.errorRate": { ru: "Частота ошибок (15м)", en: "Error rate (15m)" },
  "monitoring.detail.health.firingAlerts": { ru: "Активных алертов", en: "Firing alerts" },
  "monitoring.detail.health.reasons": { ru: "Причины", en: "Reasons" },

  "monitoring.detail.info.title": { ru: "Информация", en: "Info" },
  "monitoring.detail.info.id": { ru: "ID", en: "ID" },
  "monitoring.detail.info.created": { ru: "Создано", en: "Created" },
  "monitoring.detail.info.updated": { ru: "Обновлено", en: "Updated" },
  "monitoring.detail.info.grafanaUid": { ru: "Grafana dashboard UID", en: "Grafana dashboard UID" },

  "monitoring.detail.grafana.blocked": {
    ru: "Дашборд заблокирован политикой безопасности браузера — откройте напрямую.",
    en: "Dashboard blocked by browser security policy — open it directly.",
  },
  "monitoring.detail.grafana.hint": {
    ru: "Живой Grafana дашборд. Если панель пустая — нужно включить встраивание Grafana на сервере.",
    en: "Live Grafana dashboard. If the panel is blank, Grafana embedding may need to be enabled on the server.",
  },
  "monitoring.detail.grafana.unavailable": { ru: "Ссылка на Grafana недоступна", en: "Grafana link unavailable" },
  "monitoring.detail.grafana.notProvisioned": {
    ru: "Дашборд Grafana ещё не подготовлен. Убедитесь, что у приложения мониторинга установлен grafana_dashboard_uid.",
    en: "The Grafana dashboard may not have been provisioned yet. Check that the monitoring app has a grafana_dashboard_uid set.",
  },
  "monitoring.detail.grafana.error.load": { ru: "Не удалось получить ссылку на Grafana", en: "Failed to get Grafana link" },

  "monitoring.detail.alerts.section": { ru: "Правила алертов", en: "Alert Rules" },
  "monitoring.detail.alerts.createRule": { ru: "Создать правило", en: "Create Rule" },
  "monitoring.detail.alerts.empty": { ru: "Правила алертов не настроены.", en: "No alert rules configured." },
  "monitoring.detail.alerts.col.name": { ru: "Название", en: "Name" },
  "monitoring.detail.alerts.col.metric": { ru: "Метрика", en: "Metric" },
  "monitoring.detail.alerts.col.condition": { ru: "Условие", en: "Condition" },
  "monitoring.detail.alerts.col.duration": { ru: "Длительность", en: "Duration" },
  "monitoring.detail.alerts.col.channel": { ru: "Канал", en: "Channel" },
  "monitoring.detail.alerts.deleteRule": { ru: "Удалить", en: "Delete" },

  "monitoring.detail.channels.section": { ru: "Каналы", en: "Channels" },
  "monitoring.detail.channels.add": { ru: "Добавить канал", en: "Add Channel" },
  "monitoring.detail.channels.empty": { ru: "Каналы уведомлений не настроены.", en: "No notification channels configured." },
  "monitoring.detail.channels.col.name": { ru: "Название", en: "Name" },
  "monitoring.detail.channels.col.type": { ru: "Тип", en: "Type" },
  "monitoring.detail.channels.col.created": { ru: "Создано", en: "Created" },
  "monitoring.detail.channels.delete": { ru: "Удалить", en: "Delete" },

  "monitoring.detail.modal.createRule.title": { ru: "Создать правило алерта", en: "Create Alert Rule" },
  "monitoring.detail.modal.rule.name": { ru: "Название", en: "Name" },
  "monitoring.detail.modal.rule.metric": { ru: "Метрика", en: "Metric" },
  "monitoring.detail.modal.rule.metricCustom": { ru: "Название пользовательской метрики", en: "Custom metric name" },
  "monitoring.detail.modal.rule.condition": { ru: "Условие", en: "Condition" },
  "monitoring.detail.modal.rule.threshold": { ru: "Порог", en: "Threshold" },
  "monitoring.detail.modal.rule.duration": { ru: "Длительность", en: "Duration" },
  "monitoring.detail.modal.rule.channel": { ru: "Канал", en: "Channel" },
  "monitoring.detail.modal.rule.channelNone": { ru: "— нет —", en: "— none —" },
  "monitoring.detail.modal.rule.submitLabel": { ru: "Создать правило", en: "Create Rule" },
  "monitoring.detail.modal.rule.working": { ru: "Выполняется...", en: "Working..." },
  "monitoring.detail.modal.rule.error": { ru: "Не удалось создать правило", en: "Failed to create rule" },
  "monitoring.detail.modal.rule.error.load": { ru: "Не удалось загрузить алерты", en: "Failed to load alerts" },

  "monitoring.detail.modal.addChannel.title": { ru: "Добавить канал", en: "Add Channel" },
  "monitoring.detail.modal.channel.name": { ru: "Название", en: "Name" },
  "monitoring.detail.modal.channel.type": { ru: "Тип", en: "Type" },
  "monitoring.detail.modal.channel.botToken": { ru: "Bot Token", en: "Bot Token" },
  "monitoring.detail.modal.channel.chatId": { ru: "Chat ID", en: "Chat ID" },
  "monitoring.detail.modal.channel.emailAddresses": { ru: "Email-адреса", en: "Email addresses" },
  "monitoring.detail.modal.channel.emailHint": { ru: "(через запятую)", en: "(comma-separated)" },
  "monitoring.detail.modal.channel.webhookUrl": { ru: "Webhook URL", en: "Webhook URL" },
  "monitoring.detail.modal.channel.submitLabel": { ru: "Добавить канал", en: "Add Channel" },
  "monitoring.detail.modal.channel.error": { ru: "Не удалось создать канал", en: "Failed to create channel" },
};
