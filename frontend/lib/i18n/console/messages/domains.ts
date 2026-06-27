import type { Messages } from "./common";

/** Domains page — apex domain authorization (domains.*). */
export const domains: Messages = {
  "domains.title": { ru: "Домены", en: "Domains" },
  "domains.subtitle": {
    ru: "Подтвердите права на apex-домены. Верифицированный apex позволяет подключить его и любые поддомены к приложениям с автоматическим TLS.",
    en: "Authorize apex domains you own. A verified apex lets you attach it and any of its subdomains to apps, with automatic TLS.",
  },
  "domains.add": { ru: "Добавить домен", en: "Add Domain" },

  "domains.empty.title": { ru: "Пока нет доменов", en: "No domains yet" },
  "domains.empty.description": {
    ru: "Подключите собственный домен и получите автоматический HTTPS — подтвердите владение через TXT-запись в DNS.",
    en: "Connect your own domain and get automatic HTTPS — verify ownership via a TXT record in DNS.",
  },
  "domains.empty.cta": { ru: "Добавить первый домен →", en: "Add your first domain →" },

  "domains.status.verified": { ru: "Подтверждён", en: "Verified" },
  "domains.status.failed": { ru: "Ошибка", en: "Error" },
  "domains.status.pending": { ru: "Ожидает", en: "Pending" },

  "domains.row.verifiedAt": { ru: "Проверен {ago}", en: "Verified {ago}" },
  "domains.row.lastChecked": { ru: "Последняя проверка {ago}", en: "Last checked {ago}" },
  "domains.row.added": { ru: "Добавлен {ago}", en: "Added {ago}" },

  "domains.action.verify": { ru: "Проверить", en: "Verify" },

  "domains.challenge.instruction": {
    ru: "Добавьте эту запись {type} у вашего DNS-провайдера, затем нажмите «Проверить». Распространение может занять несколько минут.",
    en: "Add this {type} record at your DNS provider, then click Verify. Verification can take a few minutes to propagate.",
  },
  "domains.challenge.fieldType": { ru: "Тип", en: "Type" },
  "domains.challenge.fieldHost": { ru: "Хост / Имя", en: "Host / Name" },
  "domains.challenge.fieldValue": { ru: "Значение", en: "Value" },

  "domains.modal.title": { ru: "Авторизовать домен", en: "Authorize a Domain" },
  "domains.modal.apexLabel": { ru: "Apex-домен", en: "Apex Domain" },
  "domains.modal.apexTitle": {
    ru: "Корневой домен, например acme.com (без http://, без поддомена, без пути)",
    en: "A bare domain like acme.com (no http://, no subdomain, no path)",
  },
  "domains.modal.apexHelp": {
    ru: "Введите корневой домен, которым вы владеете, например acme.com. Верификация разрешит apex и все поддомены для этого проекта.",
    en: "Enter the apex (root) domain you own, e.g. acme.com. Verifying it authorizes the apex and all subdomains for this project.",
  },
  "domains.modal.apexHelpAnd": { ru: "и все поддомены", en: "and all subdomains" },
  "domains.modal.adding": { ru: "Добавление…", en: "Adding…" },

  "domains.error.load": { ru: "Не удалось загрузить домены", en: "Failed to load domains" },
  "domains.error.add": { ru: "Не удалось добавить домен", en: "Failed to add domain" },
  "domains.error.verify": { ru: "Верификация не удалась", en: "Verification failed" },
  "domains.error.delete": { ru: "Не удалось удалить домен", en: "Failed to delete domain" },

  "domains.confirm.remove": {
    ru: "Удалить авторизацию домена? Сначала необходимо отвязать все подключённые хосты.",
    en: "Remove this domain authorization? Attached hostnames must be detached first.",
  },
};
