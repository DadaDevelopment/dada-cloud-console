import type { Messages } from "./common";

export const cost: Messages = {
  "cost.title": { ru: "Стоимость", en: "Cost" },
  "cost.note": { ru: "фактический расход ресурсов кластера по тарифам", en: "actual cluster resource cost at our rates" },
  "cost.window.24h": { ru: "24 часа", en: "24h" },
  "cost.window.7d": { ru: "7 дней", en: "7d" },
  "cost.window.14d": { ru: "14 дней", en: "14d" },
  "cost.window.30d": { ru: "30 дней", en: "30d" },
  "cost.total": { ru: "Итого за {window}", en: "Total ({window})" },
  "cost.cpu": { ru: "CPU", en: "CPU" },
  "cost.ram": { ru: "RAM", en: "RAM" },
  "cost.pv": { ru: "Диски", en: "Storage" },
  "cost.byEnvironment": { ru: "По окружениям", en: "By environment" },
  "cost.empty.title": { ru: "Пока нет данных о стоимости", en: "No cost data yet" },
  "cost.empty.description": { ru: "Данные появятся по мере накопления статистики по кластеру.", en: "Data appears as cluster statistics accumulate." },
  "cost.unavailable": { ru: "Учёт стоимости не настроен", en: "Cost reporting not configured" },
  "cost.error": { ru: "Не удалось загрузить стоимость", en: "Failed to load cost" },
};
