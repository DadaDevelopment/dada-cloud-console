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

  "spend.quota.title": { ru: "Тариф {plan}: что включено", en: "{plan} plan: what is included" },
  "spend.quota.apps": { ru: "Приложения", en: "Apps" },
  "spend.quota.databases": { ru: "Базы данных", en: "Databases" },
  "spend.quota.domains": { ru: "Домены", en: "Domains" },
  "spend.quota.team_members": { ru: "Участники", en: "Members" },
  "spend.quota.value": { ru: "{used} из {limit}", en: "{used} of {limit}" },
  "spend.quota.full": { ru: "Лимит исчерпан", en: "Limit reached" },
  "spend.quota.grace": {
    ru: "Что уже создано — работает. Лимиты начнут действовать {date}.",
    en: "Everything you already created keeps running. Limits start applying on {date}.",
  },
  "spend.quota.upgrade": { ru: "Увеличить лимиты", en: "Raise the limits" },

  "grace.banner.text": {
    ru: "У вас больше ресурсов ({resources}), чем включено в бесплатный тариф. Всё созданное продолжит работать, но с {date} создавать новые не получится.",
    en: "You have more resources ({resources}) than the free plan includes. Everything you created keeps running, but from {date} you will not be able to create new ones.",
  },
  "grace.banner.cta": { ru: "Посмотреть тарифы", en: "See the plans" },
  "grace.banner.dismiss": { ru: "Скрыть", en: "Dismiss" },
  "quota.upsell.title": { ru: "Достигнут лимит тарифа", en: "You have reached your plan limit" },
  "quota.upsell.text": {
    ru: "На текущем тарифе доступно {resource}: {limit}. Всё созданное продолжает работать — чтобы создать ещё, нужен тариф побольше.",
    en: "Your current plan includes {limit} {resource}. Everything you created keeps running — creating more needs a bigger plan.",
  },
  "quota.upsell.textNoLimit": {
    ru: "Лимит текущего тарифа по ресурсу «{resource}» исчерпан. Всё созданное продолжает работать.",
    en: "Your plan limit for {resource} is used up. Everything you created keeps running.",
  },
  "quota.upsell.cta": { ru: "{plan} — {price} ₽/мес, оплатить", en: "{plan} — {price} ₽/mo, pay now" },
  "quota.upsell.plansCta": { ru: "Посмотреть тарифы", en: "See the plans" },
  "quota.upsell.starting": { ru: "Открываем оплату…", en: "Opening checkout…" },
  "quota.upsell.error": { ru: "Не удалось открыть оплату", en: "Could not open checkout" },
};
