import type { Messages } from "./common";

/** Agents page — user-built kagent agents (agents.*). */
export const agents: Messages = {
  "agents.title": { ru: "Агенты", en: "Agents" },
  "agents.subtitle": {
    ru: "Свои LLM-агенты: промпт, инструменты MCP, живое состояние",
    en: "Your own LLM agents: prompt, MCP tools, live state",
  },
  "agents.create": { ru: "Создать агента", en: "Create agent" },

  "agents.empty.title": { ru: "Пока нет агентов", en: "No agents yet" },
  "agents.empty.description": {
    ru: "Агент — это промпт плюс набор MCP-инструментов. Консоль пишет его в git, кластер поднимает рантайм, а каждый его разговор с моделью виден в Langfuse.",
    en: "An agent is a prompt plus a set of MCP tools. The console writes it to git, the cluster brings the runtime up, traces go to Langfuse.",
  },
  "agents.empty.step1": { ru: "Опишите роль агента в промпте", en: "Describe the agent's role in the prompt" },
  "agents.empty.step2": { ru: "Выберите MCP-серверы, которыми он пользуется", en: "Pick the MCP servers it may use" },
  "agents.empty.step3": { ru: "Следите за готовностью и разговорами прямо здесь", en: "Watch readiness and traces right here" },

  "agents.state.pending": { ru: "Ещё не синхронизирован", en: "Not synced yet" },
  "agents.state.ready": { ru: "Отвечает", en: "Serving" },
  "agents.state.notReady": { ru: "Не готов", en: "Not ready" },
  "agents.state.unknown": { ru: "Состояние рантайма недоступно", en: "Runtime state unavailable" },
  "agents.state.promptVersion": { ru: "Версия промпта: {version}", en: "Prompt version: {version}" },
  "agents.state.pods": { ru: "Поды: {ready}/{total} готовы", en: "Pods: {ready}/{total} ready" },
  "agents.state.restarts": { ru: "рестартов: {count}", en: "restarts: {count}" },
  "agents.state.traces": { ru: "Разговоры в Langfuse →", en: "Langfuse traces →" },
  "agents.state.telegram": { ru: "Telegram: @{username} ↗", en: "Telegram: @{username} ↗" },
  "agents.gitOwned": {
    ru: "Агент описан в git вручную, вне консоли: правки только коммитом в инфраструктурный репозиторий",
    en: "This agent is described in git by hand, outside the console: it changes by a commit to the infrastructure repo",
  },

  "agents.action.edit": { ru: "Изменить", en: "Edit" },
  "agents.action.delete": { ru: "Удалить", en: "Delete" },

  "agents.modal.createTitle": { ru: "Создать агента", en: "Create agent" },
  "agents.modal.editTitle": { ru: "Изменить агента", en: "Edit agent" },
  "agents.modal.name": { ru: "Имя", en: "Name" },
  "agents.modal.nameSub": { ru: "(строчные буквы, цифры и дефисы)", en: "(lowercase letters, digits and hyphens)" },
  "agents.modal.nameLocked": { ru: "Имя менять нельзя — создайте нового агента", en: "The name cannot change — create a new agent instead" },
  "agents.modal.displayName": { ru: "Отображаемое имя", en: "Display name" },
  "agents.modal.description": { ru: "Описание", en: "Description" },
  "agents.modal.prompt": { ru: "Системный промпт", en: "System prompt" },
  "agents.modal.promptHint": {
    ru: "Пустой промпт означает голую модель без роли, поэтому он обязателен.",
    en: "An empty prompt means the bare model with no role, so it is required.",
  },
  "agents.modal.promptVersion": { ru: "Версия промпта", en: "Prompt version" },
  "agents.modal.promptVersionHint": {
    ru: "Проставляется в под — по ней видно, доехала ли правка промпта.",
    en: "Lands in the pod — it is how you see whether a prompt edit arrived.",
  },
  "agents.modal.modelConfig": { ru: "Конфигурация модели", en: "Model config" },
  "agents.modal.tools": { ru: "MCP-серверы", en: "MCP servers" },
  "agents.modal.toolsEmpty": {
    ru: "Рантайм агентов не отвечает — список инструментов недоступен, остальные поля сохранить можно.",
    en: "The agent runtime is not answering — the tool list is unavailable, the other fields still save.",
  },
  "agents.modal.toolNotReady": { ru: "не принят рантаймом", en: "not accepted by the runtime" },
  "agents.modal.toolDiscovered": { ru: "инструментов: {count}", en: "{count} tools" },
  "agents.modal.telegram.label": { ru: "Telegram-бот", en: "Telegram bot" },
  "agents.modal.telegram.saveFirst": {
    ru: "Сохраните агента, затем привяжите бота",
    en: "Save the agent first, then connect a bot",
  },
  "agents.modal.telegram.checking": { ru: "Проверяем привязку…", en: "Checking binding…" },
  "agents.modal.telegram.connected": { ru: "Подключён как @{username}", en: "Connected as @{username}" },
  "agents.modal.telegram.tokenPlaceholder": { ru: "Токен бота от @BotFather", en: "Bot token from @BotFather" },
  "agents.modal.telegram.connect": { ru: "Подключить", en: "Connect" },
  "agents.modal.telegram.connecting": { ru: "Подключаем…", en: "Connecting…" },
  "agents.modal.telegram.disconnect": { ru: "Отключить", en: "Disconnect" },
  "agents.modal.telegram.disconnecting": { ru: "Отключаем…", en: "Disconnecting…" },
  "agents.modal.telegram.errorBind": { ru: "Не удалось подключить бота", en: "Failed to connect the bot" },
  "agents.modal.telegram.errorUnbind": { ru: "Не удалось отключить бота", en: "Failed to disconnect the bot" },
  "agents.modal.customMcp.add": { ru: "Добавить свой MCP", en: "Add your own MCP" },
  "agents.modal.customMcp.remove": { ru: "Убрать", en: "Remove" },
  "agents.modal.customMcp.namePlaceholder": { ru: "имя-сервера", en: "server-name" },
  "agents.modal.customMcp.headersPlaceholder": {
    ru: "Authorization: Bearer ${MCP_TOKEN}",
    en: "Authorization: Bearer ${MCP_TOKEN}",
  },
  "agents.modal.customMcp.hint": {
    ru: "По строке на заголовок. Значение может ссылаться на переменную окружения агента: ${MCP_TOKEN} — тогда токен лежит только в env, а не в двух местах.",
    en: "One header per line. A value may refer to this agent's environment as ${MCP_TOKEN}, so the token lives in env only.",
  },
  "agents.modal.env": { ru: "Переменные окружения", en: "Environment variables" },
  "agents.modal.envHint": { ru: "По строке на переменную: KEY=value", en: "One per line: KEY=value" },
  "agents.modal.save": { ru: "Сохранить", en: "Save" },
  "agents.modal.saving": { ru: "Сохраняем…", en: "Saving…" },
  "agents.modal.cancel": { ru: "Отмена", en: "Cancel" },

  "agents.delete.title": { ru: "Удалить агента", en: "Delete agent" },
  "agents.delete.confirm": {
    ru: "Агент {name} перестанет отвечать, а его заявка уедет из git. Продолжить?",
    en: "Agent {name} stops answering and its claim leaves git. Continue?",
  },
  "agents.delete.submit": { ru: "Удалить", en: "Delete" },

  "agents.error.load": { ru: "Не удалось загрузить агентов", en: "Failed to load agents" },
  "agents.error.save": { ru: "Не удалось сохранить агента", en: "Failed to save the agent" },
  "agents.error.delete": { ru: "Не удалось удалить агента", en: "Failed to delete the agent" },
};
