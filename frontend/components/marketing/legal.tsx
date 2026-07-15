"use client";

import type { Locale } from "@/lib/i18n/dict";

/**
 * Legal content for the marketing site: privacy policy (152-ФЗ oriented) and
 * terms of use. Text is grounded in real platform behavior — auth via Keycloak,
 * GitHub/GitLab connect, Yandex.Metrika analytics (counter 110158915, webvisor
 * on, dada_uid pseudonymous cookie), user-owned Postgres/S3, RU hosting, and the
 * free-plan quotas from backend/internal/billing/data/plans.yaml.
 *
 * Owner action before/right after publish: fill the operator legal identity
 * (юр. лицо / ИП, ОГРН/ИНН, юр. адрес) in the "Оператор" section — the code
 * ships with the public brand name and contact email only, no invented entity.
 */

export interface LegalSection {
  heading: string;
  body: string[];
}

export interface LegalDocData {
  title: string;
  updated: string;
  intro: string;
  sections: LegalSection[];
}

const CONTACT = "hello@dada-tuda.ru";

const privacyRu: LegalDocData = {
  title: "Политика обработки персональных данных",
  updated: "Обновлено: 15 июля 2026",
  intro:
    "Настоящая Политика описывает, какие данные обрабатывает облачная платформа DADA Cloud (cloud.dada-tuda.ru), с какой целью и на каком основании, где они хранятся и какие права есть у пользователя. Обработка данных граждан Российской Федерации ведётся на серверах, расположенных на территории России, в соответствии с Федеральным законом № 152-ФЗ «О персональных данных».",
  sections: [
    {
      heading: "1. Оператор",
      body: [
        "Оператором обработки персональных данных является владелец сервиса DADA Cloud.",
        `По любым вопросам обработки данных и для реализации своих прав обращайтесь на электронную почту: ${CONTACT}.`,
      ],
    },
    {
      heading: "2. Какие данные мы обрабатываем",
      body: [
        "Учётные данные. При регистрации и входе через систему аутентификации мы обрабатываем адрес электронной почты, имя (отображаемое имя), логин и внутренний идентификатор пользователя. Аутентификация выполняется через сервис на базе Keycloak.",
        "Данные подключения репозиториев. При подключении GitHub или GitLab мы храним идентификатор установки приложения, имя аккаунта и его тип. Токены доступа GitHub не сохраняются — они выпускаются на время сборки и имеют короткий срок жизни. Токен доступа GitLab хранится в зашифрованном виде.",
        "Аналитические данные. На сайте установлен счётчик Яндекс.Метрики (номер 110158915). Он собирает cookie, IP-адрес, данные об устройстве и браузере, действия на страницах, а также запись сессии (Вебвизор: движения курсора, клики, просматриваемые страницы). Мы также используем cookie dada_uid — псевдонимный идентификатор пользователя (UUID), не содержащий адреса почты, логина или имени.",
        "Технические логи и метрики. Для работы и наблюдаемости платформы мы обрабатываем журналы и метрики приложений, привязанные к идентификатору проекта. Они хранятся на инфраструктуре в России.",
        "Данные, которые вы размещаете сами. Ваши базы данных, файлы в объектном хранилище, переменные окружения и исходный код развёртываемых приложений. Оператор не использует это содержимое для собственных целей, хранит его на инфраструктуре в России и ограничивает к нему доступ.",
        "Данные коммуникации. Адрес почты аккаунта используется для операционных уведомлений — например, о результате сборки и деплоя.",
      ],
    },
    {
      heading: "3. Цели и основания обработки",
      body: [
        "Предоставление сервиса: создание и ведение аккаунта, аутентификация, деплой приложений, управление проектами, базами данных, доменами и хранилищем.",
        "Обеспечение работоспособности и безопасности: мониторинг, диагностика ошибок, защита от злоупотреблений.",
        "Аналитика и улучшение продукта: измерение посещаемости и поведения на сайте в обезличенном или псевдонимном виде.",
        "Коммуникация: отправка операционных и сервисных сообщений.",
        "Основания обработки: согласие пользователя, исполнение договора (пользовательского соглашения) и законные интересы оператора по обеспечению работы сервиса.",
      ],
    },
    {
      heading: "4. Где хранятся данные",
      body: [
        "Персональные данные и пользовательский контент обрабатываются и хранятся на серверах, расположенных на территории Российской Федерации. Это соответствует требованию о локализации данных граждан РФ (152-ФЗ).",
      ],
    },
    {
      heading: "5. Передача третьим лицам",
      body: [
        "Яндекс.Метрика (ООО «Яндекс») — сбор и обработка обезличенной статистики посещений сайта.",
        "GitHub / GitLab — при добровольном подключении вами репозитория для деплоя; передаются только данные, необходимые для доступа к указанному репозиторию.",
        "Поставщик хостинга и инфраструктуры в России — размещение серверов и данных.",
        "Мы не продаём персональные данные и не передаём их третьим лицам для целей, не связанных с предоставлением сервиса, за исключением случаев, предусмотренных законом.",
      ],
    },
    {
      heading: "6. Cookie и трекинг",
      body: [
        "Мы используем cookie для аутентификации, сохранения настроек и аналитики. Cookie Яндекс.Метрики и dada_uid применяются для измерения аудитории.",
        "Вы можете отключить аналитические cookie в настройках браузера или через механизмы отказа Яндекс.Метрики. Отключение части cookie может ограничить работу отдельных функций сайта.",
      ],
    },
    {
      heading: "7. Сроки хранения",
      body: [
        "Учётные данные хранятся, пока существует аккаунт. Пользовательский контент хранится, пока вы его не удалите или пока действует аккаунт. Аналитические данные хранятся в сроки, установленные Яндекс.Метрикой. Технические логи хранятся ограниченное время, необходимое для эксплуатации платформы.",
      ],
    },
    {
      heading: "8. Ваши права",
      body: [
        "Вы имеете право получить сведения об обработке ваших данных, потребовать их уточнения, блокирования или удаления, а также отозвать согласие на обработку.",
        `Для реализации прав направьте запрос на ${CONTACT}. Мы ответим в сроки, установленные законодательством.`,
      ],
    },
    {
      heading: "9. Изменения политики",
      body: [
        "Мы можем обновлять настоящую Политику. Актуальная версия всегда доступна на этой странице с указанием даты обновления.",
      ],
    },
  ],
};

const termsRu: LegalDocData = {
  title: "Пользовательское соглашение",
  updated: "Обновлено: 15 июля 2026",
  intro:
    "Настоящее Соглашение регулирует использование облачной платформы DADA Cloud (cloud.dada-tuda.ru). Начиная пользоваться сервисом, вы принимаете его условия.",
  sections: [
    {
      heading: "1. Предмет",
      body: [
        "DADA Cloud предоставляет облачную платформу для развёртывания приложений: подключение репозитория, сборка, деплой, базы данных, домены, HTTPS, хранилище и мониторинг.",
      ],
    },
    {
      heading: "2. Аккаунт",
      body: [
        "Для использования сервиса необходима регистрация. Вы отвечаете за сохранность доступа к аккаунту и за действия, совершённые под ним.",
        "Вы обязуетесь предоставлять достоверные данные при регистрации.",
      ],
    },
    {
      heading: "3. Тарифы",
      body: [
        "Бесплатный тариф включает: 1 приложение, 1 базу данных, 1 ГБ хранилища, 1 домен, 1 окружение и 1 участника команды; резервные копии на бесплатном тарифе не хранятся.",
        "Платные тарифы и их стоимость указаны на странице тарифов. Условия оплаты определяются выбранным тарифом.",
      ],
    },
    {
      heading: "4. Допустимое использование",
      body: [
        "Запрещено размещать противоправный контент, нарушать права третьих лиц, распространять вредоносное ПО, вести массовые рассылки без согласия получателей, проводить атаки, майнинг криптовалют и иные действия, создающие непропорциональную нагрузку или нарушающие закон.",
        "Вы несёте ответственность за код, данные и контент, размещённые вами на платформе.",
      ],
    },
    {
      heading: "5. Доступность и ответственность",
      body: [
        "Сервис предоставляется «как есть». Мы стремимся обеспечить стабильную работу, но не гарантируем бесперебойность, особенно на бесплатном тарифе.",
        "В пределах, допустимых законом, оператор не несёт ответственности за косвенные убытки, потерю данных или упущенную выгоду. Рекомендуем самостоятельно создавать резервные копии важных данных.",
      ],
    },
    {
      heading: "6. Приостановление и прекращение",
      body: [
        "Мы вправе ограничить или прекратить доступ к сервису при нарушении настоящего Соглашения или закона, по возможности предварительно уведомив пользователя.",
        `Вы можете прекратить использование сервиса в любой момент. Для удаления аккаунта и связанных данных направьте запрос на ${CONTACT}.`,
      ],
    },
    {
      heading: "7. Изменения условий",
      body: [
        "Мы можем изменять настоящее Соглашение. Актуальная версия публикуется на этой странице с датой обновления. Продолжение использования сервиса означает согласие с изменениями.",
      ],
    },
    {
      heading: "8. Контакты",
      body: [`По вопросам использования сервиса обращайтесь на ${CONTACT}.`],
    },
  ],
};

const privacyEn: LegalDocData = {
  title: "Privacy Policy",
  updated: "Updated: 15 July 2026",
  intro:
    "This Policy describes what data the DADA Cloud platform (cloud.dada-tuda.ru) processes, why, where it is stored, and what rights you have. Personal data of Russian citizens is processed on servers located in Russia in accordance with Federal Law No. 152-FZ on Personal Data.",
  sections: [
    {
      heading: "1. Operator",
      body: [
        "The operator of personal data processing is the owner of the DADA Cloud service.",
        `For any data-processing questions or to exercise your rights, contact: ${CONTACT}.`,
      ],
    },
    {
      heading: "2. Data we process",
      body: [
        "Account data. On sign-up and login we process your email address, name (display name), username, and an internal user identifier. Authentication runs through a Keycloak-based service.",
        "Repository connection data. When you connect GitHub or GitLab we store the app installation id, account name, and account type. GitHub access tokens are not stored — they are minted per build and short-lived. The GitLab access token is stored encrypted.",
        "Analytics data. The site uses a Yandex.Metrika counter (110158915). It collects cookies, IP address, device and browser data, on-page actions, and session recording (Webvisor: cursor movements, clicks, pages viewed). We also use the dada_uid cookie — a pseudonymous user identifier (UUID) that contains no email, username, or name.",
        "Technical logs and metrics. For operating and observing the platform we process application logs and metrics tied to a project identifier, stored on infrastructure in Russia.",
        "Data you host yourself. Your databases, object-storage files, environment variables, and deployed application source code. The operator does not use this content for its own purposes, stores it on infrastructure in Russia, and restricts access to it.",
        "Communication data. Your account email is used for operational notifications, such as build and deploy results.",
      ],
    },
    {
      heading: "3. Purposes and legal basis",
      body: [
        "Service delivery: account creation and management, authentication, deployments, and management of projects, databases, domains, and storage.",
        "Reliability and security: monitoring, error diagnostics, abuse protection.",
        "Analytics and product improvement: measuring site traffic and behavior in anonymized or pseudonymous form.",
        "Communication: sending operational and service messages.",
        "Legal basis: user consent, performance of the contract (terms of use), and the operator's legitimate interest in running the service.",
      ],
    },
    {
      heading: "4. Where data is stored",
      body: [
        "Personal data and user content are processed and stored on servers located in the Russian Federation, meeting the data-localization requirement for Russian citizens (152-FZ).",
      ],
    },
    {
      heading: "5. Third parties",
      body: [
        "Yandex.Metrika (Yandex LLC) — collection of anonymized site-visit statistics.",
        "GitHub / GitLab — only when you voluntarily connect a repository for deployment; only data needed to access that repository is shared.",
        "Hosting and infrastructure provider in Russia — server and data hosting.",
        "We do not sell personal data and do not share it with third parties for purposes unrelated to providing the service, except as required by law.",
      ],
    },
    {
      heading: "6. Cookies and tracking",
      body: [
        "We use cookies for authentication, saving preferences, and analytics. Yandex.Metrika and dada_uid cookies are used for audience measurement.",
        "You can disable analytics cookies in your browser settings or via Yandex.Metrika opt-out mechanisms. Disabling some cookies may limit certain site features.",
      ],
    },
    {
      heading: "7. Retention",
      body: [
        "Account data is kept while the account exists. User content is kept until you delete it or while the account is active. Analytics data is retained per Yandex.Metrika periods. Technical logs are kept for the limited time needed to operate the platform.",
      ],
    },
    {
      heading: "8. Your rights",
      body: [
        "You have the right to obtain information about the processing of your data, request its correction, blocking, or deletion, and withdraw consent to processing.",
        `To exercise your rights, send a request to ${CONTACT}. We respond within the periods set by law.`,
      ],
    },
    {
      heading: "9. Changes to this policy",
      body: [
        "We may update this Policy. The current version is always available on this page with the update date.",
      ],
    },
  ],
};

const termsEn: LegalDocData = {
  title: "Terms of Use",
  updated: "Updated: 15 July 2026",
  intro:
    "These Terms govern the use of the DADA Cloud platform (cloud.dada-tuda.ru). By using the service you accept them.",
  sections: [
    {
      heading: "1. Subject",
      body: [
        "DADA Cloud provides a cloud platform for deploying applications: repository connection, build, deploy, databases, domains, HTTPS, storage, and monitoring.",
      ],
    },
    {
      heading: "2. Account",
      body: [
        "Using the service requires registration. You are responsible for keeping your account access secure and for actions taken under it.",
        "You agree to provide accurate information when registering.",
      ],
    },
    {
      heading: "3. Plans",
      body: [
        "The free plan includes: 1 application, 1 database, 1 GB of storage, 1 domain, 1 environment, and 1 team member; backups are not retained on the free plan.",
        "Paid plans and their prices are listed on the pricing page. Payment terms depend on the selected plan.",
      ],
    },
    {
      heading: "4. Acceptable use",
      body: [
        "You may not host unlawful content, infringe third-party rights, distribute malware, send unsolicited bulk mail, run attacks, mine cryptocurrency, or take other actions that create disproportionate load or violate the law.",
        "You are responsible for the code, data, and content you place on the platform.",
      ],
    },
    {
      heading: "5. Availability and liability",
      body: [
        "The service is provided “as is”. We aim for stable operation but do not guarantee uninterrupted availability, especially on the free plan.",
        "To the extent permitted by law, the operator is not liable for indirect damages, data loss, or lost profit. We recommend keeping your own backups of important data.",
      ],
    },
    {
      heading: "6. Suspension and termination",
      body: [
        "We may limit or terminate access to the service upon violation of these Terms or the law, giving prior notice where possible.",
        `You may stop using the service at any time. To delete your account and associated data, send a request to ${CONTACT}.`,
      ],
    },
    {
      heading: "7. Changes to the terms",
      body: [
        "We may amend these Terms. The current version is published on this page with the update date. Continued use of the service means acceptance of the changes.",
      ],
    },
    {
      heading: "8. Contact",
      body: [`For service questions, contact ${CONTACT}.`],
    },
  ],
};

export const privacyDoc: Record<Locale, LegalDocData> = { ru: privacyRu, en: privacyEn };
export const termsDoc: Record<Locale, LegalDocData> = { ru: termsRu, en: termsEn };

export function LegalDoc({ doc }: { doc: LegalDocData }) {
  return (
    <section className="mx-auto max-w-3xl px-4 py-16 sm:px-6 lg:py-20">
      <h1 className="text-3xl font-bold tracking-tight text-slate-900 sm:text-4xl">{doc.title}</h1>
      <p className="mt-3 text-sm text-slate-500">{doc.updated}</p>
      <p className="mt-6 text-base leading-relaxed text-slate-700">{doc.intro}</p>
      <div className="mt-10 space-y-8">
        {doc.sections.map((s) => (
          <div key={s.heading}>
            <h2 className="text-lg font-semibold text-slate-900">{s.heading}</h2>
            <div className="mt-2 space-y-3">
              {s.body.map((p, i) => (
                <p key={i} className="text-base leading-relaxed text-slate-700">
                  {p}
                </p>
              ))}
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}
