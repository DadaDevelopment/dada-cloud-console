import type { Messages } from "./common";

/** Databases list page + database detail page. Namespace: "databases.*" */
export const databases: Messages = {
  "databases.title": { ru: "Базы данных", en: "Databases" },
  "databases.subtitle": { ru: "Инстансы PostgreSQL", en: "PostgreSQL database instances" },
  "databases.createButton": { ru: "Создать базу данных", en: "Create Database" },

  "databases.empty.title": { ru: "Пока нет баз данных", en: "No databases yet" },
  "databases.empty.description": {
    ru: "Создайте PostgreSQL-инстанс и подключите его к приложению — строка подключения появится в переменных окружения автоматически.",
    en: "Create a PostgreSQL instance and connect it to your app — the connection string will appear in environment variables automatically.",
  },
  "databases.empty.createFirst": { ru: "Создать первую базу данных →", en: "Create your first database →" },
  "databases.empty.create": { ru: "Создать базу данных", en: "Create database" },
  "databases.empty.step1": { ru: "Создайте инстанс PostgreSQL в нужном окружении", en: "Create a PostgreSQL instance in an environment" },
  "databases.empty.step2": { ru: "Строка подключения появится в переменных окружения приложения", en: "The connection string appears in your app's environment variables" },
  "databases.empty.step3": { ru: "Приложение подключается к базе по имени сервиса", en: "Your app connects to it by service name" },

  "databases.card.noBackup": { ru: "нет бэкапа", en: "no backup" },
  "databases.card.backup": { ru: "бэкап {ago}", en: "backup {ago}" },
  "databases.card.size": { ru: "— размер", en: "— size" },
  "databases.card.synced": { ru: "Синхронизировано {ago}", en: "Synced {ago}" },

  "databases.error.load": { ru: "Не удалось загрузить базы данных", en: "Failed to load databases" },
  "databases.error.create": { ru: "Не удалось создать базу данных", en: "Failed to create database" },
  "databases.error.notFound": { ru: "База данных не найдена", en: "Database not found" },
  "databases.error.loadDetail": { ru: "Не удалось загрузить базу данных", en: "Failed to load database" },

  "databases.modal.title": { ru: "Создать базу данных", en: "Create Database" },
  "databases.modal.name.label": { ru: "Имя базы данных", en: "Database Name" },
  "databases.modal.name.hint": { ru: "(имя ресурса Kubernetes)", en: "(Kubernetes resource name)" },
  "databases.modal.name.validation": {
    ru: "Только строчные буквы, цифры и дефисы",
    en: "Lowercase letters, numbers, and hyphens only",
  },
  "databases.modal.pgName.label": { ru: "Имя базы PostgreSQL", en: "PostgreSQL DB Name" },
  "databases.modal.appRef.label": { ru: "Ссылка на приложение", en: "App Reference" },
  "databases.modal.appRef.hint": {
    ru: "(необязательно — оставьте пустым для базы данных уровня окружения)",
    en: "(optional — leave empty for an environment-level database)",
  },
  "databases.modal.appRef.help": {
    ru: "Привязать к чарту приложения или оставить пустым для самостоятельной базы данных.",
    en: "Bind to an app's chart, or leave empty to provision a standalone database your apps can reference.",
  },
  "databases.modal.backups.title": { ru: "Включить резервные копии", en: "Enable Backups" },
  "databases.modal.backups.subtitle": { ru: "Автоматические плановые резервные копии", en: "Automatic scheduled backups" },
  "databases.modal.schedule.label": { ru: "Расписание", en: "Backup Schedule" },
  "databases.modal.schedule.hourly": { ru: "Ежечасно", en: "Hourly" },
  "databases.modal.schedule.daily": { ru: "Ежедневно", en: "Daily" },
  "databases.modal.retention.label": { ru: "Хранение", en: "Retention" },
  "databases.modal.retention.7d": { ru: "7 дней", en: "7 days" },
  "databases.modal.retention.14d": { ru: "14 дней", en: "14 days" },
  "databases.modal.retention.30d": { ru: "30 дней", en: "30 days" },
  "databases.modal.submit": { ru: "Создать базу данных", en: "Create Database" },

  "databases.detail.subtitle": { ru: "База данных PostgreSQL", en: "PostgreSQL database" },
  "databases.detail.overview": { ru: "Обзор", en: "Overview" },
  "databases.detail.field.database": { ru: "База данных", en: "Database" },
  "databases.detail.field.attachedApp": { ru: "Привязанное приложение", en: "Attached app" },
  "databases.detail.field.environment": { ru: "Окружение", en: "Environment" },
  "databases.detail.field.status": { ru: "Статус", en: "Status" },
  "databases.detail.field.statusUnknown": { ru: "Неизвестно", en: "Unknown" },
  "databases.detail.connection": { ru: "Подключение", en: "Connection" },
  "databases.detail.field.host": { ru: "Хост (внутри кластера)", en: "Host (in-cluster)" },
  "databases.detail.field.dbName": { ru: "Имя базы данных", en: "Database name" },
  "databases.detail.field.port": { ru: "Порт", en: "Port" },
  "databases.detail.credentials": {
    ru: "Учётные данные предоставляются как секрет Kubernetes в пространстве имён приложения и никогда не отображаются здесь. Используйте стандартные переменные окружения секрета в вашем приложении.",
    en: "Credentials are provisioned as a Kubernetes secret in the app namespace and are never displayed here. Reference them in your app via the standard secret env vars.",
  },
  "databases.detail.backups": { ru: "Резервные копии", en: "Backups" },
  "databases.detail.backup.field.status": { ru: "Статус", en: "Status" },
  "databases.detail.backup.enabled": { ru: "Включено", en: "Enabled" },
  "databases.detail.backup.field.schedule": { ru: "Расписание", en: "Schedule" },
  "databases.detail.backup.field.retention": { ru: "Хранение", en: "Retention" },
  "databases.detail.backup.disabled": {
    ru: "Резервные копии отключены для этой базы данных.",
    en: "Backups are disabled for this database.",
  },

  "databases.delete.modal.title": { ru: "Удалить базу данных", en: "Delete database" },
  "databases.delete.modal.body": {
    ru: "Это необратимо удалит базу данных «{name}» и все её данные. Отменить это действие нельзя.",
    en: "This permanently deletes the database \"{name}\" and all its data. This action cannot be undone.",
  },
  "databases.delete.error": { ru: "Не удалось удалить базу данных", en: "Failed to delete database" },

  "databases.backups.title": { ru: "Резервные копии", en: "Backups" },
  "databases.backups.empty": { ru: "Резервных копий пока нет.", en: "No backups yet." },
  "databases.backups.createBtn": { ru: "Создать резервную копию", en: "Back up now" },
  "databases.backups.creating": { ru: "Создание…", en: "Creating…" },
  "databases.backups.column.created": { ru: "Создана", en: "Created" },
  "databases.backups.column.status": { ru: "Статус", en: "Status" },
  "databases.backups.column.kind": { ru: "Тип", en: "Kind" },
  "databases.backups.column.size": { ru: "Размер", en: "Size" },
  "databases.backups.restore": { ru: "Восстановить", en: "Restore" },
  "databases.backups.error": { ru: "Не удалось загрузить резервные копии", en: "Failed to load backups" },
  "databases.backups.createError": { ru: "Не удалось создать резервную копию", en: "Failed to create backup" },

  "databases.backups.status.ready": { ru: "Готова", en: "Ready" },
  "databases.backups.status.running": { ru: "Выполняется", en: "Running" },
  "databases.backups.status.pending": { ru: "В очереди", en: "Pending" },
  "databases.backups.status.failed": { ru: "Ошибка", en: "Failed" },
  "databases.backups.status.deleting": { ru: "Удаляется", en: "Deleting" },
  "databases.backups.status.deleted": { ru: "Удалена", en: "Deleted" },

  "databases.backups.kind.manual": { ru: "Вручную", en: "Manual" },
  "databases.backups.kind.scheduled": { ru: "По расписанию", en: "Scheduled" },
  "databases.backups.kind.preRestore": { ru: "Перед восстановлением", en: "Pre-restore" },

  "databases.backups.restoreModal.title": { ru: "Восстановить базу данных", en: "Restore database" },
  "databases.backups.restoreModal.body": {
    ru: "Это перезапишет текущую базу данных «{name}» содержимым выбранной резервной копии. Все текущие данные будут потеряны. Отменить это действие нельзя.",
    en: "This overwrites the current database \"{name}\" with the contents of the selected backup. All current data will be lost. This action cannot be undone.",
  },
  "databases.backups.restoreModal.confirmLabel": {
    ru: "Введите «{name}» для подтверждения",
    en: "Type {name} to confirm",
  },
  "databases.backups.restoreModal.mismatch": {
    ru: "Введённое имя не совпадает с именем базы данных",
    en: "The typed name doesn't match the database name",
  },
  "databases.backups.restoreModal.submit": { ru: "Восстановить", en: "Restore" },
  "databases.backups.restoreModal.restoring": { ru: "Восстановление…", en: "Restoring…" },
  "databases.backups.restoreError": { ru: "Не удалось запустить восстановление", en: "Failed to start restore" },
};
