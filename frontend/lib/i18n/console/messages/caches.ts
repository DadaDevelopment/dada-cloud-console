import type { Messages } from "./common";

/** Redis caches list page + cache detail page. Namespace: "caches.*" */
export const caches: Messages = {
  "caches.title": { ru: "Redis", en: "Redis" },
  "caches.subtitle": { ru: "Управляемые пользователи Redis ACL", en: "Managed Redis ACL users" },
  "caches.createButton": { ru: "Создать пользователя Redis", en: "Create Redis user" },

  "caches.empty.title": { ru: "Пока нет пользователей Redis", en: "No Redis users yet" },
  "caches.empty.description": {
    ru: "Создайте пользователя Redis с ограниченным профилем прав и привяжите его к приложению — учётные данные появятся в переменных окружения автоматически.",
    en: "Create a scoped Redis ACL user and bind it to an app — its credentials appear in environment variables automatically.",
  },
  "caches.empty.create": { ru: "Создать пользователя Redis", en: "Create Redis user" },
  "caches.empty.step1": { ru: "Создайте пользователя Redis с выбранным профилем прав", en: "Create a Redis user with a capability profile" },
  "caches.empty.step2": { ru: "Учётные данные появятся в переменных окружения приложения", en: "Credentials appear in your app's environment variables" },
  "caches.empty.step3": { ru: "Приложение подключается к общему инстансу Redis по имени сервиса", en: "Your app connects to the shared Redis instance by service name" },

  "caches.card.synced": { ru: "Синхронизировано {ago}", en: "Synced {ago}" },

  "caches.error.load": { ru: "Не удалось загрузить пользователей Redis", en: "Failed to load Redis users" },
  "caches.error.create": { ru: "Не удалось создать пользователя Redis", en: "Failed to create Redis user" },
  "caches.error.notFound": { ru: "Пользователь Redis не найден", en: "Redis user not found" },
  "caches.error.loadDetail": { ru: "Не удалось загрузить пользователя Redis", en: "Failed to load Redis user" },

  "caches.modal.title": { ru: "Создать пользователя Redis", en: "Create Redis user" },
  "caches.modal.name.label": { ru: "Имя ресурса", en: "Resource name" },
  "caches.modal.name.hint": { ru: "(сгенерировано автоматически, можно изменить)", en: "(auto-generated, you can edit it)" },
  "caches.modal.name.validation": {
    ru: "Только строчные буквы, цифры и дефисы",
    en: "Lowercase letters, numbers, and hyphens only",
  },
  "caches.modal.appRef.label": { ru: "Приложение", en: "Application" },
  "caches.modal.appRef.auto": { ru: "— (определить автоматически)", en: "— (auto-detect)" },
  "caches.modal.appRef.optionalHint": {
    ru: "Необязательно. Если не выбрать — Redis создастся как самостоятельный ресурс окружения, как база данных без привязки к приложению.",
    en: "Optional. Leave unset to create a standalone, environment-level Redis resource, the same way an unbound database works.",
  },
  "caches.modal.keyPrefix.label": { ru: "Префикс ключей", en: "Key prefix" },
  "caches.modal.keyPrefix.hint": {
    ru: "Необязательно, по умолчанию — имя ресурса. Доступ ограничивается ключами с этим префиксом.",
    en: "Optional, defaults to the resource name. Access is scoped to keys under this prefix.",
  },
  "caches.modal.profile.label": { ru: "Профиль прав", en: "Capability profile" },
  "caches.modal.profile.hint": {
    ru: "«Полный доступ» — как своя база данных: всё, кроме администрирования сервера. Остальные профили — для точечного ограничения доступа.",
    en: "\"Full access\" behaves like your own database: everything except server administration. The other profiles narrow access for specific workloads.",
  },
  "caches.modal.profile.full-access": { ru: "Полный доступ (рекомендуется)", en: "Full access (recommended)" },
  "caches.modal.profile.kv-readonly": { ru: "Ключ-значение: только чтение", en: "Key-value: read-only" },
  "caches.modal.profile.kv-readwrite": { ru: "Ключ-значение: чтение и запись", en: "Key-value: read-write" },
  "caches.modal.profile.stream-producer": { ru: "Поток: producer (XADD)", en: "Stream: producer (XADD)" },
  "caches.modal.profile.stream-consumer": { ru: "Поток: consumer group (XREADGROUP/XACK)", en: "Stream: consumer group (XREADGROUP/XACK)" },
  "caches.modal.profile.stream-admin": { ru: "Поток: администрирование (XGROUP)", en: "Stream: admin (XGROUP)" },
  "caches.modal.profile.list-producer": { ru: "Список: producer (LPUSH/RPUSH)", en: "List: producer (LPUSH/RPUSH)" },
  "caches.modal.profile.list-consumer": { ru: "Список: consumer (BLPOP/BRPOP)", en: "List: consumer (BLPOP/BRPOP)" },
  "caches.modal.advanced.show": { ru: "Дополнительные настройки", en: "Advanced settings" },
  "caches.modal.advanced.hide": { ru: "Скрыть дополнительные настройки", en: "Hide advanced settings" },
  "caches.modal.submit": { ru: "Создать", en: "Create" },

  "caches.detail.subtitle": { ru: "Пользователь Redis ACL", en: "Redis ACL user" },
  "caches.detail.overview": { ru: "Обзор", en: "Overview" },
  "caches.detail.field.attachedApp": { ru: "Привязанное приложение", en: "Attached app" },
  "caches.detail.field.keyPrefix": { ru: "Префикс ключей", en: "Key prefix" },
  "caches.detail.field.profile": { ru: "Профиль прав", en: "Capability profile" },
  "caches.detail.field.environment": { ru: "Окружение", en: "Environment" },
  "caches.detail.field.status": { ru: "Статус", en: "Status" },
  "caches.detail.field.statusUnknown": { ru: "Неизвестно", en: "Unknown" },
  "caches.detail.connection": { ru: "Подключение", en: "Connection" },
  "caches.detail.field.host": { ru: "Хост (внутри платформы)", en: "Host (internal)" },
  "caches.detail.field.port": { ru: "Порт", en: "Port" },
  "caches.detail.credentials": {
    ru: "Учётные данные хранятся как секрет в пространстве имён приложения. Приложение получает их через стандартные переменные окружения секрета -- либо покажите их разово ниже.",
    en: "Credentials are stored as a secret in the app namespace. Your app references them via the standard secret env vars -- or reveal them once below.",
  },
  "caches.detail.hostHidden": { ru: "Скрыт -- покажите учётные данные", en: "Hidden -- reveal credentials" },
  "caches.detail.hostHint": {
    ru: "Точные хост и порт берутся из секрета -- покажите учётные данные ниже.",
    en: "The exact host and port come from the secret -- reveal credentials below.",
  },
  "caches.detail.access.username": { ru: "Пользователь", en: "Username" },
  "caches.detail.access.password": { ru: "Пароль", en: "Password" },
  "caches.detail.access.reveal": { ru: "Показать", en: "Reveal" },
  "caches.detail.access.hide": { ru: "Скрыть", en: "Hide" },
  "caches.detail.access.revealBtn": { ru: "Показать учётные данные", en: "Reveal credentials" },
  "caches.detail.access.revealing": { ru: "Загрузка...", en: "Revealing..." },
  "caches.detail.access.notReady": {
    ru: "Учётные данные пока недоступны -- пользователь ещё создаётся.",
    en: "Credentials are not available yet -- the user is still provisioning.",
  },
  "caches.detail.access.notReadyExhausted": {
    ru: "Учётные данные так и не появились за {minutes} мин -- пользователь всё ещё создаётся. Попробуйте показать их ещё раз чуть позже.",
    en: "Credentials did not appear within {minutes} min -- the user is still provisioning. Try revealing them again a little later.",
  },
  "caches.detail.access.notConfigured": {
    ru: "Показ учётных данных не настроен для этого окружения.",
    en: "Credential reveal is not configured for this environment.",
  },
  "caches.detail.access.error": { ru: "Не удалось показать учётные данные", en: "Failed to reveal credentials" },
  "caches.detail.access.dsn": { ru: "Строка подключения", en: "Connection string" },
  "caches.detail.access.dsnHint": {
    ru: "Скопируйте целиком и положите в переменную окружения приложения (обычно REDIS_URL).",
    en: "Copy the whole string into your app's environment variable (usually REDIS_URL).",
  },

  "caches.delete.modal.title": { ru: "Удалить пользователя Redis", en: "Delete Redis user" },
  "caches.delete.modal.body": {
    ru: "Пользователь «{name}» будет отключён от проекта: доступ и учётные данные перестанут работать, он пропадёт из консоли. Вернуть его самостоятельно нельзя.",
    en: "The user \"{name}\" will be detached from the project: access and credentials stop working, and it disappears from the console. You cannot re-attach it yourself.",
  },
  "caches.delete.modal.confirmLabel": { ru: "Введите «{name}», чтобы подтвердить", en: "Type \"{name}\" to confirm" },
  "caches.delete.modal.mismatch": { ru: "Имя не совпадает", en: "Name does not match" },
  "caches.delete.error": { ru: "Не удалось удалить пользователя Redis", en: "Failed to delete Redis user" },
};
