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
  "domains.hm.defaultBadge": { ru: "по умолчанию", en: "default" },
  "domains.hm.reason.dns_not_pointed": {
    ru: "DNS ещё не указывает на платформу — пока запись не переедет, сертификат выпустить нельзя.",
    en: "DNS does not point at the platform yet — the certificate cannot be issued until the record moves.",
  },
  "domains.hm.reason.cert_pending": {
    ru: "DNS на месте, выпускаем сертификат — обычно занимает минуту.",
    en: "DNS is in place, the certificate is being issued — usually takes a minute.",
  },
  "domains.hm.reason.route_missing": {
    ru: "Сертификат выпущен, но маршрута к приложению нет — платформа не отдаёт этот адрес. Передеплойте приложение или привяжите хост заново.",
    en: "The certificate is issued, but no route to the app exists — the platform does not serve this address yet. Redeploy the app or attach the hostname again.",
  },
  "domains.hm.reason.attach_timeout": {
    ru: "Домен так и не заработал за 48 часов. Проверьте DNS-запись и привяжите хост заново.",
    en: "The domain never came up within 48 hours. Check the DNS record and attach the hostname again.",
  },
  "domains.hm.detach": { ru: "Отвязать", en: "Detach" },
  "domains.hm.detaching": { ru: "Отвязывание…", en: "Detaching…" },
  "domains.hm.detachError": { ru: "Не удалось отвязать хост", en: "Failed to detach hostname" },
  "domains.hm.confirmDetach": {
    ru: "Отвязать {name}? Его TLS-сертификат и ingress будут удалены.",
    en: "Detach {name}? Its TLS certificate and ingress will be removed.",
  },

  "domains.path.toggleLabel": { ru: "Как подключить домен", en: "How to connect the domain" },
  "domains.path.advanced": { ru: "Указать запись сами", en: "Point a record yourself" },
  "domains.path.advancedHint": { ru: "продвинуто", en: "advanced" },
  "domains.path.delegate": { ru: "Делегировать нам", en: "Delegate to us" },
  "domains.path.delegateHint": { ru: "рекомендуется", en: "recommended" },

  "domains.dns.pickApex": { ru: "Домен для делегирования", en: "Domain to delegate" },
  "domains.dns.selectApex": { ru: "Выберите домен...", en: "Select a domain..." },
  "domains.dns.needVerified": {
    ru: "Сначала подтвердите apex-домен выше -- делегировать можно только подтверждённый домен.",
    en: "Verify an apex domain above first -- only a verified domain can be delegated.",
  },
  "domains.dns.intro": {
    ru: "Делегируйте домен нашим серверам имён -- мы возьмём на себя DNS, маршрутизацию и TLS. Вам останется один раз прописать NS у регистратора.",
    en: "Delegate the domain to our nameservers -- we handle DNS, routing and TLS. You only set the NS records at your registrar once.",
  },
  "domains.dns.delegateBtn": { ru: "Делегировать домен", en: "Delegate domain" },
  "domains.dns.delegating": { ru: "Делегирование...", en: "Delegating..." },
  "domains.dns.nsTitle": { ru: "Пропишите эти NS у вашего регистратора", en: "Set these NS records at your registrar" },
  "domains.dns.nsNote": {
    ru: "Замените текущие NS-записи домена на эти две. После распространения мы автоматически подключим домен и выпустим сертификат.",
    en: "Replace the domain's current NS records with these two. Once they propagate we connect the domain and issue the certificate automatically.",
  },
  "domains.dns.statusAwaiting": { ru: "Ожидание делегации", en: "Awaiting delegation" },
  "domains.dns.statusActive": { ru: "Подключено", en: "Connected" },
  "domains.dns.polling": { ru: "Проверяем делегацию каждые 15 с...", en: "Checking delegation every 15s..." },
  "domains.dns.delegateError": { ru: "Не удалось делегировать домен", en: "Failed to delegate domain" },
  "domains.dns.zoneError": { ru: "Не удалось загрузить зону", en: "Failed to load zone" },

  "domains.dns.importTitle": { ru: "Мы нашли ваши текущие записи", en: "We found your current records" },
  "domains.dns.importNote": {
    ru: "Отметьте записи, которые нужно перенести, чтобы почта и сайт продолжили работать после смены NS.",
    en: "Select the records to carry over so email and site keep working after the NS switch.",
  },
  "domains.dns.importBtn": { ru: "Перенести", en: "Import selected" },
  "domains.dns.importing": { ru: "Перенос...", en: "Importing..." },
  "domains.dns.importDone": { ru: "Перенесено: {imported}, пропущено: {skipped}", en: "Imported {imported}, skipped {skipped}" },
  "domains.dns.importError": { ru: "Не удалось перенести записи", en: "Failed to import records" },

  "domains.dns.recordsTitle": { ru: "Записи зоны", en: "Zone records" },
  "domains.dns.recordsEmpty": { ru: "В зоне пока нет записей.", en: "No records in this zone yet." },
  "domains.dns.recordsError": { ru: "Не удалось загрузить записи", en: "Failed to load records" },
  "domains.dns.addRecord": { ru: "Добавить запись", en: "Add record" },
  "domains.dns.thName": { ru: "Имя", en: "Name" },
  "domains.dns.thType": { ru: "Тип", en: "Type" },
  "domains.dns.thTtl": { ru: "TTL", en: "TTL" },
  "domains.dns.thValue": { ru: "Значение", en: "Value" },
  "domains.dns.namePlaceholder": { ru: "@ или www", en: "@ or www" },
  "domains.dns.valuePlaceholder": { ru: "Значение записи", en: "Record value" },
  "domains.dns.saveRecord": { ru: "Сохранить", en: "Save" },
  "domains.dns.saving": { ru: "Сохранение...", en: "Saving..." },
  "domains.dns.saveError": { ru: "Не удалось сохранить запись", en: "Failed to save record" },
  "domains.dns.deleteError": { ru: "Не удалось удалить запись", en: "Failed to delete record" },
  "domains.dns.confirmDelete": { ru: "Удалить запись {name} {type}?", en: "Delete record {name} {type}?" },
  "domains.dns.protectedNote": { ru: "Системные записи NS/SOA нельзя изменить.", en: "System NS/SOA records cannot be edited." },
  "domains.dns.edit": { ru: "Изменить", en: "Edit" },

  "domains.hostStatus.active": { ru: "Подключено", en: "Connected" },
  "domains.hostStatus.pending": { ru: "Ожидание DNS/сертификата", en: "Awaiting DNS/certificate" },
  "domains.hostStatus.failed": { ru: "Ошибка", en: "Failed" },

  "domains.row.pointsTo": { ru: "→ {app}", en: "→ {app}" },
  "domains.tag.delegated": { ru: "Делегирован", en: "Delegated" },

  "domains.apex.needsVerify": { ru: "Требует подтверждения", en: "Needs verification" },
  "domains.apex.verifiedIdle": {
    ru: "Подтверждён — добавьте хост или делегируйте домен",
    en: "Verified — add a hostname or delegate the domain",
  },

  "domains.action.addHost": { ru: "Добавить хост", en: "Add hostname" },
  "domains.action.delegateEdit": { ru: "Настроить DNS", en: "Manage DNS" },

  "domains.edit.recordUnknown": {
    ru: "Тип записи: {type}. Точное значение показывается при первичном подключении хоста.",
    en: "Record type: {type}. The exact target is shown when the hostname is first attached.",
  },

  "domains.funnel.title": { ru: "Добавить домен", en: "Add domain" },
  "domains.funnel.inputLabel": { ru: "Домен", en: "Domain" },
  "domains.funnel.inputHelp": {
    ru: "Введите домен или поддомен, например acme.com или shop.acme.com.",
    en: "Enter a domain or subdomain, e.g. acme.com or shop.acme.com.",
  },
  "domains.funnel.continue": { ru: "Продолжить", en: "Continue" },
  "domains.funnel.verifyTitle": { ru: "Подтвердите владение доменом {apex}", en: "Verify ownership of {apex}" },
  "domains.funnel.verifyIntro": {
    ru: "Добавьте эту TXT-запись у вашего регистратора, затем нажмите «Проверить».",
    en: "Add this TXT record at your registrar, then click Verify.",
  },
  "domains.funnel.verifyPending": {
    ru: "Домен ещё не подтверждён. Проверяем автоматически каждые 10 с…",
    en: "Domain not verified yet. Checking automatically every 10s…",
  },
  "domains.funnel.pathTitle": { ru: "Как использовать {domain}?", en: "How should {domain} be used?" },
  "domains.funnel.pointApp": { ru: "Направить на приложение", en: "Point to an app" },
  "domains.funnel.done": { ru: "Готово", en: "Done" },
};
