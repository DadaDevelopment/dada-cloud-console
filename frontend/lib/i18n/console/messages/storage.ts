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
  "storage.empty.create": { ru: "Создать бакет", en: "Create bucket" },
  "storage.empty.step1": { ru: "Создайте бакет в нужном окружении", en: "Create a bucket in an environment" },
  "storage.empty.step2": { ru: "Получите ключи доступа S3 — access key и secret", en: "Get S3 access keys — access key and secret" },
  "storage.empty.step3": { ru: "Подключайтесь через S3 API, FTP или SFTP", en: "Connect via S3 API, FTP or SFTP" },

  "storage.badge.public": { ru: "Публичный", en: "Public" },
  "storage.badge.appRef": { ru: "приложение: {name}", en: "app: {name}" },
  "storage.badge.envLevel": { ru: "уровень окружения", en: "environment-level" },

  "storage.list.provisionError.title": { ru: "Провижининг не удался", en: "Provisioning failed" },
  "storage.list.provisionError.hint": {
    ru: "Чаще всего дело в описании: провайдер принимает не больше 45 символов и не всю пунктуацию. Описание уже созданного бакета изменить нельзя — создайте бакет заново с коротким описанием без знаков препинания.",
    en: "Usually the description: the provider accepts at most 45 characters and rejects some punctuation. An existing bucket's description cannot be edited — create the bucket again with a short, punctuation-free description.",
  },

  "storage.modal.title": { ru: "Создать S3-бакет", en: "Create S3 Bucket" },
  "storage.modal.resourceName": { ru: "Имя ресурса", en: "Resource Name" },
  "storage.modal.resourceNameSub": { ru: "(строчные буквы, цифры и дефисы)", en: "(lowercase letters, digits and hyphens)" },
  "storage.modal.resourceNameTitle": {
    ru: "Только строчные буквы, цифры и дефисы",
    en: "Lowercase letters, numbers, and hyphens only",
  },
  "storage.modal.bucketName": { ru: "Имя бакета", en: "Bucket Name" },
  "storage.modal.region": { ru: "Регион", en: "Region" },
  "storage.modal.description": { ru: "Описание", en: "Description" },
  "storage.modal.descriptionPlaceholder": { ru: "Необязательно", en: "Optional" },
  "storage.modal.description.hint": {
    ru: "Буквы, цифры, пробел и . , _ - Максимум {max} символов ({len}/{max})",
    en: "Letters, digits, spaces, and . , _ - only. Max {max} characters ({len}/{max})",
  },
  "storage.modal.description.invalidChar": {
    ru: "Символ «{char}» недопустим — разрешены буквы, цифры, пробел и . , _ -",
    en: "Character \"{char}\" isn't allowed — only letters, digits, spaces, and . , _ - are permitted",
  },
  "storage.modal.description.tooLong": {
    ru: "Слишком длинное описание — максимум {max} символов ({len}/{max})",
    en: "Description is too long — max {max} characters ({len}/{max})",
  },
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

  "storage.detail.notFound": { ru: "Бакет не найден", en: "Bucket not found" },
  "storage.detail.overview": { ru: "Обзор", en: "Overview" },
  "storage.detail.field.bucket": { ru: "Имя бакета", en: "Bucket name" },
  "storage.detail.field.region": { ru: "Регион", en: "Region" },
  "storage.detail.field.visibility": { ru: "Доступ", en: "Visibility" },
  "storage.detail.field.ftp": { ru: "FTP/SFTP", en: "FTP/SFTP" },
  "storage.detail.field.appRef": { ru: "Приложение", en: "App" },
  "storage.detail.visibility.public": { ru: "Публичный", en: "Public" },
  "storage.detail.visibility.private": { ru: "Приватный", en: "Private" },
  "storage.detail.on": { ru: "Включено", en: "Enabled" },
  "storage.detail.off": { ru: "Выключено", en: "Disabled" },
  "storage.detail.envLevel": { ru: "уровень окружения", en: "environment-level" },

  "storage.detail.access.title": { ru: "Доступ по S3", en: "S3 access" },
  "storage.detail.access.endpoint": { ru: "Эндпоинт", en: "Endpoint" },
  "storage.detail.access.accessKey": { ru: "Access key", en: "Access key" },
  "storage.detail.access.secretKey": { ru: "Secret key", en: "Secret key" },
  "storage.detail.access.reveal": { ru: "Показать", en: "Reveal" },
  "storage.detail.access.hide": { ru: "Скрыть", en: "Hide" },
  "storage.detail.access.none": {
    ru: "Ключи доступа выдаются вместе с бакетом и не отображаются в консоли. Добавьте их в приложение как переменные окружения. В примере ниже подставьте свои значения.",
    en: "Access keys are issued with the bucket and are not surfaced in the console. Add them to your app as environment variables — substitute your own values in the example below.",
  },
  "storage.detail.access.revealBtn": { ru: "Показать учётные данные", en: "Reveal credentials" },
  "storage.detail.access.revealing": { ru: "Загрузка…", en: "Revealing…" },
  "storage.detail.access.notReady": {
    ru: "Учётные данные пока недоступны — бакет ещё создаётся.",
    en: "Credentials aren't available yet — the bucket is still provisioning.",
  },
  "storage.detail.access.waiting": {
    ru: "Бакет ещё создаётся на стороне провайдера — ждём и проверяем сами каждые 15 секунд, страницу обновлять не нужно. Ждём уже {min} мин; у Beget это иногда занимает больше часа.",
    en: "The provider is still building the bucket — we keep checking every 15 seconds, no need to refresh. Waiting {min} min so far; with Beget this sometimes takes over an hour.",
  },
  "storage.detail.access.slowTitle": { ru: "Это дольше обычного", en: "This is taking longer than usual" },
  "storage.detail.access.slowHint": {
    ru: "Известный успешный случай ждал 80 минут — если счётчик уже перевалил за это, скорее всего дело в описании: провайдер Beget принимает не больше 45 символов и не всю пунктуацию, а провал обычно тихий. Описание уже созданного бакета не поменять. Если через пару часов доступа всё ещё нет, создайте отдельный бакет под другим именем с коротким описанием без знаков препинания, а про застрявший напишите нам — удалить его из консоли пока нельзя.",
    en: "The longest known successful case waited 80 minutes — past that, the usual culprit is the description: Beget's provider accepts at most 45 characters and rejects some punctuation, and the failure is silent. An existing bucket's description can't be edited. If there's still no access after a couple more hours, create a separate bucket under a different name with a short, punctuation-free description, and tell us about the stuck one — the console can't delete it yet.",
  },
  "storage.detail.access.notConfigured": {
    ru: "Доступ к учётным данным не настроен для этого окружения.",
    en: "Credential access isn't configured for this environment.",
  },
  "storage.detail.access.error": { ru: "Не удалось показать учётные данные", en: "Failed to reveal credentials" },
  "storage.detail.access.failedTitle": { ru: "Провижининг бакета не удался", en: "Bucket provisioning failed" },
  "storage.detail.access.failedHint": {
    ru: "Чаще всего дело в описании: провайдер принимает не больше 45 символов и не всю пунктуацию. Описание уже созданного бакета изменить нельзя — создайте бакет заново с коротким описанием без знаков препинания.",
    en: "Usually the description: the provider accepts at most 45 characters and rejects some punctuation. An existing bucket's description cannot be edited — create the bucket again with a short, punctuation-free description.",
  },

  "storage.detail.cli.title": { ru: "Пример aws-cli", en: "aws-cli example" },
  "storage.detail.cli.hint": {
    ru: "S3-совместимый API. Замените плейсхолдеры на свои значения.",
    en: "S3-compatible API. Replace the placeholders with your own values.",
  },
};
