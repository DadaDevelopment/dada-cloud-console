"use client";

import { COMPANY } from "@/lib/company";
import { useLang } from "@/lib/i18n/context";

/**
 * Public requisites page. A business customer paying by bank transfer will not
 * raise a payment order against a site that names no legal entity, and the same
 * page answers the "who is the developer behind this service" question that a
 * procurement check asks before a first purchase. Every value comes from
 * lib/company.ts, which the footer and the legal documents also read.
 */
interface Row {
  label: string;
  value: string;
}

const COPY = {
  ru: {
    title: "Реквизиты",
    intro:
      "Сервис DADA Cloud (cloud.dada-tuda.ru) оказывает российское юридическое лицо. Ниже — полные реквизиты для договора, счёта и платёжного поручения.",
    entityTitle: "Организация",
    bankTitle: "Банковские реквизиты",
    contactTitle: "Контакты",
    contactBody:
      "По вопросам договора, счёта, закрывающих документов и работы сервиса пишите на почту — отвечаем в рабочие дни.",
    invoiceTitle: "Оплата по счёту",
    invoiceBody:
      "Юридическим лицам и ИП счёт выставляется в консоли: раздел «Биллинг» → «Счёт для ЮЛ». Счёт формируется сразу, оплата зачисляется автоматически по номеру счёта в назначении платежа, тариф включается после поступления денег на расчётный счёт.",
    entity: [
      { label: "Полное наименование", value: COMPANY.fullName },
      { label: "Сокращённое наименование", value: COMPANY.shortName },
      { label: "ИНН", value: COMPANY.inn },
      { label: "КПП", value: COMPANY.kpp },
      { label: "ОГРН", value: COMPANY.ogrn },
      { label: "Юридический адрес", value: COMPANY.legalAddress },
    ] as Row[],
    bank: [
      { label: "Расчётный счёт", value: COMPANY.bank.account },
      { label: "Банк", value: COMPANY.bank.name },
      { label: "БИК", value: COMPANY.bank.bic },
      {
        label: "Корреспондентский счёт",
        value: COMPANY.bank.correspondentAccount,
      },
      { label: "ИНН банка", value: COMPANY.bank.inn },
      { label: "КПП банка", value: COMPANY.bank.kpp },
    ] as Row[],
  },
  en: {
    title: "Company details",
    intro:
      "The DADA Cloud service (cloud.dada-tuda.ru) is operated by a Russian legal entity. Below are the full details used for contracts, invoices and bank transfers.",
    entityTitle: "Entity",
    bankTitle: "Bank details",
    contactTitle: "Contact",
    contactBody:
      "For contract, invoice, closing-document or service questions, write to us by email — we reply on business days.",
    invoiceTitle: "Payment by bank transfer",
    invoiceBody:
      "Companies and sole proprietors can issue an invoice in the console: Billing → invoice for a legal entity. The invoice is generated immediately, an incoming transfer is matched automatically by the invoice number in the payment purpose, and the plan is activated once the money reaches the account.",
    entity: [
      { label: "Full name", value: COMPANY.fullName },
      { label: "Short name", value: COMPANY.shortNameEn },
      { label: "INN (tax ID)", value: COMPANY.inn },
      { label: "KPP", value: COMPANY.kpp },
      { label: "OGRN (registration number)", value: COMPANY.ogrn },
      { label: "Registered address", value: COMPANY.legalAddress },
    ] as Row[],
    bank: [
      { label: "Account", value: COMPANY.bank.account },
      { label: "Bank", value: COMPANY.bank.name },
      { label: "BIC", value: COMPANY.bank.bic },
      {
        label: "Correspondent account",
        value: COMPANY.bank.correspondentAccount,
      },
      { label: "Bank INN", value: COMPANY.bank.inn },
      { label: "Bank KPP", value: COMPANY.bank.kpp },
    ] as Row[],
  },
} as const;

function Table({ title, rows }: { title: string; rows: readonly Row[] }) {
  return (
    <div>
      <h2 className="text-lg font-semibold text-slate-900">{title}</h2>
      <dl className="mt-3 divide-y divide-slate-200 rounded-xl border border-slate-200 bg-white">
        {rows.map((r) => (
          <div
            key={r.label}
            className="grid gap-1 px-4 py-3 sm:grid-cols-[220px_1fr] sm:gap-4"
          >
            <dt className="text-sm text-slate-500">{r.label}</dt>
            <dd className="text-sm font-medium text-slate-900 break-words">
              {r.value}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

export function CompanyRequisites() {
  const { locale } = useLang();
  const c = COPY[locale];

  return (
    <section className="mx-auto max-w-3xl px-4 py-16 sm:px-6 lg:py-20">
      <h1 className="text-3xl font-bold tracking-tight text-slate-900 sm:text-4xl">
        {c.title}
      </h1>
      <p className="mt-6 text-base leading-relaxed text-slate-700">{c.intro}</p>
      <div className="mt-10 space-y-8">
        <Table title={c.entityTitle} rows={c.entity} />
        <Table title={c.bankTitle} rows={c.bank} />
        <div>
          <h2 className="text-lg font-semibold text-slate-900">
            {c.invoiceTitle}
          </h2>
          <p className="mt-2 text-base leading-relaxed text-slate-700">
            {c.invoiceBody}
          </p>
        </div>
        <div>
          <h2 className="text-lg font-semibold text-slate-900">
            {c.contactTitle}
          </h2>
          <p className="mt-2 text-base leading-relaxed text-slate-700">
            {c.contactBody}
          </p>
          <p className="mt-2 text-base">
            <a
              className="font-medium text-blue-600 hover:text-blue-700"
              href={`mailto:${COMPANY.email}`}
            >
              {COMPANY.email}
            </a>
          </p>
        </div>
      </div>
    </section>
  );
}
