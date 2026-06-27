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
};
