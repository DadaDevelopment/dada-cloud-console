import type { Messages } from "./common";

/**
 * Admin — AI Gateway's own usage/cost dashboard (provider/project/model/source
 * breakdown from the gateway's LiteLLM ledger). God-admin only.
 */
export const aiGatewayUsage: Messages = {
  "aiGateway.crumb.aiGateway": { ru: "AI-шлюз", en: "AI Gateway" },
  "aiGateway.title": { ru: "AI-шлюз: использование", en: "AI Gateway usage" },
  "aiGateway.subtitle": {
    ru: "Стоимость и токены по провайдерам, проектам, моделям и источникам трафика. Доступно только администраторам платформы.",
    en: "Cost and tokens by provider, project, model, and traffic source. Platform-admin only.",
  },
  "aiGateway.accessDenied": {
    ru: "Нет доступа. AI-шлюз доступен только администраторам платформы.",
    en: "No access. The AI Gateway view is available to platform admins only.",
  },
  "aiGateway.error.load": { ru: "Не удалось загрузить данные AI-шлюза", en: "Failed to load AI Gateway data" },

  "aiGateway.window.7d": { ru: "7 дней", en: "7d" },
  "aiGateway.window.30d": { ru: "30 дней", en: "30d" },

  "aiGateway.kpi.totalCost": { ru: "Стоимость (провайдеры)", en: "Cost (providers)" },
  "aiGateway.kpi.totalCalls": { ru: "Вызовов", en: "Calls" },

  "aiGateway.table.provider": { ru: "Провайдер", en: "Provider" },
  "aiGateway.table.project": { ru: "Проект", en: "Project" },
  "aiGateway.table.model": { ru: "Модель", en: "Model" },
  "aiGateway.table.source": { ru: "Источник", en: "Source" },
  "aiGateway.table.calls": { ru: "Вызовов", en: "Calls" },
  "aiGateway.table.promptTokens": { ru: "Токенов (запрос)", en: "Prompt tokens" },
  "aiGateway.table.completionTokens": { ru: "Токенов (ответ)", en: "Completion tokens" },
  "aiGateway.table.cost": { ru: "Стоимость, $", en: "Cost, $" },

  "aiGateway.byProvider.title": { ru: "По провайдерам", en: "By provider" },
  "aiGateway.byProject.title": { ru: "По проектам", en: "By project" },
  "aiGateway.byModel.title": { ru: "По моделям", en: "By model" },
  "aiGateway.bySource.title": { ru: "По источнику трафика", en: "By traffic source" },

  "aiGateway.source.console_chat": { ru: "Чат в консоли", en: "Console chat" },
  "aiGateway.source.gateway": { ru: "Прямой BYOK-вызов", en: "Direct BYOK call" },
  "aiGateway.source.cloud_task": { ru: "Облачная задача (claude -p)", en: "Cloud task (claude -p)" },

  "aiGateway.empty": { ru: "Нет данных за выбранный период", en: "No data for the selected window" },
};
