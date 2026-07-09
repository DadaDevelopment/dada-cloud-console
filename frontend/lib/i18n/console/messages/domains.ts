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
  "domains.empty.create": { ru: "Добавить домен", en: "Add domain" },
  "domains.empty.step1": { ru: "Добавьте свой домен", en: "Add your own domain" },
  "domains.empty.step2": { ru: "Подтвердите владение TXT-записью в DNS", en: "Verify ownership with a DNS TXT record" },
  "domains.empty.step3": { ru: "HTTPS-сертификат выпустится автоматически", en: "An HTTPS certificate is issued automatically" },

  "domains.status.verified": { ru: "Подтверждён", en: "Verified" },
  "domains.status.failed": { ru: "Ошибка", en: "Error" },
  "domains.status.pending": { ru: "Ожидает", en: "Pending" },

  "domains.row.verifiedAt": { ru: "Проверен {ago}", en: "Verified {ago}" },
  "domains.row.lastChecked": { ru: "Последняя проверка {ago}", en: "Last checked {ago}" },
  "domains.row.added": { ru: "Добавлен {ago}", en: "Added {ago}" },

  "domains.action.verify": { ru: "Проверить сейчас", en: "Check now" },
  "domains.autoCheck": { ru: "Автопроверка каждые 30 с", en: "Auto-checking every 30s" },
  "domains.checking": { ru: "Проверка…", en: "Checking…" },

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

  "domains.hostnames.title": { ru: "Привязать хост к приложению", en: "Attach a hostname to an app" },
  "domains.hostnames.subtitle": {
    ru: "Направьте поддомен подтверждённого домена на одно из ваших приложений — TLS-сертификат выпустится автоматически.",
    en: "Point a subdomain of a verified domain at one of your apps — TLS is issued automatically.",
  },
  "domains.hostnames.needVerified": {
    ru: "Сначала подтвердите хотя бы один apex-домен выше — после этого его хосты можно привязывать к приложениям.",
    en: "Verify at least one apex domain above first — then its hostnames can be attached to apps.",
  },
  "domains.hostnames.env": { ru: "Окружение", en: "Environment" },
  "domains.hostnames.app": { ru: "Приложение", en: "Application" },
  "domains.hostnames.selectApp": { ru: "Выберите приложение…", en: "Select an app…" },
  "domains.hostnames.loadingApps": { ru: "Загрузка приложений…", en: "Loading apps…" },
  "domains.hostnames.noApps": { ru: "В этом окружении пока нет приложений", en: "No apps in this environment yet" },

  "domains.hm.title": { ru: "Свои хосты", en: "Custom hostnames" },
  "domains.hm.subtitle": {
    ru: "Привяжите хост под доменом, который вы подтвердили для этого проекта. TLS выпускается автоматически.",
    en: "Attach a hostname under a domain you've verified for this project. TLS is issued automatically.",
  },
  "domains.hm.authorizePre": { ru: "Подтвердите apex-домены на странице", en: "Authorize apex domains on the" },
  "domains.hm.authorizeLink": { ru: "Домены проекта", en: "project Domains" },
  "domains.hm.authorizePost": { ru: "сначала.", en: "page first." },
  "domains.hm.inputTitle": {
    ru: "Хост под подтверждённым apex, например shop.acme.com или acme.com",
    en: "A hostname under a verified apex, e.g. shop.acme.com or acme.com",
  },
  "domains.hm.attach": { ru: "Привязать", en: "Attach" },
  "domains.hm.attachError": { ru: "Не удалось привязать хост", en: "Failed to attach hostname" },
  "domains.hm.loadError": { ru: "Не удалось загрузить хосты", en: "Failed to load hostnames" },
  "domains.hm.dnsTitle": { ru: "Направьте свой DNS на платформу:", en: "Point your DNS at the platform:" },
  "domains.hm.dnsNote": {
    ru: "Сертификат выпустится, как только DNS начнёт указывать на ingress платформы.",
    en: "The certificate is issued once DNS resolves to the platform ingress.",
  },
  "domains.hm.empty": { ru: "К этому приложению не привязано ни одного хоста.", en: "No custom hostnames attached to this app." },
  "domains.hm.thHostname": { ru: "Хост", en: "Hostname" },
  "domains.hm.thRecord": { ru: "Запись", en: "Record" },
  "domains.hm.thStatus": { ru: "Статус", en: "Status" },
  "domains.hm.thCert": { ru: "Сертификат", en: "Certificate" },
  "domains.hm.detach": { ru: "Отвязать", en: "Detach" },
  "domains.hm.detaching": { ru: "Отвязывание…", en: "Detaching…" },
  "domains.hm.detachError": { ru: "Не удалось отвязать хост", en: "Failed to detach hostname" },
  "domains.hm.confirmDetach": {
    ru: "Отвязать {name}? Его TLS-сертификат и ingress будут удалены.",
    en: "Detach {name}? Its TLS certificate and ingress will be removed.",
  },
};
