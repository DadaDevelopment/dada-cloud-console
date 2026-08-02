import type { Messages } from "./common";

/** AI API page (projects/[projectId]/ai/page.tsx) — keys, BYOK credentials, catalog, usage. */
export const ai: Messages = {
  "ai.title": { ru: "LLM-провайдеры", en: "LLM providers" },
  "ai.subtitle": {
    ru: "OpenAI-совместимый эндпоинт из России — без VPN и без прокси. Свой ключ провайдера, наш роутинг.",
    en: "An OpenAI-compatible endpoint reachable from Russia — no VPN, no proxy. Your provider key, our routing.",
  },

  "ai.mode.title": { ru: "Чей ключ платит провайдеру", en: "Whose key pays the provider" },
  "ai.mode.subtitle": {
    ru: "Один выбор, от него зависит всё остальное на странице. Поменять можно в любой момент.",
    en: "One choice, and everything below follows from it. You can change it at any time.",
  },
  "ai.mode.byok.title": { ru: "Свои ключи", en: "Your own keys" },
  "ai.mode.byok.body": {
    ru: "У вас уже есть ключ OpenAI или Anthropic. Мы только маршрутизируем запрос из России и берём за это ноль.",
    en: "You already have an OpenAI or Anthropic key. We only route the request out of Russia, and charge nothing for it.",
  },
  "ai.mode.byok.bullet1": { ru: "Шлюз бесплатный", en: "The gateway is free" },
  "ai.mode.byok.bullet2": { ru: "Нужно завести ключ провайдера", en: "You need to add a provider key" },
  "ai.mode.byok.cta": { ru: "Использовать свои ключи", en: "Use my own keys" },
  "ai.mode.platform.title": { ru: "Наши ключи", en: "Our keys" },
  "ai.mode.platform.body": {
    ru: "Ключей нет и заводить не хочется. Берите наш — работает сразу, платите нам по факту расхода.",
    en: "No keys, and no wish to get any. Use ours: it works immediately and you pay us for what you spend.",
  },
  "ai.mode.platform.bullet1": { ru: "Ничего настраивать не нужно", en: "Nothing to configure" },
  "ai.mode.platform.bullet2": {
    ru: "Наценка ×{markup} к цене провайдера, списание по расходу",
    en: "A ×{markup} markup on the provider price, charged as you spend",
  },
  "ai.mode.platform.cta": { ru: "Взять наш ключ", en: "Use our key" },
  "ai.mode.platform.enabling": { ru: "Включаем…", en: "Enabling…" },
  "ai.mode.platform.on": { ru: "Работаете на нашем ключе", en: "Running on our key" },
  "ai.mode.platform.off": { ru: "Работаете на своих ключах", en: "Running on your own keys" },
  "ai.mode.platform.active": { ru: "Вернуться на свои ключи", en: "Switch back to my own keys" },
  "ai.mode.platform.pending": { ru: "Переключаем…", en: "Switching…" },
  "ai.mode.back": { ru: "← Другой способ", en: "← The other way" },
  "ai.mode.error.save": { ru: "Не удалось переключить режим", en: "Failed to switch the mode" },

  "ai.quickstart.title": { ru: "Быстрый старт", en: "Quickstart" },
  "ai.quickstart.body": {
    ru: "Две строки в вашем коде. Любой SDK, который умеет OpenAI, умеет и это.",
    en: "Two lines in your code. Any SDK that speaks OpenAI speaks this.",
  },
  "ai.quickstart.tab.python": { ru: "Python", en: "Python" },
  "ai.quickstart.tab.node": { ru: "Node.js", en: "Node.js" },
  "ai.quickstart.tab.curl": { ru: "curl", en: "curl" },
  "ai.quickstart.keyPlaceholder": {
    ru: "Создайте ключ ниже — сниппет подставит его сюда.",
    en: "Create a key below and the snippet fills it in here.",
  },

  "ai.step.1": { ru: "Создайте ключ", en: "Create a key" },
  "ai.step.2": { ru: "Добавьте ключ провайдера", en: "Add a provider key" },
  "ai.step.3": { ru: "Вызовите модель", en: "Call a model" },

  "ai.keys.title": { ru: "Ключи AI Gateway", en: "AI Gateway keys" },
  "ai.keys.subtitle": {
    ru: "Ключ проекта для эндпоинта. Раздавайте его сервисам, отзывайте по одному.",
    en: "A project key for the endpoint. Hand it to services, revoke them one by one.",
  },
  "ai.keys.create": { ru: "Создать ключ", en: "Create key" },
  "ai.keys.creating": { ru: "Создание…", en: "Creating…" },
  "ai.keys.name.placeholder": { ru: "например, telegram-bot", en: "e.g. telegram-bot" },
  "ai.keys.empty": {
    ru: "Ключей пока нет. Создайте первый — он сразу заработает.",
    en: "No keys yet. Create the first one — it works immediately.",
  },
  "ai.keys.unnamed": { ru: "Без названия", en: "Unnamed" },
  "ai.keys.createdAt": { ru: "создан {ago}", en: "created {ago}" },
  "ai.keys.lastUsed": { ru: "использован {ago}", en: "last used {ago}" },
  "ai.keys.neverUsed": { ru: "ещё не использован", en: "never used" },
  "ai.keys.revoked": { ru: "Отозван", en: "Revoked" },
  "ai.keys.revoke": { ru: "Отозвать", en: "Revoke" },
  "ai.keys.revoke.confirm": {
    ru: "Отозвать этот ключ? Всё, что его использует, перестанет отвечать в течение минуты.",
    en: "Revoke this key? Anything using it stops working within a minute.",
  },
  "ai.keys.created.title": { ru: "Ключ создан", en: "Key created" },
  "ai.keys.created.warning": {
    ru: "Скопируйте ключ сейчас — он показывается один раз и больше не будет виден.",
    en: "Copy the key now — it is shown once and cannot be retrieved again.",
  },
  "ai.keys.created.done": { ru: "Готово, я сохранил ключ", en: "Done, I saved the key" },
  "ai.keys.error.load": { ru: "Не удалось загрузить ключи", en: "Failed to load keys" },
  "ai.keys.error.create": { ru: "Не удалось создать ключ", en: "Failed to create key" },
  "ai.keys.error.revoke": { ru: "Не удалось отозвать ключ", en: "Failed to revoke key" },

  "ai.creds.title": { ru: "Ключи провайдеров", en: "Provider keys" },
  "ai.creds.subtitle": {
    ru: "Свой ключ провайдера (BYOK). Хранится зашифрованным, подставляется на стороне шлюза — из браузера больше не читается.",
    en: "Bring your own provider key. Stored encrypted, injected gateway-side — never readable from the browser again.",
  },
  "ai.creds.none": {
    ru: "Ключей провайдеров нет. Без них вызовы будут падать: шлюз не подставит чужой ключ.",
    en: "No provider keys yet. Calls fail without one — the gateway will not substitute someone else's key.",
  },
  "ai.creds.add": { ru: "Добавить ключ", en: "Add key" },
  "ai.creds.replace": { ru: "Заменить", en: "Replace" },
  "ai.creds.save": { ru: "Сохранить", en: "Save" },
  "ai.creds.saving": { ru: "Сохранение…", en: "Saving…" },
  "ai.creds.cancel": { ru: "Отмена", en: "Cancel" },
  "ai.creds.delete": { ru: "Удалить", en: "Delete" },
  "ai.creds.delete.confirm": {
    ru: "Удалить ключ этого провайдера? Вызовы к его моделям перестанут работать.",
    en: "Delete this provider key? Calls to its models will stop working.",
  },
  "ai.creds.apiKey.placeholder": { ru: "Ключ провайдера", en: "Provider API key" },
  "ai.creds.apiBase.placeholder": { ru: "api_base (необязательно)", en: "api_base (optional)" },
  "ai.creds.getKey": { ru: "Где взять ключ →", en: "Where to get a key →" },
  "ai.creds.updated": { ru: "обновлён {ago}", en: "updated {ago}" },
  "ai.creds.error.load": { ru: "Не удалось загрузить ключи провайдеров", en: "Failed to load provider keys" },
  "ai.creds.error.save": { ru: "Не удалось сохранить ключ", en: "Failed to save key" },
  "ai.creds.error.delete": { ru: "Не удалось удалить ключ", en: "Failed to delete key" },

  "ai.models.title": { ru: "Доступные модели", en: "Available models" },
  "ai.models.subtitle": {
    ru: "Подставьте alias в поле model. Alias стабилен — апстрим можем переключить без правок в вашем коде.",
    en: "Use the alias as the `model` field. Aliases are stable — we can re-point the upstream without touching your code.",
  },
  "ai.models.col.alias": { ru: "Alias", en: "Alias" },
  "ai.models.col.provider": { ru: "Провайдер", en: "Provider" },
  "ai.models.col.kind": { ru: "Тип", en: "Kind" },
  "ai.models.col.upstream": { ru: "Апстрим", en: "Upstream" },
  "ai.models.kind.chat": { ru: "чат", en: "chat" },
  "ai.models.kind.embeddings": { ru: "эмбеддинги", en: "embeddings" },
  "ai.models.needsKey": { ru: "нужен ключ провайдера", en: "needs a provider key" },
  "ai.models.error.load": { ru: "Не удалось загрузить каталог", en: "Failed to load the catalog" },

  "ai.usage.title": { ru: "Расход", en: "Usage" },
  "ai.usage.window.7": { ru: "7 дней", en: "7 days" },
  "ai.usage.window.30": { ru: "30 дней", en: "30 days" },
  "ai.usage.calls": { ru: "Вызовы", en: "Calls" },
  "ai.usage.tokens": { ru: "Токены", en: "Tokens" },
  "ai.usage.cost": { ru: "Стоимость у провайдера", en: "Provider cost" },
  "ai.usage.costHint": {
    ru: "Считается по вашему ключу провайдера. Мы за шлюз не берём денег.",
    en: "Billed on your own provider key. We do not charge for the gateway.",
  },
  "ai.usage.billed": { ru: "К оплате нам", en: "Billed by us" },
  "ai.usage.billedHint": {
    ru: "Роутинг на нашем ключе: цена провайдера плюс наценка.",
    en: "Routing on our key: the provider price plus the markup.",
  },
  "ai.usage.empty": { ru: "За это окно вызовов не было.", en: "No calls in this window." },
  "ai.usage.byModel": { ru: "По моделям", en: "By model" },
  "ai.usage.error.load": { ru: "Не удалось загрузить расход", en: "Failed to load usage" },

  "ai.free.badge": { ru: "Входит в тариф", en: "Included in your plan" },
  "ai.free.body": {
    ru: "Шлюз бесплатный: платите только провайдеру по своему ключу.",
    en: "The gateway is free: you pay only the provider, on your own key.",
  },
};
