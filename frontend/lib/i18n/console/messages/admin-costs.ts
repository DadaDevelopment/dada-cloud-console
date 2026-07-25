import type { Messages } from "./common";

/**
 * Admin — cost/revenue/margin drilldown (god-admin economics view).
 */
export const adminCosts: Messages = {
  "adminCosts.crumb.costs": { ru: "Экономика", en: "Economics" },
  "adminCosts.title": { ru: "Экономика платформы", en: "Platform economics" },
  "adminCosts.subtitle": {
    ru: "Себестоимость и выручка по клиентам, проектам и ресурсам. Доступно только администраторам платформы.",
    en: "Cost and revenue broken down by client, project, and resource. Platform-admin only.",
  },
  "adminCosts.accessDenied": {
    ru: "Нет доступа. Экономика платформы доступна только администраторам платформы.",
    en: "No access. Platform economics is available to platform admins only.",
  },
  "adminCosts.error.load": { ru: "Не удалось загрузить данные по экономике", en: "Failed to load economics data" },
  "adminCosts.unavailable": {
    ru: "Данные по стоимости временно недоступны (OpenCost).",
    en: "Cost data temporarily unavailable (OpenCost).",
  },

  "adminCosts.window.7d": { ru: "7 дней", en: "7d" },
  "adminCosts.window.30d": { ru: "30 дней", en: "30d" },

  "adminCosts.kpi.hardware": { ru: "Стоимость железа", en: "Hardware cost" },
  "adminCosts.kpi.revenue": { ru: "Выручка", en: "Revenue" },
  "adminCosts.kpi.margin": { ru: "Маржа", en: "Margin" },
  "adminCosts.kpi.unallocated": { ru: "Не распределено / простой", en: "Unallocated / idle" },

  "adminCosts.hardwareSource.beget_api": {
    ru: "реальный счёт (Beget, автоматически)",
    en: "real bill (Beget, live)",
  },
  "adminCosts.hardwareSource.beget_manual_config": {
    ru: "реальный счёт (задан вручную)",
    en: "real bill (manually configured)",
  },
  "adminCosts.hardwareSource.opencost_only": {
    ru: "оценка OpenCost — реальный счёт не задан",
    en: "OpenCost estimate — real bill not configured",
  },
  "adminCosts.hardwareSource.note": {
    ru: "Задайте HARDWARE_MONTHLY_COST_RUB, чтобы привязать пропорции OpenCost к реальному счёту за железо.",
    en: "Set HARDWARE_MONTHLY_COST_RUB to anchor OpenCost's proportions to the real hardware bill.",
  },

  "adminCosts.hardware.title": { ru: "Состав счёта за железо", en: "Hardware bill breakdown" },
  "adminCosts.hardware.nodeCount": { ru: "узлов: {count}", en: "{count} node(s)" },

  "adminCosts.method.title": { ru: "Откуда берутся цифры", en: "Where the numbers come from" },
  "adminCosts.method.model": {
    ru: "Реальный счёт за железо распределяется по клиентам пропорционально измеренной доле ресурсов (OpenCost). Сумма расходов по клиентам плюс нераспределённый остаток равны счёту за железо.",
    en: "The real hardware bill is distributed across clients in proportion to their measured resource share (OpenCost). Client costs plus the unallocated remainder equal the hardware bill.",
  },
  "adminCosts.method.clientsSum": { ru: "Клиенты (распределено)", en: "Clients (allocated)" },
  "adminCosts.method.unallocated": { ru: "Не распределено / простой", en: "Unallocated / idle" },
  "adminCosts.method.totalExpenses": { ru: "Итого расходы", en: "Total expenses" },
  "adminCosts.method.hardware": { ru: "Счёт за железо", en: "Hardware bill" },
  "adminCosts.method.delta": { ru: "Расхождение", en: "Discrepancy" },
  "adminCosts.method.reconciled": { ru: "сходится со счётом за железо", en: "reconciles with the hardware bill" },
  "adminCosts.method.params": {
    ru: "Сырьё OpenCost (доля ресурсов): {raw} · множитель до счёта за железо: ×{scale}",
    en: "OpenCost raw (resource share): {raw} · multiplier to hardware bill: ×{scale}",
  },

  "adminCosts.table.total": { ru: "Итого", en: "Total" },

  "adminCosts.lossMakers.title": { ru: "Топ убыточных клиентов", en: "Top loss-makers" },
  "adminCosts.lossMakers.empty": { ru: "Убыточных клиентов нет", en: "No loss-making clients" },

  "adminCosts.table.client": { ru: "Клиент / проект / ресурс", en: "Client / project / resource" },
  "adminCosts.table.cost": { ru: "Стоимость", en: "Cost" },
  "adminCosts.table.revenue": { ru: "Выручка", en: "Revenue" },
  "adminCosts.table.margin": { ru: "Маржа", en: "Margin" },

  "adminCosts.empty": { ru: "Нет данных за выбранный период", en: "No data for the selected window" },
  "adminCosts.expand": { ru: "Развернуть", en: "Expand" },
  "adminCosts.collapse": { ru: "Свернуть", en: "Collapse" },

  "adminCosts.agent.title": { ru: "Агентские запуски (токены ИИ)", en: "Agent runs (AI tokens)" },
  "adminCosts.agent.revenue": { ru: "Выручка", en: "Revenue" },
  "adminCosts.agent.cost": { ru: "Себестоимость", en: "Cost" },
  "adminCosts.agent.margin": { ru: "Маржа", en: "Margin" },
  "adminCosts.agent.tokens": { ru: "Токенов", en: "Tokens" },
  "adminCosts.agent.params": {
    ru: "Себестоимость провайдера: ${usd} · курс {rate} ₽/$ · наценка ×{markup} · окно {days} дн.",
    en: "Provider cost: ${usd} · rate {rate} RUB/$ · markup ×{markup} · window {days}d",
  },
};
