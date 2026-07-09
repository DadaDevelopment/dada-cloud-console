import type { Messages } from "./common";

/** AI Models list page (models/page.tsx) + model detail page (models/[name]/page.tsx). */
export const models: Messages = {
  "models.title": { ru: "AI-модели", en: "AI Models" },
  "models.subtitle": { ru: "Сервисы инференса на базе KServe", en: "KServe-backed inference services" },
  "models.deploy": { ru: "Развернуть модель", en: "Deploy Model" },

  "models.quota.cpuModels": { ru: "CPU-модели", en: "CPU models" },
  "models.quota.gpuModels": { ru: "GPU-модели", en: "GPU models" },
  "models.quota.inferenceCalls": { ru: "Вызовы инференса / месяц", en: "Inference calls / month" },
  "models.quota.advisory": { ru: "(рекомендательно)", en: "(advisory)" },

  "models.empty.title": { ru: "Нет моделей в {env}", en: "No models in {env}" },
  "models.empty.deploy": { ru: "Развернуть первую модель →", en: "Deploy your first model →" },

  "models.card.synced": { ru: "Синхронизировано {ago}", en: "Synced {ago}" },

  "models.error.load": { ru: "Не удалось загрузить модели", en: "Failed to load models" },
  "models.error.create": { ru: "Не удалось создать модель", en: "Failed to create model" },

  "models.modal.title": { ru: "Развернуть AI-модель", en: "Deploy AI Model" },

  "models.form.name.label": { ru: "Имя (имя ресурса k8s)", en: "Name (k8s resource name)" },
  "models.form.modelType.label": { ru: "Тип модели", en: "Model type" },
  "models.form.source.label": { ru: "Источник", en: "Source" },
  "models.form.source.s3": { ru: "S3 артефакт URI", en: "S3 artifact URI" },
  "models.form.source.mlflow": { ru: "Зарегистрированная модель MLflow", en: "MLflow registered model" },
  "models.form.source.custom": { ru: "Пользовательский образ контейнера", en: "Custom container image" },

  "models.form.artifactUri.label": { ru: "Artifact URI", en: "Artifact URI" },
  "models.form.artifactUri.help": {
    ru: "Должен начинаться с префикса хранилища этого проекта.",
    en: "Must start with this project's storage prefix.",
  },

  "models.form.mlflowName.label": { ru: "Зарегистрированное имя", en: "Registered name" },
  "models.form.mlflowVersion.label": { ru: "Версия", en: "Version" },

  "models.form.containerImage.label": { ru: "Образ контейнера", en: "Container image" },

  "models.form.profile.label": { ru: "Профиль вычислений", en: "Compute profile" },
  "models.form.profile.gpuApproval": {
    ru: "ⓘ GPU-профиль требует подтверждения администратора — операция будет поставлена в WaitingForApproval.",
    en: "ⓘ GPU profile requires admin approval — this op will be parked in WaitingForApproval.",
  },
  "models.form.profile.gpuApprovalLink": { ru: "Открыть очередь подтверждений", en: "Open the approvals queue" },

  "models.form.authMode.label": { ru: "Режим аутентификации", en: "Auth mode" },
  "models.form.authMode.apikey": { ru: "api-key (по умолчанию)", en: "api-key (default)" },
  "models.form.authMode.jwt": { ru: "platform-jwt", en: "platform-jwt" },
  "models.form.authMode.public": { ru: "public (только администратор)", en: "public (admin only)" },

  "models.form.attachedApp.label": { ru: "Привязанное приложение", en: "Attached app" },
  "models.form.versionLabel.label": { ru: "Метка версии", en: "Version label" },

  "models.form.deploy": { ru: "Развернуть", en: "Deploy" },
  "models.form.deploying": { ru: "Развёртывание…", en: "Deploying…" },

  "models.detail.notFound": { ru: "Модель не найдена", en: "Model not found" },
  "models.detail.error.load": { ru: "Не удалось загрузить модель", en: "Failed to load model" },

  "models.tab.overview": { ru: "Обзор", en: "Overview" },
  "models.tab.versions": { ru: "Версии", en: "Versions" },
  "models.tab.access": { ru: "Доступ", en: "Access" },
  "models.tab.playground": { ru: "Площадка", en: "Playground" },
  "models.tab.operations": { ru: "Операции", en: "Operations" },
  "models.tab.manifests": { ru: "Манифесты", en: "Manifests" },

  "models.detail.setCanary": { ru: "Установить canary", en: "Set canary" },
  "models.detail.promote": { ru: "Продвинуть", en: "Promote" },

  "models.canary.modal.title": { ru: "Установить canary-трафик", en: "Set canary traffic" },
  "models.canary.label": {
    ru: "Процент canary-трафика {pct}%",
    en: "Canary traffic percent {pct}%",
  },
  "models.canary.help": {
    ru: "0% откатывает до стабильной версии. 100% равнозначно Продвижению.",
    en: "0% rolls back to stable. 100% is equivalent to Promote.",
  },
  "models.canary.error": { ru: "Не удалось установить canary", en: "Failed to set canary" },

  "models.promote.modal.title": { ru: "Продвинуть canary", en: "Promote canary" },
  "models.promote.body": {
    ru: "Продвигает canary-ревизию до 100% стабильного трафика. Предыдущая стабильная ревизия становится неактивной.",
    en: "Promotes the canary revision to 100% stable traffic. The previous stable revision becomes inactive.",
  },
  "models.promote.error": { ru: "Не удалось продвинуть", en: "Failed to promote" },
  "models.promote.submit": { ru: "Продвинуть", en: "Promote" },

  "models.delete.modal.title": { ru: "Удалить модель", en: "Delete model" },
  "models.delete.body": {
    ru: "Это удаляет манифест AIModel из Git. KServe деинициализирует InferenceService при следующей синхронизации Argo. Привязанное приложение нужно отвязать, если не установлен параметр force.",
    en: "This removes the AIModel manifest from Git. KServe will deprovision the InferenceService on the next Argo sync. Any attached App will need to be detached unless force is set.",
  },
  "models.delete.force": {
    ru: "Force — удалить даже если привязано к приложению (аудируется).",
    en: "Force — delete even if attached to an App (audited).",
  },
  "models.delete.error": { ru: "Не удалось удалить", en: "Failed to delete" },

  "models.working": { ru: "Выполняется…", en: "Working…" },

  "models.overview.source": { ru: "Источник", en: "Source" },
  "models.overview.traffic": { ru: "Трафик", en: "Traffic" },
  "models.overview.status": { ru: "Статус", en: "Status" },

  "models.overview.specCard.modelType": { ru: "Тип модели", en: "Model type" },
  "models.overview.specCard.profile": { ru: "Профиль", en: "Profile" },
  "models.overview.specCard.authMode": { ru: "Режим аутентификации", en: "Auth mode" },
  "models.overview.specCard.stage": { ru: "Стадия", en: "Stage" },

  "models.overview.row.artifactUri": { ru: "Artifact URI", en: "Artifact URI" },
  "models.overview.row.containerImage": { ru: "Образ контейнера", en: "Container image" },
  "models.overview.row.mlflow": { ru: "MLflow", en: "MLflow" },
  "models.overview.row.attachedApp": { ru: "Привязанное приложение", en: "Attached app" },
  "models.overview.row.phase": { ru: "Фаза", en: "Phase" },
  "models.overview.row.lastSynced": { ru: "Последняя синхронизация", en: "Last synced" },
  "models.overview.row.status": { ru: "Статус", en: "Status" },
  "models.overview.row.apiKeyPrefix": { ru: "Префикс API-ключа", en: "API key prefix" },

  "models.overview.traffic.canary": { ru: "canary → новая ревизия", en: "canary → new revision" },
  "models.overview.traffic.canaryHelp": {
    ru: "Продвижение переключает 100% на canary; Установить canary обновляет разделение или откатывает до 0%.",
    en: "Promote shifts 100% to canary; Set canary updates the split or rolls back to 0%.",
  },
  "models.overview.traffic.stable": { ru: "100% стабильный — canary не активен.", en: "100% stable — no canary active." },

  "models.versions.updateArtifact": { ru: "Обновить артефакт", en: "Update artifact" },
  "models.versions.artifactUri.label": { ru: "Новый artifact URI", en: "New artifact URI" },
  "models.versions.artifactUri.help": {
    ru: "Должен начинаться с префикса хранилища этого проекта.",
    en: "Must start with this project's storage prefix.",
  },
  "models.versions.mlflowName.label": { ru: "Имя MLflow", en: "MLflow name" },
  "models.versions.mlflowVersion.label": { ru: "Версия", en: "Version" },
  "models.versions.updateBtn": { ru: "Обновить артефакт", en: "Update artifact" },
  "models.versions.updating": { ru: "Обновление…", en: "Updating…" },
  "models.versions.error": { ru: "Не удалось обновить", en: "Failed to update" },
  "models.versions.history": { ru: "История версий", en: "Version history" },
  "models.versions.noHistory": { ru: "Изменений версий ещё не зарегистрировано.", en: "No version-changing operations recorded yet." },

  "models.access.auth": { ru: "Аутентификация", en: "Auth" },
  "models.access.noApiKey": {
    ru: "API-ключ не выдан (auth_mode не является apikey).",
    en: "No API key issued (auth_mode is not apikey).",
  },
  "models.access.reveal.title": { ru: "Показать API-ключ (одноразово)", en: "Reveal API key (one-shot)" },
  "models.access.reveal.body": {
    ru: "Открытый ключ хранится в 15-минутной строке Postgres, привязанной к операции создания. Нажмите Показать, чтобы использовать строку — после этого ключ не может быть восстановлен. Смените ключ для получения нового.",
    en: "The plaintext key is parked in a 15-minute Postgres row keyed on the create operation. Click Reveal to consume the row — the key cannot be recovered after that. Rotate to issue a new one.",
  },
  "models.access.reveal.btn": { ru: "Показать API-ключ", en: "Reveal API key" },
  "models.access.reveal.revealing": { ru: "Загрузка…", en: "Revealing..." },
  "models.access.reveal.save": { ru: "Сохраните сейчас — ключ больше не будет показан.", en: "Save this now — it will not be shown again." },
  "models.access.reveal.error": { ru: "Показ не удался (окно могло истечь)", en: "Reveal failed (window may have expired)" },

  "models.ops.empty": { ru: "Операций для этой модели ещё нет.", en: "No operations recorded for this model yet." },
  "models.ops.col.action": { ru: "Действие", en: "Action" },
  "models.ops.col.status": { ru: "Статус", en: "Status" },
  "models.ops.col.when": { ru: "Когда", en: "When" },
  "models.ops.col.link": { ru: "Ссылка", en: "Link" },
  "models.ops.details": { ru: "Детали →", en: "Details →" },

  "models.manifests.gitops": { ru: "GitOps", en: "GitOps" },
  "models.manifests.row.gitPath": { ru: "Git путь", en: "Git path" },
  "models.manifests.row.lastCommit": { ru: "Последний коммит", en: "Last commit" },
  "models.manifests.row.argoApp": { ru: "Argo application", en: "Argo application" },
  "models.manifests.row.crossplane": { ru: "Crossplane composite", en: "Crossplane composite" },
  "models.manifests.resolvedSpec": { ru: "Разрешённая спецификация", en: "Resolved spec" },
};
