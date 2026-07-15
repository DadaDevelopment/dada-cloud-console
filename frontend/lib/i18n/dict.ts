// Marketing-site copy dictionary. RU is the default locale (parity with the
// reference cloud-provider landing); EN mirrors it for the language toggle.
// Console UI strings are intentionally NOT here — this is marketing only.

export type Locale = "ru" | "en";

export const LOCALES: Locale[] = ["ru", "en"];
export const DEFAULT_LOCALE: Locale = "ru";

type Feature = { title: string; desc: string };
type Faq = { q: string; a: string };
type AltPage = {
  heroTitle: string;
  heroSubtitle: string;
  featuresTitle: string;
  features: Feature[];
  faqTitle: string;
  faq: Faq[];
};
type Plan = {
  key: string;
  name: string;
  price: string;
  period: string;
  tagline: string;
  features: string[];
  quotaMatrix: {
    apps: string;
    databases: string;
    storage: string;
    domains: string;
    environments: string;
    members: string;
    backups: string;
    support: string;
  };
  cta: string;
  highlight?: boolean;
};
type Step = { num: string; title: string; desc: string };
type Scenario = { tag: string; title: string; desc: string };
type Proof = { quote: string; author: string };
type PricingTeaser = { name: string; price: string; tagline: string; bullets: string[]; highlight?: boolean };

export interface Dict {
  nav: {
    cloud: string;
    servers: string;
    databases: string;
    storage: string;
    pricing: string;
    docs: string;
    how: string;
    login: string;
    register: string;
    console: string;
  };
  common: {
    createAccount: string;
    getStarted: string;
    learnMore: string;
    order: string;
    consult: string;
    perMonth: string;
    beta: string;
    soon: string;
  };
  home: {
    heroBadge: string;
    heroTitle: string;
    heroSubtitle: string;
    heroPrimary: string;
    heroSecondary: string;
    heroTertiary: string;
    stepsTitle: string;
    stepsSubtitle: string;
    steps: Step[];
    valueTitle: string;
    valueSubtitle: string;
    value: Feature[];
    scenariosTitle: string;
    scenariosSubtitle: string;
    scenarios: Scenario[];
    proofTitle: string;
    proofSubtitle: string;
    proof: Proof[];
    pricingTitle: string;
    pricingSubtitle: string;
    pricingTiers: PricingTeaser[];
    pricingNote: string;
    faqTitle: string;
    faq: Faq[];
    ctaTitle: string;
    ctaSubtitle: string;
    mcp: {
      tag: string;
      title: string;
      subtitle: string;
      chat: { role: "user" | "assistant"; text: string }[];
      bullets: string[];
      cta: string;
    };
  };
  servers: {
    heroTitle: string;
    heroSubtitle: string;
    featuresTitle: string;
    features: Feature[];
    faqTitle: string;
    faq: Faq[];
  };
  vercelAlt: AltPage;
  herokuAlt: AltPage;
  railwayAlt: AltPage;
  renderAlt: AltPage;
  databases: {
    heroTitle: string;
    heroSubtitle: string;
    engines: string[];
    featuresTitle: string;
    features: Feature[];
    note: string;
    noteLinkLabel: string;
    faqTitle: string;
    faq: Faq[];
  };
  storage: {
    heroTitle: string;
    heroSubtitle: string;
    featuresTitle: string;
    features: Feature[];
    faqTitle: string;
    faq: Faq[];
  };
  pricing: {
    heroTitle: string;
    heroSubtitle: string;
    plans: Plan[];
    note: string;
    recommender: {
      title: string;
      subtitle: string;
      labelApps: string;
      labelDatabases: string;
      labelDomains: string;
      labelMembers: string;
      labelStorage: string;
      submit: string;
      loading: string;
      result: string;
      errorFallback: string;
    };
  };
  footer: {
    tagline: string;
    productsTitle: string;
    products: { label: string; href: string }[];
    companyTitle: string;
    company: { label: string; href: string }[];
    resourcesTitle: string;
    resources: { label: string; href: string }[];
    rights: string;
    legal: string;
  };
}

const ru: Dict = {
  nav: {
    cloud: "Облако",
    servers: "Серверы",
    databases: "Базы данных",
    storage: "Хранилище",
    pricing: "Цены",
    docs: "Документация",
    how: "Как это работает",
    login: "Вход",
    register: "Регистрация",
    console: "Консоль",
  },
  common: {
    createAccount: "Создать аккаунт",
    getStarted: "Начать",
    learnMore: "Подробнее",
    order: "Заказать",
    consult: "Получить консультацию",
    perMonth: "/мес",
    beta: "Beta",
    soon: "Скоро",
  },
  home: {
    heroBadge: "Backend-облако · деплой из GitHub",
    heroTitle: "Backend из GitHub в проде за пару минут",
    heroSubtitle:
      "Подключаете репозиторий, рядом поднимаете Postgres и домен. Дальше каждый push сам собирается, едет в прод и отдаётся по HTTPS. DevOps-команда для этого не нужна.",
    heroPrimary: "Подключить GitHub",
    heroSecondary: "Смотреть, как работает",
    heroTertiary: "Запросить пилот или миграцию",
    stepsTitle: "От репозитория до прода. Три шага",
    stepsSubtitle: "Без самописного CI/CD и релизов по SSH.",
    steps: [
      { num: "01", title: "Подключите GitHub", desc: "Выбираете репозиторий, дальше платформа сама собирает сервис. Есть Dockerfile — берём его. Нет — соберём из исходников." },
      { num: "02", title: "Добавьте Postgres и домен", desc: "База, домен и сертификат поднимаются здесь же. Не нужно заводить тикет в инфраструктуру и ждать." },
      { num: "03", title: "Катите и откатывайтесь", desc: "Push в ветку = новый деплой. Логи идут вживую. Что-то сломалось — откат на прошлую версию одной кнопкой." },
    ],
    valueTitle: "Что вы получаете",
    valueSubtitle: "По делу, без слоёв конфигов.",
    value: [
      { title: "От push до HTTPS без своего CI/CD", desc: "Запушили в ветку — сервис собрался, выкатился и уже отвечает по HTTPS. Пайплайны под это писать не надо." },
      { title: "База и домен там же, где деплой", desc: "Postgres, домен и TLS живут в том же проекте. DATABASE_URL прилетает в сервис сам, руками строку не собираете." },
      { title: "Мониторинг сразу после деплоя", desc: "Логи, метрики и алерты не нужно подключать отдельно — видно, что упало и где, а уведомление прилетает в Telegram или на почту раньше, чем напишет клиент." },
    ],
    scenariosTitle: "Под ваш случай",
    scenariosSubtitle: "От пет-проекта до растущей команды.",
    scenarios: [
      { tag: "Solo / founder", title: "Запуск сервиса за минуты", desc: "Уже есть VPS с docker-compose? Подключите его по SSH как App Server — платформа берёт сервер под управление и предлагает забрать уже работающие контейнеры без пересборки." },
      { tag: "Стартап 2–10", title: "Команда без DevOps", desc: "Деплой из GitHub, доступы по ролям, общие логи и откаты. Инженеры катят сами. Отдельный ops под это держать не нужно." },
      { tag: "Агентство", title: "Парк клиентских серверов в одной панели", desc: "Подключаете VM каждого клиента по SSH — домены, базы, деплой и мониторинг всех серверов видно из одной панели, без десятка отдельных логинов." },
    ],
    proofTitle: "Истории команд",
    proofSubtitle: "Скоро здесь будут живые кейсы.",
    proof: [
      { quote: "Сидели на VPS с compose, релизились по ssh руками. Перенесли в один проект, теперь просто пушим.", author: "Команда · скоро" },
      { quote: "Раньше поднять новый сервис — это полдня. Сейчас успеваю до обеда выкатить пару штук.", author: "Стартап · скоро" },
      { quote: "Домены и базы клиентов больше не размазаны по десяти панелям. Всё в одном месте, выдыхаю.", author: "Агентство · скоро" },
    ],
    pricingTitle: "Прозрачные планы",
    pricingSubtitle: "Без сюрпризов в счёте: понятные квоты плана и оценка стоимости до деплоя.",
    pricingTiers: [
      { name: "Free", price: "0 ₽", tagline: "Попробовать и пет-проекты", bullets: ["1 приложение", "1 база данных", "Деплой из GitHub", "Базовые логи"] },
      { name: "Startup", price: "990 ₽/мес", tagline: "Один разработчик в продакшене", bullets: ["5 приложений", "2 базы данных", "5 доменов", "Бэкапы 7 дней"], highlight: true },
      { name: "Business", price: "2 900 ₽/мес", tagline: "Растущая команда с продакшеном", bullets: ["20 приложений", "10 баз данных", "Бэкапы 30 дней", "Приоритетная поддержка"] },
    ],
    pricingNote: "Полная таблица тарифов — на странице цен.",
    faqTitle: "Частые возражения",
    faq: [
      { q: "У меня уже есть VPS", a: "VPS вы тащите сами: обновления ОС, бэкапы, деплой-скрипты, ночные SSH-сессии когда что-то легло. Подключите его к платформе по SSH одним разом — мы поставим Docker и агента, а контейнеры, которые на нём уже работают, можно забрать в управляемые приложения без пересборки. Переносить данные никуда не нужно." },
      { q: "У нас есть GitHub Actions", a: "Actions соберут артефакт, и на этом всё. Дальше его надо куда-то выкатить, поднять базу, прицепить домен, придумать откат. Вот этот кусок от push до живого HTTPS-сервиса мы и берём на себя. Свой CD-пайплайн писать не придётся." },
      { q: "Мы ещё маленькие", a: "Тем более ваш вариант. Один проект вместо ручной инфраструктуры, которую в таком размере держать нечем. Sandbox бесплатный, квоты плана держат счёт под контролем, а вырастете — переезжать не надо." },
      { q: "Чем это отличается от Heroku, Vercel и Coolify?", a: "DADA Cloud — backend-облако уровня PaaS: как Heroku или Render, вы деплоите бэкенд из GitHub одним push. В отличие от Vercel, заточенного под фронтенд, здесь первичен долгоживущий бэкенд с управляемым Postgres. А в отличие от self-hosted Coolify, свой сервер администрировать не нужно — хотя существующий VPS можно подключить по SSH и вести из той же панели." },
    ],
    ctaTitle: "Поднимите backend из GitHub сегодня",
    ctaSubtitle: "Подключение репозитория занимает минуту. Платите только за то, что реально потребляете.",
    mcp: {
      tag: "Новое · MCP",
      title: "Управляйте облаком прямо из Claude",
      subtitle:
        "Подключите платформу к Claude одной командой — и просите его словами: подними сервер, разверни приложение, покажи логи. Он сделает и отчитается.",
      chat: [
        { role: "user", text: "Claude, подними сервер под API и разверни туда приложение из моего репозитория" },
        { role: "assistant", text: "Создаю сервер, беру репозиторий, собираю образ…" },
        { role: "assistant", text: "Готово, сэр. Приложение живёт по HTTPS, база и домен на месте." },
      ],
      bullets: [
        "132 действия платформы прямо в чате",
        "Вход через браузер — ни токенов, ни ключей вручную",
        "Работает только с вашими проектами, по вашим правам",
      ],
      cta: "Как подключить",
    },
  },
  servers: {
    heroTitle: "Свой сервер — под управлением. Или закажите новый",
    heroSubtitle:
      "SSH-доступ — и через пару минут сервер под управлением: деплой, логи и метрики видно из панели, а контейнеры, которые там уже крутятся, можно забрать без пересборки. Не хотите переносить свой — закажем новую VM. Self-host гибкость Coolify, но без администрирования сервера вручную.",
    featuresTitle: "Что вы получаете с App Servers",
    features: [
      { title: "Подключите свой сервер по SSH", desc: "Один SSH-заход, чтобы поставить Docker и лёгкого edge-агента. Ключ используется один раз и нигде не сохраняется." },
      { title: "Заберите то, что уже работает", desc: "Discovery находит контейнеры, уже запущенные на сервере, и предлагает импортировать их в управляемые приложения — без остановки и пересборки, тома с данными сохраняются." },
      { title: "Или закажите у нас новую VM", desc: "Не хотите переносить свой сервер — выберите конфигурацию, регион и образ ОС в панели, и мы поднимем VM за вас." },
      { title: "Один сервер или парк клиентских VM", desc: "Агентство ведёт VM всех клиентов в одной панели: статус, логи и метрики каждого сервера — без десятка отдельных логинов и SSH-сессий." },
      { title: "Docker Compose как есть", desc: "Деплой идёт через ваш существующий docker-compose.yml — переписывать под платформу не нужно." },
      { title: "Логи и метрики сервера сразу видно", desc: "CPU, память, диск и логи контейнеров — в панели сразу после подключения, отдельно настраивать мониторинг не нужно." },
    ],
    faqTitle: "Частые вопросы",
    faq: [
      { q: "Вы получаете постоянный доступ к моему серверу?", a: "Нет. SSH-ключ нужен один раз — поставить Docker и edge-агента. Дальше платформа управляет сервером через агента, ключ нигде не хранится." },
      { q: "Что будет с контейнерами, которые уже работают на сервере?", a: "Discovery покажет их в режиме только чтения. Если решите импортировать — каждый сервис станет отдельным приложением со своими логами и метриками, тома с данными сохранятся." },
      { q: "Можно вести серверы нескольких клиентов в одной панели?", a: "Да, это основной сценарий для агентств: сервер каждого клиента подключается отдельно, а деплой, логи и мониторинг видны из одной панели." },
      { q: "Обязательно переносить сервер, если он уже работает?", a: "Нет. Можно подключить существующий сервер как есть — переносить ничего не нужно. Либо закажите у нас новую VM, если хотите начать с чистого листа." },
    ],
  },
  vercelAlt: {
    heroTitle: "Аналог Vercel, который работает в России",
    heroSubtitle:
      "Vercel и Railway удобны, но оплатить их российской картой и держать данные в РФ не выйдет. Dada Cloud закрывает ровно этот сценарий: подключаете GitHub-репозиторий, пуш — и через минуты живой HTTPS-адрес. Без VPN, оплата рублями, серверы в России.",
    featuresTitle: "Почему на Dada Cloud переходят с Vercel и Railway",
    features: [
      { title: "Оплата рублёвой картой", desc: "Российская карта, счёт и закрывающие документы. Не нужны зарубежная карта, посредники и обходные схемы оплаты подписки." },
      { title: "Работает без VPN", desc: "И панель, и задеплоенные приложения открываются из России напрямую — ни вам, ни вашим пользователям не нужен VPN, чтобы зайти." },
      { title: "Данные в России (152-ФЗ)", desc: "Приложения и базы размещаются на серверах в РФ — это то, что требует закон о персональных данных и чего зарубежный хостинг структурно закрыть не может." },
      { title: "Деплой из GitHub, как вы привыкли", desc: "Тот же флоу, что на Vercel: подключаете репозиторий, пуш в основную ветку — автоматическая пересборка и деплой. Фреймворк определяется сам." },
      { title: "HTTPS-домен из коробки", desc: "Сразу после деплоя приложение доступно по адресу с валидным TLS-сертификатом. Свой домен подключается в пару шагов, сертификат выпускается автоматически." },
      { title: "Postgres и хранилище рядом", desc: "Не только фронтенд: управляемый PostgreSQL и S3-хранилище создаются рядом с приложением, строка подключения прокидывается в сервис сама." },
    ],
    faqTitle: "Vercel и Россия — частые вопросы",
    faq: [
      { q: "Работает ли Vercel в России?", a: "Сам сайт и задеплоенные проекты чаще всего открываются, но оплатить платный тариф российской картой нельзя, а данные хранятся за рубежом — что не соответствует требованиям 152-ФЗ для персональных данных россиян. Dada Cloud решает обе проблемы: оплата рублями и серверы в РФ." },
      { q: "Чем заменить Vercel для оплаты рублями?", a: "Dada Cloud — российская платформа с тем же сценарием «из GitHub в прод»: оплата рублёвой картой, счёт и закрывающие документы для юрлиц, без зарубежных карт и посредников." },
      { q: "Нужно ли переписывать проект при переходе с Vercel?", a: "Нет. Вы подключаете тот же GitHub-репозиторий, платформа сама определяет фреймворк и собирает проект. Пуш в основную ветку пересобирает и деплоит приложение автоматически." },
      { q: "Есть ли база данных, как в связке Vercel + внешний Postgres?", a: "Да. Управляемый PostgreSQL создаётся прямо в платформе рядом с приложением, DATABASE_URL прокидывается в сервис автоматически — отдельный внешний провайдер базы не нужен." },
      { q: "Данные точно хранятся в России?", a: "Да. Приложения и базы размещаются на серверах в РФ, что соответствует требованиям 152-ФЗ и 242-ФЗ о хранении персональных данных граждан России на территории страны." },
    ],
  },
  herokuAlt: {
    heroTitle: "Аналог Heroku для России",
    heroSubtitle:
      "Heroku убил бесплатный тариф, а платный не оплатить российской картой. Dada Cloud даёт тот же путь «git push → живое приложение»: подключаете GitHub-репозиторий, пуш — и через минуты HTTPS-адрес. Оплата рублями, серверы в России, без VPN.",
    featuresTitle: "Почему разработчики переезжают с Heroku на Dada Cloud",
    features: [
      { title: "Оплата рублёвой картой", desc: "Российская карта, счёт и закрывающие документы для юрлиц. Не нужны зарубежная карта и посредники, чтобы оплатить хостинг." },
      { title: "Деплой из GitHub без Procfile-магии", desc: "Подключаете репозиторий, пуш в основную ветку — платформа сама определяет фреймворк, собирает и деплоит. Buildpack'и настраивать не нужно." },
      { title: "Managed PostgreSQL рядом", desc: "Аналог Heroku Postgres: управляемая база создаётся рядом с приложением, DATABASE_URL прокидывается в сервис автоматически." },
      { title: "Приложения не «засыпают»", desc: "В отличие от бесплатных dyno, приложение остаётся живым и отвечает без задержки на первый запрос." },
      { title: "Данные в России (152-ФЗ)", desc: "Приложения и базы размещаются на серверах в РФ — то, что требует закон о персональных данных и чего Heroku структурно не даёт." },
      { title: "HTTPS-домен из коробки", desc: "Сразу после деплоя приложение доступно по адресу с валидным TLS-сертификатом. Свой домен подключается в пару шагов." },
    ],
    faqTitle: "Замена Heroku в России — частые вопросы",
    faq: [
      { q: "Работает ли Heroku в России сейчас?", a: "Бесплатных тарифов у Heroku больше нет, а платные нельзя оплатить российской картой. Данные хранятся за рубежом, что не соответствует 152-ФЗ. Dada Cloud закрывает оба вопроса: оплата рублями и серверы в РФ." },
      { q: "Чем заменить Heroku с оплатой рублями?", a: "Dada Cloud — российская платформа с тем же сценарием «git push → прод»: оплата рублёвой картой, счёт и закрывающие документы, без зарубежных карт и посредников." },
      { q: "Нужно ли переписывать приложение при переходе с Heroku?", a: "Нет. Вы подключаете тот же GitHub-репозиторий, платформа сама определяет фреймворк и собирает проект. Procfile и buildpack'и настраивать не требуется." },
      { q: "Есть ли аналог Heroku Postgres?", a: "Да. Управляемый PostgreSQL создаётся прямо в платформе рядом с приложением, а строка подключения DATABASE_URL прокидывается в сервис автоматически." },
      { q: "Будут ли приложения «засыпать», как бесплатные dyno?", a: "Нет. Приложение остаётся запущенным и отвечает без холодного старта на первый запрос." },
    ],
  },
  railwayAlt: {
    heroTitle: "Аналог Railway, который работает в России",
    heroSubtitle:
      "Railway удобен, но для оплаты нужна зарубежная карта, а данные лежат за границей. Dada Cloud повторяет тот же опыт: подключаете GitHub-репозиторий, пуш — и через минуты живой HTTPS-адрес с базой рядом. Оплата рублями, серверы в России, без VPN.",
    featuresTitle: "Почему переходят с Railway на Dada Cloud",
    features: [
      { title: "Оплата рублёвой картой", desc: "Российская карта, счёт и закрывающие документы. Не нужны зарубежная карта, посредники и обходные схемы оплаты по usage-billing." },
      { title: "Деплой из GitHub, как в Railway", desc: "Подключаете репозиторий, пуш в основную ветку — автоматическая пересборка и деплой. Фреймворк определяется сам." },
      { title: "PostgreSQL и хранилище рядом", desc: "Как в Railway: управляемый PostgreSQL и S3-хранилище создаются рядом с приложением, строка подключения прокидывается в сервис автоматически." },
      { title: "Работает без VPN", desc: "И панель, и задеплоенные приложения открываются из России напрямую — ни вам, ни вашим пользователям не нужен VPN." },
      { title: "Данные в России (152-ФЗ)", desc: "Приложения и базы размещаются на серверах в РФ — то, что требует закон о персональных данных." },
      { title: "Предсказуемая цена в рублях", desc: "Тарифы в рублях вместо usage-billing в долларах — расходы понятны заранее и не привязаны к курсу." },
    ],
    faqTitle: "Замена Railway в России — частые вопросы",
    faq: [
      { q: "Работает ли Railway в России?", a: "Панель и задеплоенные проекты обычно открываются, но оплатить тариф российской картой нельзя, а данные хранятся за рубежом — что не соответствует 152-ФЗ. Dada Cloud решает обе проблемы: оплата рублями и серверы в РФ." },
      { q: "Чем заменить Railway для оплаты рублями?", a: "Dada Cloud — российская платформа с тем же сценарием «из GitHub в прод»: оплата рублёвой картой, счёт и закрывающие документы для юрлиц, без зарубежных карт." },
      { q: "Нужно ли переписывать проект при переходе с Railway?", a: "Нет. Вы подключаете тот же GitHub-репозиторий, платформа сама определяет фреймворк и собирает проект. Пуш в основную ветку пересобирает и деплоит приложение автоматически." },
      { q: "Есть ли база данных, как плагин Postgres в Railway?", a: "Да. Управляемый PostgreSQL создаётся рядом с приложением, DATABASE_URL прокидывается в сервис автоматически — отдельный внешний провайдер не нужен." },
      { q: "Чем цена отличается от usage-billing Railway?", a: "Тарифы фиксированы в рублях, а не считаются по usage в долларах — расходы предсказуемы и не зависят от курса валют." },
    ],
  },
  renderAlt: {
    heroTitle: "Аналог Render в России",
    heroSubtitle:
      "Render удобен для деплоя веб-сервисов и баз, но оплатить его российской картой не выйдет, а данные лежат за границей. Dada Cloud даёт тот же путь: подключаете GitHub-репозиторий, пуш — и через минуты живой HTTPS-адрес с managed PostgreSQL рядом. Оплата рублями, серверы в России, без VPN.",
    featuresTitle: "Почему переходят с Render на Dada Cloud",
    features: [
      { title: "Оплата рублёвой картой", desc: "Российская карта, счёт и закрывающие документы для юрлиц. Не нужны зарубежная карта и посредники." },
      { title: "Деплой из GitHub, как в Render", desc: "Подключаете репозиторий, пуш в основную ветку — автоматическая пересборка и деплой. Фреймворк определяется сам." },
      { title: "Managed PostgreSQL рядом", desc: "Как Render PostgreSQL: управляемая база создаётся рядом с приложением, DATABASE_URL прокидывается в сервис автоматически." },
      { title: "Приложения не «засыпают»", desc: "В отличие от бесплатного тарифа Render, приложение остаётся живым и отвечает без задержки на первый запрос." },
      { title: "Данные в России (152-ФЗ)", desc: "Приложения и базы размещаются на серверах в РФ — то, что требует закон о персональных данных." },
      { title: "HTTPS-домен из коробки", desc: "Сразу после деплоя приложение доступно по адресу с валидным TLS-сертификатом. Свой домен подключается в пару шагов." },
    ],
    faqTitle: "Замена Render в России — частые вопросы",
    faq: [
      { q: "Работает ли Render в России?", a: "Панель и задеплоенные сервисы обычно открываются, но оплатить тариф российской картой нельзя, а данные хранятся за рубежом — что не соответствует 152-ФЗ. Dada Cloud решает обе проблемы: оплата рублями и серверы в РФ." },
      { q: "Чем заменить Render с оплатой рублями?", a: "Dada Cloud — российская платформа с тем же сценарием «из GitHub в прод»: оплата рублёвой картой, счёт и закрывающие документы для юрлиц, без зарубежных карт." },
      { q: "Нужно ли переписывать проект при переходе с Render?", a: "Нет. Вы подключаете тот же GitHub-репозиторий, платформа сама определяет фреймворк и собирает проект. Пуш в основную ветку пересобирает и деплоит приложение автоматически." },
      { q: "Есть ли аналог Render PostgreSQL?", a: "Да. Управляемый PostgreSQL создаётся рядом с приложением, DATABASE_URL прокидывается в сервис автоматически — отдельный внешний провайдер не нужен." },
      { q: "Будут ли сервисы «засыпать», как на бесплатном Render?", a: "Нет. Приложение остаётся запущенным и отвечает без холодного старта на первый запрос." },
    ],
  },
  databases: {
    heroTitle: "Управляемый PostgreSQL",
    heroSubtitle:
      "Создаётся рядом с приложением, DATABASE_URL прилетает в сервис сам. Бэкапы настраиваются при создании, мониторинг и обновления — на нас.",
    engines: ["PostgreSQL"],
    featuresTitle: "Что входит в управляемый Postgres",
    features: [
      { title: "Бэкапы по расписанию", desc: "Резервное копирование настраивается при создании базы, расписание видно в панели." },
      { title: "Мониторинг производительности", desc: "Метрики подключений, запросов и нагрузки в реальном времени." },
      { title: "Безопасный доступ", desc: "Приватные сети, доступ по ролям и шифрование соединений." },
      { title: "Масштабирование", desc: "Меняйте ресурсы инстанса без миграции данных вручную." },
    ],
    note: "Нужны MySQL или Redis? Managed Postgres — здесь, MySQL и Redis разворачиваются на своём сервере.",
    noteLinkLabel: "Серверы (App Servers) →",
    faqTitle: "Частые вопросы о базах данных",
    faq: [
      { q: "Какие СУБД доступны как управляемые?", a: "Управляемый вариант — PostgreSQL. Он создаётся рядом с приложением, а строку подключения DATABASE_URL платформа прокидывает в сервис автоматически. MySQL и Redis запускаются на вашем сервере (App Server), а не как managed-ресурс." },
      { q: "Нужно ли собирать строку подключения вручную?", a: "Нет. При создании базы вы привязываете её к приложению, и DATABASE_URL появляется в переменных окружения сервиса сам — DSN руками собирать не нужно." },
      { q: "Есть ли резервные копии?", a: "Да. Бэкапы включаются при создании базы: вы выбираете расписание (почасовое или ежедневное) и срок хранения — 7, 14 или 30 дней." },
      { q: "Можно ли менять ресурсы базы без миграции?", a: "Да. Ресурсы инстанса меняются без ручного переноса данных — приложение продолжает использовать ту же строку подключения." },
    ],
  },
  storage: {
    heroTitle: "Объектное хранилище S3",
    heroSubtitle:
      "S3-совместимое хранилище для бэкапов, медиа и статики. Сейчас в статусе Beta — часть возможностей ещё дорабатывается.",
    featuresTitle: "Возможности хранилища",
    features: [
      { title: "S3-совместимый API", desc: "Работает с привычными SDK и инструментами (aws-cli, s3cmd и др.)." },
      { title: "Оплата за объём", desc: "Платите только за реально занятое место и трафик." },
    ],
    faqTitle: "Вопросы об объектном хранилище",
    faq: [
      { q: "В каком статусе сейчас объектное хранилище?", a: "В статусе Beta. Создание S3-совместимых бакетов работает, часть возможностей ещё дорабатывается. Не закладывайте его в критичные сценарии, пока хранилище не вышло из беты." },
      { q: "С какими инструментами оно совместимо?", a: "API S3-совместимый, поэтому хранилище работает с привычными инструментами — aws-cli, s3cmd и любыми SDK, которые умеют в S3." },
      { q: "Как считается оплата?", a: "По объёму: вы платите за реально занятое место и трафик, а не за фиксированный тариф диска." },
    ],
  },
  pricing: {
    heroTitle: "Простые и прозрачные цены",
    heroSubtitle: "Платите за план, а не за каждый ресурс. Без скрытых платежей.",
    plans: [
      {
        key: "free",
        name: "Free",
        price: "0 ₽",
        period: "",
        tagline: "Попробовать и пет-проекты",
        features: [
          "1 приложение",
          "1 база данных",
          "1 ГБ хранилища",
          "1 домен",
          "1 среда",
          "1 участник",
          "Без резервных копий",
          "Поддержка сообщества",
        ],
        quotaMatrix: {
          apps: "1",
          databases: "1",
          storage: "1 ГБ",
          domains: "1",
          environments: "1",
          members: "1",
          backups: "Нет",
          support: "Сообщество",
        },
        cta: "Создать аккаунт",
      },
      {
        key: "startup",
        name: "Startup",
        price: "990 ₽",
        period: "/мес",
        tagline: "Один разработчик или небольшая команда",
        features: [
          "5 приложений",
          "2 базы данных",
          "10 ГБ хранилища",
          "5 доменов",
          "2 среды",
          "3 участника",
          "Бэкапы 7 дней",
          "Поддержка по email",
        ],
        quotaMatrix: {
          apps: "5",
          databases: "2",
          storage: "10 ГБ",
          domains: "5",
          environments: "2",
          members: "3",
          backups: "7 дней",
          support: "Email",
        },
        cta: "Начать",
        highlight: true,
      },
      {
        key: "business",
        name: "Business",
        price: "2 900 ₽",
        period: "/мес",
        tagline: "Растущая команда с нагрузкой в продакшене",
        features: [
          "20 приложений",
          "10 баз данных",
          "100 ГБ хранилища",
          "20 доменов",
          "5 сред",
          "10 участников",
          "Бэкапы 30 дней",
          "Приоритетная поддержка",
        ],
        quotaMatrix: {
          apps: "20",
          databases: "10",
          storage: "100 ГБ",
          domains: "20",
          environments: "5",
          members: "10",
          backups: "30 дней",
          support: "Приоритетная",
        },
        cta: "Начать",
      },
      {
        key: "enterprise",
        name: "Enterprise",
        price: "По запросу",
        period: "",
        tagline: "Индивидуальные квоты и SLA",
        features: [
          "Любое количество приложений",
          "Неограниченные базы данных",
          "Хранилище по договорённости",
          "Неограниченные домены",
          "Неограниченные среды",
          "Неограниченные участники",
          "Бэкапы по договорённости",
          "Приоритетная поддержка + SLA",
        ],
        quotaMatrix: {
          apps: "Без ограничений",
          databases: "Без ограничений",
          storage: "По договорённости",
          domains: "Без ограничений",
          environments: "Без ограничений",
          members: "Без ограничений",
          backups: "По договорённости",
          support: "Приоритет + SLA",
        },
        cta: "Связаться с нами",
      },
    ],
    note: "",
    recommender: {
      title: "Подобрать план",
      subtitle: "Укажите ваши потребности — мы подберём подходящий план.",
      labelApps: "Приложений",
      labelDatabases: "Баз данных",
      labelDomains: "Доменов",
      labelMembers: "Участников команды",
      labelStorage: "Хранилище (ГБ)",
      submit: "Подобрать",
      loading: "Подбираем…",
      result: "Вам подойдёт:",
      errorFallback: "Не удалось связаться с сервером — используем локальный расчёт.",
    },
  },
  footer: {
    tagline: "Backend-облако: из GitHub в прод за минуты.",
    productsTitle: "Продукты",
    products: [
      { label: "Серверы", href: "/cloud-servers" },
      { label: "Базы данных", href: "/databases" },
      { label: "Объектное хранилище", href: "/storage" },
      { label: "Аналог Vercel", href: "/analog-vercel" },
      { label: "Аналог Heroku", href: "/analog-heroku" },
      { label: "Аналог Railway", href: "/analog-railway" },
      { label: "Аналог Render", href: "/analog-render" },
      { label: "Цены", href: "/pricing" },
    ],
    companyTitle: "Компания",
    company: [
      { label: "О платформе", href: "/" },
      { label: "Консоль", href: "/projects" },
      { label: "Вход", href: "/login" },
      { label: "DADA Development", href: "https://development.dada-tuda.ru/" },
      { label: "AgentSync Hub", href: "https://a2a-hub.pro/" },
    ],
    resourcesTitle: "Ресурсы",
    resources: [
      { label: "Документация", href: "/developer" },
      { label: "API", href: "/developer" },
      { label: "Статус", href: "/developer" },
    ],
    rights: "Все права защищены.",
    legal: "DADA Cloud — облачная платформа.",
  },
};

const en: Dict = {
  nav: {
    cloud: "Cloud",
    servers: "Servers",
    databases: "Databases",
    storage: "Storage",
    pricing: "Pricing",
    docs: "Docs",
    how: "How it works",
    login: "Log in",
    register: "Sign up",
    console: "Console",
  },
  common: {
    createAccount: "Create account",
    getStarted: "Get started",
    learnMore: "Learn more",
    order: "Order",
    consult: "Contact sales",
    perMonth: "/mo",
    beta: "Beta",
    soon: "Soon",
  },
  home: {
    heroBadge: "Backend cloud · deploy from GitHub",
    heroTitle: "Your backend from GitHub, live in a couple of minutes",
    heroSubtitle:
      "Connect a repo, spin up Postgres and a domain next to it. From there every push builds itself, ships to prod and serves over HTTPS. You don't need a DevOps team for this.",
    heroPrimary: "Connect GitHub",
    heroSecondary: "See how it works",
    heroTertiary: "Request a pilot or migration",
    stepsTitle: "From repo to prod. Three steps",
    stepsSubtitle: "No homegrown CI/CD, no releases over SSH.",
    steps: [
      { num: "01", title: "Connect GitHub", desc: "Pick a repository and the platform builds the service for you. Got a Dockerfile? We use it. No Dockerfile? We build from source." },
      { num: "02", title: "Add Postgres and a domain", desc: "Database, domain and certificate come up right here. No infrastructure ticket to file and wait on." },
      { num: "03", title: "Ship and roll back", desc: "Push to a branch = a new deploy. Logs stream live. Something broke? Roll back to the previous version with one button." },
    ],
    valueTitle: "What you get",
    valueSubtitle: "Straight to the point, no layers of config.",
    value: [
      { title: "From push to HTTPS without your own CI/CD", desc: "Push to a branch and the service is built, shipped and already answering over HTTPS. No pipelines to write for it." },
      { title: "Database and domain right where you deploy", desc: "Postgres, domain and TLS live in the same project. DATABASE_URL lands in the service on its own — you don't assemble the string by hand." },
      { title: "Monitoring the moment you deploy", desc: "Logs, metrics and alerts don't need separate wiring — see what broke and where, with a notification in Telegram or email before a client has to tell you." },
    ],
    scenariosTitle: "Built for your case",
    scenariosSubtitle: "From a pet project to a growing team.",
    scenarios: [
      { tag: "Solo / founder", title: "Launch a service in minutes", desc: "Already have a VPS running docker-compose? Connect it over SSH as an App Server — the platform takes it under management and offers to adopt the containers already running there, no rebuild." },
      { tag: "Startup 2–10", title: "A team without DevOps", desc: "Deploy from GitHub, role-based access, shared logs and rollbacks. Engineers ship themselves. No dedicated ops needed for it." },
      { tag: "Agency", title: "A whole client fleet, one panel", desc: "Connect each client's VM over SSH — domains, databases, deploys and monitoring for every server show up in one panel, no juggling a dozen separate logins." },
    ],
    proofTitle: "Team stories",
    proofSubtitle: "Real cases coming here soon.",
    proof: [
      { quote: "We were on a VPS with compose, releasing over ssh by hand. Moved it into one project, now we just push.", author: "Team · soon" },
      { quote: "Standing up a new service used to eat half a day. Now I ship a couple before lunch.", author: "Startup · soon" },
      { quote: "Client domains and databases aren't smeared across ten panels anymore. One place, I can breathe.", author: "Agency · soon" },
    ],
    pricingTitle: "Transparent plans",
    pricingSubtitle: "No billing surprises: clear plan quotas and a cost estimate before you deploy.",
    pricingTiers: [
      { name: "Free", price: "$0", tagline: "Trials and pet projects", bullets: ["1 application", "1 database", "Deploy from GitHub", "Basic logs"] },
      { name: "Startup", price: "$12/mo", tagline: "Solo developer in production", bullets: ["5 applications", "2 databases", "5 domains", "7-day backups"], highlight: true },
      { name: "Business", price: "$35/mo", tagline: "Growing team with production load", bullets: ["20 applications", "10 databases", "30-day backups", "Priority support"] },
    ],
    pricingNote: "Full pricing table on the pricing page.",
    faqTitle: "Common objections",
    faq: [
      { q: "I already have a VPS", a: "A VPS is on you: OS updates, backups, deploy scripts, the late-night SSH session when something falls over. Connect it to the platform over SSH once — we install Docker and an agent, and containers already running on it can be adopted into managed applications with no rebuild. Nothing to migrate." },
      { q: "We have GitHub Actions", a: "Actions build an artifact and that's where they stop. You still have to ship it somewhere, stand up a database, attach a domain, figure out rollback. That stretch from push to a live HTTPS service is the part we take on. No CD pipeline of your own to write." },
      { q: "We're still small", a: "All the more reason. One project instead of manual infrastructure you've got no one to run at this size. Sandbox is free, plan quotas keep the bill in check, and when you grow there's no migration to do." },
      { q: "How is this different from Heroku, Vercel and Coolify?", a: "DADA Cloud is a PaaS-grade backend cloud: like Heroku or Render, you deploy a backend from GitHub with a single push. Unlike Vercel, which is frontend-first, the long-running backend with managed Postgres is the primary object here. And unlike self-hosted Coolify, you don't administer your own server — though you can connect an existing VPS over SSH and run it from the same panel." },
    ],
    ctaTitle: "Get your backend live from GitHub today",
    ctaSubtitle: "Connecting a repo takes a minute. You only pay for what you actually use.",
    mcp: {
      tag: "New · MCP",
      title: "Run your cloud straight from Claude",
      subtitle:
        "Connect the platform to Claude with one command, then just ask: spin up a server, deploy an app, show me the logs. It does it and reports back.",
      chat: [
        { role: "user", text: "Claude, spin up a server for my API and deploy the app from my repo" },
        { role: "assistant", text: "Creating the server, pulling the repo, building the image…" },
        { role: "assistant", text: "Done, sir. The app is live over HTTPS, database and domain in place." },
      ],
      bullets: [
        "132 platform actions right in the chat",
        "Browser login — no tokens or keys to paste",
        "Scoped to your projects, under your permissions",
      ],
      cta: "How to connect",
    },
  },
  servers: {
    heroTitle: "Your server, under management. Or order a new one",
    heroSubtitle:
      "Give us SSH access and in a couple of minutes the server is under management: deploys, logs and metrics show up in the panel, and containers already running there import without a rebuild. Don't want to migrate your own — we'll provision a new VM instead. The self-host flexibility of Coolify, without administering the server by hand.",
    featuresTitle: "What you get with App Servers",
    features: [
      { title: "Connect your server over SSH", desc: "One SSH session installs Docker and a lightweight edge agent. The key is used once and is never stored." },
      { title: "Adopt what's already running", desc: "Discovery finds containers already running on the server and offers to import them as managed applications — no downtime, no rebuild, data volumes are preserved." },
      { title: "Or order a new VM from us", desc: "Don't want to migrate your own server — pick the flavor, region and OS image in the panel and we'll provision the VM for you." },
      { title: "One server or a whole client fleet", desc: "An agency runs every client VM from one panel: status, logs and metrics per server — no more juggling a dozen separate logins and SSH sessions." },
      { title: "Docker Compose as-is", desc: "Deploys run on your existing docker-compose.yml — nothing to rewrite for the platform." },
      { title: "Server logs and metrics right away", desc: "CPU, memory, disk and container logs show up in the panel right after connecting — no separate monitoring setup." },
    ],
    faqTitle: "FAQ",
    faq: [
      { q: "Do you keep permanent access to my server?", a: "No. The SSH key is only needed once — to install Docker and the edge agent. After that the platform manages the server through the agent, and the key isn't stored anywhere." },
      { q: "What happens to containers already running on the server?", a: "Discovery lists them read-only. If you choose to import, each service becomes its own application with its own logs and metrics, and data volumes are preserved." },
      { q: "Can I run multiple clients' servers from one panel?", a: "Yes — that's the main agency scenario: each client's server connects separately, and deploys, logs and monitoring all show up in one panel." },
      { q: "Do I have to migrate my server if it already works?", a: "No. You can connect the existing server as-is — nothing to migrate. Or order a fresh VM from us if you'd rather start clean." },
    ],
  },
  vercelAlt: {
    heroTitle: "A Vercel alternative that works in Russia",
    heroSubtitle:
      "Vercel and Railway are convenient, but you can't pay for them with a Russian card or keep data inside Russia. Dada Cloud covers exactly that: connect a GitHub repo, push, and get a live HTTPS URL in minutes. No VPN, ruble payments, servers in Russia.",
    featuresTitle: "Why teams move from Vercel and Railway",
    features: [
      { title: "Pay with a Russian card", desc: "A Russian card, an invoice and closing documents. No foreign card, intermediaries or workarounds to pay for the subscription." },
      { title: "Works without a VPN", desc: "Both the panel and your deployed apps open from Russia directly — neither you nor your users need a VPN to reach them." },
      { title: "Data in Russia (152-FZ)", desc: "Apps and databases run on servers inside Russia — what the personal-data law requires and what foreign hosting structurally can't offer." },
      { title: "Deploy from GitHub, the way you know", desc: "The same flow as Vercel: connect a repo, push to the main branch, and it rebuilds and deploys automatically. The framework is detected for you." },
      { title: "HTTPS domain out of the box", desc: "Right after the deploy the app is reachable over a valid TLS certificate. Your own domain connects in a couple of steps, the certificate is issued automatically." },
      { title: "Postgres and storage alongside", desc: "Not just the frontend: managed PostgreSQL and S3 storage are created next to the app, and the connection string is injected into the service automatically." },
    ],
    faqTitle: "Vercel and Russia — FAQ",
    faq: [
      { q: "Does Vercel work in Russia?", a: "The site and deployed projects usually open, but you can't pay for a paid plan with a Russian card, and data is stored abroad — which doesn't meet 152-FZ requirements for Russian citizens' personal data. Dada Cloud solves both: ruble payments and servers in Russia." },
      { q: "What can replace Vercel for ruble payments?", a: "Dada Cloud is a Russian platform with the same GitHub-to-production flow: pay with a Russian card, get an invoice and closing documents for legal entities, no foreign cards or intermediaries." },
      { q: "Do I have to rewrite my project when moving from Vercel?", a: "No. You connect the same GitHub repo, the platform detects the framework and builds the project. A push to the main branch rebuilds and deploys the app automatically." },
      { q: "Is there a database, like Vercel plus an external Postgres?", a: "Yes. Managed PostgreSQL is created inside the platform next to the app, and DATABASE_URL is injected into the service automatically — no separate external database provider needed." },
      { q: "Is data really stored in Russia?", a: "Yes. Apps and databases run on servers in Russia, meeting the 152-FZ and 242-FZ requirements to store Russian citizens' personal data within the country." },
    ],
  },
  herokuAlt: {
    heroTitle: "A Heroku alternative for Russia",
    heroSubtitle:
      "Heroku killed its free tier and its paid plans can't be paid with a Russian card. Dada Cloud gives you the same git-push-to-live-app flow: connect a GitHub repo, push, and get an HTTPS URL in minutes. Ruble payments, servers in Russia, no VPN.",
    featuresTitle: "Why developers move from Heroku to Dada Cloud",
    features: [
      { title: "Pay with a Russian card", desc: "A Russian card, an invoice and closing documents for legal entities. No foreign card or intermediaries to pay for hosting." },
      { title: "Deploy from GitHub, no Procfile magic", desc: "Connect a repo, push to the main branch — the platform detects the framework, builds and deploys. No buildpacks to configure." },
      { title: "Managed PostgreSQL alongside", desc: "Like Heroku Postgres: a managed database is created next to the app and DATABASE_URL is injected into the service automatically." },
      { title: "Apps don't sleep", desc: "Unlike free dynos, your app stays alive and responds without a cold start on the first request." },
      { title: "Data in Russia (152-FZ)", desc: "Apps and databases run on servers inside Russia — what the personal-data law requires and what Heroku structurally can't offer." },
      { title: "HTTPS domain out of the box", desc: "Right after the deploy the app is reachable over a valid TLS certificate. Your own domain connects in a couple of steps." },
    ],
    faqTitle: "Replacing Heroku in Russia — FAQ",
    faq: [
      { q: "Does Heroku work in Russia now?", a: "Heroku no longer offers free tiers, and paid plans can't be paid with a Russian card. Data is stored abroad, which doesn't meet 152-FZ. Dada Cloud covers both: ruble payments and servers in Russia." },
      { q: "What can replace Heroku with ruble payments?", a: "Dada Cloud is a Russian platform with the same git-push-to-production flow: pay with a Russian card, get an invoice and closing documents, no foreign cards or intermediaries." },
      { q: "Do I have to rewrite my app when moving from Heroku?", a: "No. You connect the same GitHub repo, the platform detects the framework and builds the project. No Procfile or buildpacks to configure." },
      { q: "Is there a Heroku Postgres equivalent?", a: "Yes. Managed PostgreSQL is created inside the platform next to the app, and the DATABASE_URL connection string is injected into the service automatically." },
      { q: "Will apps sleep like free dynos?", a: "No. The app stays running and responds without a cold start on the first request." },
    ],
  },
  railwayAlt: {
    heroTitle: "A Railway alternative that works in Russia",
    heroSubtitle:
      "Railway is convenient, but paying for it needs a foreign card and data sits abroad. Dada Cloud reproduces the same experience: connect a GitHub repo, push, and get a live HTTPS URL with a database alongside in minutes. Ruble payments, servers in Russia, no VPN.",
    featuresTitle: "Why teams move from Railway to Dada Cloud",
    features: [
      { title: "Pay with a Russian card", desc: "A Russian card, an invoice and closing documents. No foreign card, intermediaries or usage-billing workarounds." },
      { title: "Deploy from GitHub, like Railway", desc: "Connect a repo, push to the main branch — automatic rebuild and deploy. The framework is detected for you." },
      { title: "PostgreSQL and storage alongside", desc: "Like Railway: managed PostgreSQL and S3 storage are created next to the app, and the connection string is injected automatically." },
      { title: "Works without a VPN", desc: "Both the panel and your deployed apps open from Russia directly — neither you nor your users need a VPN." },
      { title: "Data in Russia (152-FZ)", desc: "Apps and databases run on servers inside Russia — what the personal-data law requires." },
      { title: "Predictable pricing in rubles", desc: "Ruble plans instead of dollar usage-billing — costs are known upfront and not tied to the exchange rate." },
    ],
    faqTitle: "Replacing Railway in Russia — FAQ",
    faq: [
      { q: "Does Railway work in Russia?", a: "The panel and deployed projects usually open, but you can't pay with a Russian card and data is stored abroad — which doesn't meet 152-FZ. Dada Cloud solves both: ruble payments and servers in Russia." },
      { q: "What can replace Railway for ruble payments?", a: "Dada Cloud is a Russian platform with the same GitHub-to-production flow: pay with a Russian card, get an invoice and closing documents for legal entities, no foreign cards." },
      { q: "Do I have to rewrite my project when moving from Railway?", a: "No. You connect the same GitHub repo, the platform detects the framework and builds it. A push to the main branch rebuilds and deploys automatically." },
      { q: "Is there a database, like the Railway Postgres plugin?", a: "Yes. Managed PostgreSQL is created next to the app and DATABASE_URL is injected automatically — no separate external provider needed." },
      { q: "How is pricing different from Railway's usage-billing?", a: "Plans are fixed in rubles rather than metered in dollars — costs are predictable and independent of the exchange rate." },
    ],
  },
  renderAlt: {
    heroTitle: "A Render alternative for Russia",
    heroSubtitle:
      "Render is convenient for deploying web services and databases, but you can't pay with a Russian card and data sits abroad. Dada Cloud gives you the same flow: connect a GitHub repo, push, and get a live HTTPS URL with managed PostgreSQL alongside in minutes. Ruble payments, servers in Russia, no VPN.",
    featuresTitle: "Why teams move from Render to Dada Cloud",
    features: [
      { title: "Pay with a Russian card", desc: "A Russian card, an invoice and closing documents for legal entities. No foreign card or intermediaries." },
      { title: "Deploy from GitHub, like Render", desc: "Connect a repo, push to the main branch — automatic rebuild and deploy. The framework is detected for you." },
      { title: "Managed PostgreSQL alongside", desc: "Like Render PostgreSQL: a managed database is created next to the app and DATABASE_URL is injected automatically." },
      { title: "Apps don't sleep", desc: "Unlike Render's free tier, your app stays alive and responds without a delay on the first request." },
      { title: "Data in Russia (152-FZ)", desc: "Apps and databases run on servers inside Russia — what the personal-data law requires." },
      { title: "HTTPS domain out of the box", desc: "Right after the deploy the app is reachable over a valid TLS certificate. Your own domain connects in a couple of steps." },
    ],
    faqTitle: "Replacing Render in Russia — FAQ",
    faq: [
      { q: "Does Render work in Russia?", a: "The panel and deployed services usually open, but you can't pay with a Russian card and data is stored abroad — which doesn't meet 152-FZ. Dada Cloud solves both: ruble payments and servers in Russia." },
      { q: "What can replace Render with ruble payments?", a: "Dada Cloud is a Russian platform with the same GitHub-to-production flow: pay with a Russian card, get an invoice and closing documents for legal entities, no foreign cards." },
      { q: "Do I have to rewrite my project when moving from Render?", a: "No. You connect the same GitHub repo, the platform detects the framework and builds it. A push to the main branch rebuilds and deploys automatically." },
      { q: "Is there a Render PostgreSQL equivalent?", a: "Yes. Managed PostgreSQL is created next to the app and DATABASE_URL is injected automatically — no separate external provider needed." },
      { q: "Will services sleep like Render's free tier?", a: "No. The app stays running and responds without a cold start on the first request." },
    ],
  },
  databases: {
    heroTitle: "Managed PostgreSQL",
    heroSubtitle:
      "Created next to your application, with DATABASE_URL wired in automatically. Backups are configured at creation time; monitoring and upgrades are on us.",
    engines: ["PostgreSQL"],
    featuresTitle: "What managed Postgres includes",
    features: [
      { title: "Scheduled backups", desc: "Backup schedule is set when you create the database and shown right in the panel." },
      { title: "Performance monitoring", desc: "Connection, query and load metrics in real time." },
      { title: "Secure access", desc: "Private networks, role-based access and connection encryption." },
      { title: "Scaling", desc: "Change instance resources without manual data migration." },
    ],
    note: "Need MySQL or Redis? Managed Postgres lives here — MySQL and Redis run on your own server.",
    noteLinkLabel: "App Servers →",
    faqTitle: "Database FAQ",
    faq: [
      { q: "Which databases are available as managed?", a: "The managed engine is PostgreSQL. It's created next to your application and the DATABASE_URL connection string is injected into the service automatically. MySQL and Redis run on your own server (App Server), not as a managed resource." },
      { q: "Do I have to build the connection string by hand?", a: "No. When you create the database you bind it to an application, and DATABASE_URL shows up in the service's environment variables on its own — no DSN to assemble manually." },
      { q: "Are there backups?", a: "Yes. Backups are enabled when you create the database: you pick a schedule (hourly or daily) and a retention window — 7, 14 or 30 days." },
      { q: "Can I resize the database without a migration?", a: "Yes. Instance resources change without manual data migration — the app keeps using the same connection string." },
    ],
  },
  storage: {
    heroTitle: "S3 object storage",
    heroSubtitle:
      "S3-compatible storage for backups, media and static assets. Currently in Beta — some capabilities are still being finished.",
    featuresTitle: "Storage capabilities",
    features: [
      { title: "S3-compatible API", desc: "Works with familiar SDKs and tools (aws-cli, s3cmd, etc.)." },
      { title: "Pay for what you store", desc: "Pay only for space actually used and traffic." },
    ],
    faqTitle: "Object storage FAQ",
    faq: [
      { q: "What state is object storage in?", a: "It's in Beta. Creating S3-compatible buckets works; some capabilities are still being finished. Don't build critical workflows on it until it's out of beta." },
      { q: "What tools is it compatible with?", a: "The API is S3-compatible, so it works with familiar tools — aws-cli, s3cmd and any SDK that speaks S3." },
      { q: "How is it billed?", a: "By volume: you pay for the space actually used and traffic, not a fixed disk tier." },
    ],
  },
  pricing: {
    heroTitle: "Simple, transparent pricing",
    heroSubtitle: "Pay for a plan, not per resource. No hidden fees.",
    plans: [
      {
        key: "free",
        name: "Free",
        price: "$0",
        period: "",
        tagline: "Try it out and pet projects",
        features: [
          "1 application",
          "1 database",
          "1 GB storage",
          "1 domain",
          "1 environment",
          "1 team member",
          "No backups",
          "Community support",
        ],
        quotaMatrix: {
          apps: "1",
          databases: "1",
          storage: "1 GB",
          domains: "1",
          environments: "1",
          members: "1",
          backups: "None",
          support: "Community",
        },
        cta: "Create account",
      },
      {
        key: "startup",
        name: "Startup",
        price: "from $12",
        period: "/mo",
        tagline: "Solo developer or small team",
        features: [
          "5 applications",
          "2 databases",
          "10 GB storage",
          "5 domains",
          "2 environments",
          "3 team members",
          "7-day backups",
          "Email support",
        ],
        quotaMatrix: {
          apps: "5",
          databases: "2",
          storage: "10 GB",
          domains: "5",
          environments: "2",
          members: "3",
          backups: "7 days",
          support: "Email",
        },
        cta: "Get started",
        highlight: true,
      },
      {
        key: "business",
        name: "Business",
        price: "from $35",
        period: "/mo",
        tagline: "Growing team with production workloads",
        features: [
          "20 applications",
          "10 databases",
          "100 GB storage",
          "20 domains",
          "5 environments",
          "10 team members",
          "30-day backups",
          "Priority support",
        ],
        quotaMatrix: {
          apps: "20",
          databases: "10",
          storage: "100 GB",
          domains: "20",
          environments: "5",
          members: "10",
          backups: "30 days",
          support: "Priority",
        },
        cta: "Get started",
      },
      {
        key: "enterprise",
        name: "Enterprise",
        price: "Custom",
        period: "",
        tagline: "Custom quotas and SLA",
        features: [
          "Unlimited applications",
          "Unlimited databases",
          "Storage by agreement",
          "Unlimited domains",
          "Unlimited environments",
          "Unlimited team members",
          "Backups by agreement",
          "Priority support + SLA",
        ],
        quotaMatrix: {
          apps: "Unlimited",
          databases: "Unlimited",
          storage: "By agreement",
          domains: "Unlimited",
          environments: "Unlimited",
          members: "Unlimited",
          backups: "By agreement",
          support: "Priority + SLA",
        },
        cta: "Contact sales",
      },
    ],
    note: "",
    recommender: {
      title: "Find your plan",
      subtitle: "Tell us what you need — we'll recommend the right plan.",
      labelApps: "Applications",
      labelDatabases: "Databases",
      labelDomains: "Domains",
      labelMembers: "Team members",
      labelStorage: "Storage (GB)",
      submit: "Recommend",
      loading: "Finding best plan…",
      result: "We recommend:",
      errorFallback: "Could not reach the server — using local calculation.",
    },
  },
  footer: {
    tagline: "Backend cloud: from GitHub to production in minutes.",
    productsTitle: "Products",
    products: [
      { label: "Servers", href: "/cloud-servers" },
      { label: "Databases", href: "/databases" },
      { label: "Object storage", href: "/storage" },
      { label: "Vercel alternative", href: "/analog-vercel" },
      { label: "Heroku alternative", href: "/analog-heroku" },
      { label: "Railway alternative", href: "/analog-railway" },
      { label: "Render alternative", href: "/analog-render" },
      { label: "Pricing", href: "/pricing" },
    ],
    companyTitle: "Company",
    company: [
      { label: "About", href: "/" },
      { label: "Console", href: "/projects" },
      { label: "Log in", href: "/login" },
      { label: "DADA Development", href: "https://development.dada-tuda.ru/" },
      { label: "AgentSync Hub", href: "https://a2a-hub.pro/" },
    ],
    resourcesTitle: "Resources",
    resources: [
      { label: "Documentation", href: "/developer" },
      { label: "API", href: "/developer" },
      { label: "Status", href: "/developer" },
    ],
    rights: "All rights reserved.",
    legal: "DADA Cloud — a cloud platform.",
  },
};

export const dictionaries: Record<Locale, Dict> = { ru, en };
