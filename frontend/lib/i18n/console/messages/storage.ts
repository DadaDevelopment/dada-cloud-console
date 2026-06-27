import type { Messages } from "./common";

/** Object Storage page — S3-compatible bucket management (storage.*). */
export const storage: Messages = {
  "storage.title": { ru: "Объектное хранилище", en: "Object Storage" },
  "storage.subtitle": { ru: "S3-совместимые бакеты Beget", en: "Beget S3-compatible storage buckets" },
  "storage.createBucket": { ru: "Создать бакет", en: "Create Bucket" },

  "storage.empty.title": { ru: "Пока нет бакетов", en: "No buckets yet" },
  "storage.empty.description": {
    ru: "Создайте S3-совместимый бакет для хранения файлов, медиа и статики — доступ через S3 API, FTP и SFTP.",
    en: "Create an S3-compatible bucket for files, media, and static assets — accessible via S3 API, FTP, and SFTP.",
  },
  "storage.empty.cta": { ru: "Создать первый бакет →", en: "Create your first bucket →" },

  "storage.badge.public": { ru: "Публичный", en: "Public" },
  "storage.badge.appRef": { ru: "приложение: {name}", en: "app: {name}" },
  "storage.badge.envLevel": { ru: "уровень окружения", en: "environment-level" },

  "storage.modal.title": { ru: "Создать S3-бакет", en: "Create S3 Bucket" },
  "storage.modal.resourceName": { ru: "Имя ресурса", en: "Resource Name" },
  "storage.modal.resourceNameSub": { ru: "(имя в Kubernetes)", en: "(Kubernetes name)" },
  "storage.modal.resourceNameTitle": {
    ru: "Только строчные буквы, цифры и дефисы",
    en: "Lowercase letters, numbers, and hyphens only",
  },
  "storage.modal.bucketName": { ru: "Имя бакета", en: "Bucket Name" },
  "storage.modal.region": { ru: "Регион", en: "Region" },
  "storage.modal.description": { ru: "Описание", en: "Description" },
  "storage.modal.descriptionPlaceholder": { ru: "Необязательно", en: "Optional" },
  "storage.modal.appRef": { ru: "Привязка к приложению", en: "App Reference" },
  "storage.modal.appRefSub": {
    ru: "(необязательно — оставьте пустым для бакета уровня окружения)",
    en: "(optional — leave empty for an environment-level bucket)",
  },
  "storage.modal.appRefTitle": {
    ru: "Только строчные буквы, цифры и дефисы",
    en: "Lowercase letters, numbers, and hyphens only",
  },
  "storage.modal.appRefHelp": {
    ru: "Привяжите бакет к чарту приложения — он будет создан и удалён вместе с приложением.",
    en: "Bind the bucket to an app's chart so it's reconciled and torn down with that app.",
  },

  "storage.toggle.public.label": { ru: "Публичный доступ", en: "Public Access" },
  "storage.toggle.public.description": {
    ru: "Объекты доступны по неподписанным URL",
    en: "Objects reachable via unsigned URLs",
  },
  "storage.toggle.ftp.label": { ru: "FTP/SFTP доступ", en: "FTP/SFTP Access" },
  "storage.toggle.ftp.description": {
    ru: "Включить протоколы FTP и SFTP",
    en: "Enable FTP and SFTP protocols",
  },

  "storage.error.load": { ru: "Не удалось загрузить бакеты", en: "Failed to load buckets" },
  "storage.error.create": { ru: "Не удалось создать бакет", en: "Failed to create bucket" },
};
