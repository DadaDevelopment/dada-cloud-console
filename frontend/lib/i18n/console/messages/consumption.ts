import type { Messages } from "./common";

export const consumption: Messages = {
  "consumption.title": { ru: "Расход по ресурсам", en: "Resource consumption" },
  "consumption.note": { ru: "оценка по текущим тарифам, не счёт", en: "estimated at our rates, not a bill" },
  "consumption.group.app": { ru: "Приложения", en: "Applications" },
  "consumption.group.database": { ru: "Базы данных", en: "Databases" },
  "consumption.group.storage": { ru: "Хранилище", en: "Storage" },
  "consumption.group.dns": { ru: "DNS", en: "DNS" },
  "consumption.basis.actual": { ru: "фактически", en: "actual" },
  "consumption.basis.estimate": { ru: "ориентировочно", en: "estimated" },
  "consumption.subtotal": { ru: "Подытог", en: "Subtotal" },
  "consumption.total": { ru: "Итого", en: "Total" },
  "consumption.perMonth": { ru: "{amount}/мес", en: "{amount}/mo" },
  "consumption.usage.compute": { ru: "{cpu} vCPU · {ram} ГБ RAM", en: "{cpu} vCPU · {ram} GB RAM" },
  "consumption.usage.storage": { ru: "{gb} ГБ", en: "{gb} GB" },
  "consumption.empty.title": { ru: "Пока нет потребления", en: "No consumption yet" },
  "consumption.empty.description": { ru: "Как только появятся ресурсы, здесь будет оценка их стоимости.", en: "Once resources exist, their estimated cost appears here." },

  "spend.label": { ru: "Расход", en: "Spend" },
  "spend.plan": { ru: "План", en: "Plan" },
  "spend.thisMonth": { ru: "Расход в этом месяце", en: "Spend this month" },
  "spend.balance": { ru: "Баланс", en: "Balance" },
  "spend.toBilling": { ru: "Открыть биллинг", en: "Open billing" },
};
