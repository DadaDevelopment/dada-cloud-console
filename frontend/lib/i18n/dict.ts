// Marketing-site copy dictionary. RU is the default locale (parity with the
// reference cloud-provider landing); EN mirrors it for the language toggle.
// Console UI strings are intentionally NOT here — this is marketing only.

export type Locale = "ru" | "en";

export const LOCALES: Locale[] = ["ru", "en"];
export const DEFAULT_LOCALE: Locale = "ru";

type Service = { key: string; title: string; desc: string; href: string; badge?: string };
type Feature = { title: string; desc: string };
type Faq = { q: string; a: string };
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
    kubernetes: string;
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
  };
  servers: {
    heroTitle: string;
    heroSubtitle: string;
    features: Feature[];
    faqTitle: string;
    faq: Faq[];
  };
  kubernetes: {
    heroTitle: string;
    heroSubtitle: string;
    features: Feature[];
    faqTitle: string;
    faq: Faq[];
  };
  databases: {
    heroTitle: string;
    heroSubtitle: string;
    engines: string[];
    features: Feature[];
  };
  storage: {
    heroTitle: string;
    heroSubtitle: string;
    features: Feature[];
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
    kubernetes: "Kubernetes",
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
      { title: "Логи, откат, потолок по счёту", desc: "Видно, что упало и почему. Откат в одну кнопку. Hard-лимит держит счёт в рамках, цену видно до деплоя." },
    ],
    scenariosTitle: "Под ваш случай",
    scenariosSubtitle: "От пет-проекта до растущей команды.",
    scenarios: [
      { tag: "Solo / founder", title: "Запуск сервиса за минуты", desc: "Снимаете backend с VPS на docker-compose и переносите в один проект. Никаких релизов по SSH и самописных деплой-скриптов." },
      { tag: "Стартап 2–10", title: "Команда без DevOps", desc: "Деплой из GitHub, доступы по ролям, общие логи и откаты. Инженеры катят сами. Отдельный ops под это держать не нужно." },
      { tag: "Агентство", title: "Клиенты в одном месте", desc: "Домены, базы и доступы всех клиентов в одной панели. Новый проект поднимается за минуты вместо дня возни с настройкой." },
    ],
    proofTitle: "Истории команд",
    proofSubtitle: "Скоро здесь будут живые кейсы.",
    proof: [
      { quote: "Сидели на VPS с compose, релизились по ssh руками. Перенесли в один проект, теперь просто пушим.", author: "Команда · скоро" },
      { quote: "Раньше поднять новый сервис — это полдня. Сейчас успеваю до обеда выкатить пару штук.", author: "Стартап · скоро" },
      { quote: "Домены и базы клиентов больше не размазаны по десяти панелям. Всё в одном месте, выдыхаю.", author: "Агентство · скоро" },
    ],
    pricingTitle: "Прозрачные планы",
    pricingSubtitle: "Без сюрпризов в счёте. Hard-лимит и оценка цены до деплоя.",
    pricingTiers: [
      { name: "Free", price: "0 ₽", tagline: "Попробовать и пет-проекты", bullets: ["1 приложение", "1 база данных", "Деплой из GitHub", "Базовые логи"] },
      { name: "Startup", price: "990 ₽/мес", tagline: "Один разработчик в продакшене", bullets: ["5 приложений", "2 базы данных", "5 доменов", "Бэкапы 7 дней"], highlight: true },
      { name: "Business", price: "2 900 ₽/мес", tagline: "Растущая команда с продакшеном", bullets: ["20 приложений", "10 баз данных", "Бэкапы 30 дней", "Приоритетная поддержка"] },
    ],
    pricingNote: "Полная таблица тарифов — на странице цен.",
    faqTitle: "Частые возражения",
    faq: [
      { q: "У меня уже есть VPS", a: "VPS вы тащите сами: обновления ОС, бэкапы, деплой-скрипты, ночные SSH-сессии когда что-то легло. Тут база, домен, выкат и откат уже работают, а сам VPS можно перенести в проект и забыть про эту рутину." },
      { q: "У нас есть GitHub Actions", a: "Actions соберут артефакт, и на этом всё. Дальше его надо куда-то выкатить, поднять базу, прицепить домен, придумать откат. Вот этот кусок от push до живого HTTPS-сервиса мы и берём на себя. Свой CD-пайплайн писать не придётся." },
      { q: "Мы ещё маленькие", a: "Тем более ваш вариант. Один проект вместо ручной инфраструктуры, которую в таком размере держать нечем. Sandbox бесплатный, hard-лимит не даст счёту улететь, а вырастете — переезжать не надо." },
    ],
    ctaTitle: "Поднимите backend из GitHub сегодня",
    ctaSubtitle: "Подключение репозитория занимает минуту. Платите только за то, что реально потребляете.",
  },
  servers: {
    heroTitle: "Облачные серверы VPS/VDS",
    heroSubtitle:
      "Виртуальные машины на быстрых NVMe-дисках. Снапшоты, образы, горизонтальное масштабирование и почасовая оплата.",
    features: [
      { title: "NVMe и современное железо", desc: "Серверные CPU и NVMe-накопители для предсказуемой производительности." },
      { title: "Снапшоты и образы", desc: "Делайте снимок диска и разворачивайте новые машины из готовых образов." },
      { title: "Сеть и приватные подсети", desc: "Гибкие сетевые настройки, приватные сети между серверами проекта." },
      { title: "Мониторинг из коробки", desc: "Загрузка CPU, память, диск и сеть — графики без настройки." },
      { title: "Резервное копирование", desc: "Автоматические бэкапы по расписанию и восстановление за минуты." },
      { title: "Масштабирование", desc: "Добавляйте узлы и worker-группы по мере роста нагрузки." },
    ],
    faqTitle: "Частые вопросы",
    faq: [
      { q: "Как оплачивается сервер?", a: "Почасовая тарификация — платите только за время работы машины." },
      { q: "Можно ли изменить конфигурацию?", a: "Да, ресурсы CPU/RAM/диск меняются без переустановки ОС." },
      { q: "Есть ли защита от DDoS?", a: "Базовая защита от DDoS включена на уровне платформы." },
    ],
  },
  kubernetes: {
    heroTitle: "Управляемый Kubernetes",
    heroSubtitle:
      "Кластеры Kubernetes без рутины: управляющий узел, worker-группы, аддоны и мониторинг разворачиваются автоматически.",
    features: [
      { title: "Готовый кластер за минуты", desc: "Создайте кластер и worker-группы через панель или API." },
      { title: "Аддоны в один клик", desc: "Ingress, мониторинг, хранилище и др. устанавливаются автоматически." },
      { title: "Автомасштабирование узлов", desc: "Worker-группы масштабируются под нагрузку приложений." },
      { title: "kubeconfig из консоли", desc: "Подключайтесь к кластеру одной командой — доступ выдаётся из панели." },
      { title: "Мониторинг и алерты", desc: "Метрики узлов и подов, логи и оповещения из коробки." },
      { title: "GitOps-деплой", desc: "Доставка приложений из Git-репозитория с откатами." },
    ],
    faqTitle: "Частые вопросы",
    faq: [
      { q: "Какая версия Kubernetes?", a: "Поддерживаются актуальные стабильные версии; обновление управляется платформой." },
      { q: "Управляющий узел тарифицируется?", a: "Тарификация зависит от ресурсов worker-узлов и аддонов." },
      { q: "Сервис в Beta?", a: "Да, Kubernetes сейчас в стадии Beta — API и панель могут меняться." },
    ],
  },
  databases: {
    heroTitle: "Управляемые базы данных",
    heroSubtitle:
      "Запускайте PostgreSQL, MySQL и Redis без забот об обслуживании. Бэкапы, мониторинг и обновления — на нас.",
    engines: ["PostgreSQL", "MySQL", "Redis"],
    features: [
      { title: "Автоматические бэкапы", desc: "Регулярные резервные копии и восстановление на точку во времени." },
      { title: "Мониторинг производительности", desc: "Метрики подключений, запросов и нагрузки в реальном времени." },
      { title: "Безопасный доступ", desc: "Приватные сети, доступ по ролям и шифрование соединений." },
      { title: "Масштабирование", desc: "Меняйте ресурсы инстанса без миграции данных вручную." },
    ],
  },
  storage: {
    heroTitle: "Объектное хранилище S3",
    heroSubtitle:
      "S3-совместимое хранилище для бэкапов, медиа и статики. Раздавайте контент через CDN с пограничных узлов.",
    features: [
      { title: "S3-совместимый API", desc: "Работает с привычными SDK и инструментами (aws-cli, s3cmd и др.)." },
      { title: "CDN-раздача", desc: "Кэширование и доставка статики ближе к пользователю." },
      { title: "Оплата за объём", desc: "Платите только за реально занятое место и трафик." },
      { title: "Версионирование", desc: "Храните версии объектов и защищайтесь от случайного удаления." },
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
    tagline: "Облачная платформа на основе GitOps.",
    productsTitle: "Продукты",
    products: [
      { label: "Облачные серверы", href: "/cloud-servers" },
      { label: "Kubernetes", href: "/kubernetes" },
      { label: "Базы данных", href: "/databases" },
      { label: "Объектное хранилище", href: "/storage" },
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
    kubernetes: "Kubernetes",
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
      { title: "Logs, rollback, a ceiling on the bill", desc: "See what broke and why. Roll back with one button. A hard limit keeps the bill in check, and you see the price before you deploy." },
    ],
    scenariosTitle: "Built for your case",
    scenariosSubtitle: "From a pet project to a growing team.",
    scenarios: [
      { tag: "Solo / founder", title: "Launch a service in minutes", desc: "Pull your backend off a VPS running docker-compose and move it into one project. No releases over SSH, no homegrown deploy scripts." },
      { tag: "Startup 2–10", title: "A team without DevOps", desc: "Deploy from GitHub, role-based access, shared logs and rollbacks. Engineers ship themselves. No dedicated ops needed for it." },
      { tag: "Agency", title: "Clients in one place", desc: "Every client's domains, databases and access in one panel. A new project comes up in minutes instead of a day of fiddling with setup." },
    ],
    proofTitle: "Team stories",
    proofSubtitle: "Real cases coming here soon.",
    proof: [
      { quote: "We were on a VPS with compose, releasing over ssh by hand. Moved it into one project, now we just push.", author: "Team · soon" },
      { quote: "Standing up a new service used to eat half a day. Now I ship a couple before lunch.", author: "Startup · soon" },
      { quote: "Client domains and databases aren't smeared across ten panels anymore. One place, I can breathe.", author: "Agency · soon" },
    ],
    pricingTitle: "Transparent plans",
    pricingSubtitle: "No billing surprises. Hard limit and a price estimate before deploy.",
    pricingTiers: [
      { name: "Free", price: "$0", tagline: "Trials and pet projects", bullets: ["1 application", "1 database", "Deploy from GitHub", "Basic logs"] },
      { name: "Startup", price: "$12/mo", tagline: "Solo developer in production", bullets: ["5 applications", "2 databases", "5 domains", "7-day backups"], highlight: true },
      { name: "Business", price: "$35/mo", tagline: "Growing team with production load", bullets: ["20 applications", "10 databases", "30-day backups", "Priority support"] },
    ],
    pricingNote: "Full pricing table on the pricing page.",
    faqTitle: "Common objections",
    faq: [
      { q: "I already have a VPS", a: "A VPS is on you: OS updates, backups, deploy scripts, the late-night SSH session when something falls over. Here the database, domain, deploy and rollback already work, and the VPS itself can move into a project so you forget that chore." },
      { q: "We have GitHub Actions", a: "Actions build an artifact and that's where they stop. You still have to ship it somewhere, stand up a database, attach a domain, figure out rollback. That stretch from push to a live HTTPS service is the part we take on. No CD pipeline of your own to write." },
      { q: "We're still small", a: "All the more reason. One project instead of manual infrastructure you've got no one to run at this size. Sandbox is free, a hard limit won't let the bill run off, and when you grow there's no migration to do." },
    ],
    ctaTitle: "Get your backend live from GitHub today",
    ctaSubtitle: "Connecting a repo takes a minute. You only pay for what you actually use.",
  },
  servers: {
    heroTitle: "Cloud servers VPS/VDS",
    heroSubtitle:
      "Virtual machines on fast NVMe disks. Snapshots, images, horizontal scaling and hourly billing.",
    features: [
      { title: "NVMe and modern hardware", desc: "Server-grade CPUs and NVMe drives for predictable performance." },
      { title: "Snapshots and images", desc: "Snapshot a disk and spin up new machines from ready images." },
      { title: "Networking and private subnets", desc: "Flexible network settings and private networks between project servers." },
      { title: "Monitoring out of the box", desc: "CPU load, memory, disk and network — charts with zero setup." },
      { title: "Backups", desc: "Scheduled automatic backups and restore in minutes." },
      { title: "Scaling", desc: "Add nodes and worker groups as load grows." },
    ],
    faqTitle: "FAQ",
    faq: [
      { q: "How is a server billed?", a: "Hourly billing — pay only for the time the machine runs." },
      { q: "Can I change the configuration?", a: "Yes, CPU/RAM/disk resources change without reinstalling the OS." },
      { q: "Is there DDoS protection?", a: "Basic DDoS protection is included at the platform level." },
    ],
  },
  kubernetes: {
    heroTitle: "Managed Kubernetes",
    heroSubtitle:
      "Kubernetes clusters without the chores: control plane, worker groups, addons and monitoring provisioned automatically.",
    features: [
      { title: "Cluster ready in minutes", desc: "Create a cluster and worker groups via panel or API." },
      { title: "One-click addons", desc: "Ingress, monitoring, storage and more install automatically." },
      { title: "Node autoscaling", desc: "Worker groups scale to your application load." },
      { title: "kubeconfig from console", desc: "Connect with a single command — access issued from the panel." },
      { title: "Monitoring and alerts", desc: "Node and pod metrics, logs and alerts out of the box." },
      { title: "GitOps delivery", desc: "Deliver apps from a Git repo with rollbacks." },
    ],
    faqTitle: "FAQ",
    faq: [
      { q: "Which Kubernetes version?", a: "Current stable versions are supported; upgrades are managed by the platform." },
      { q: "Is the control plane billed?", a: "Billing depends on worker-node resources and addons." },
      { q: "Is the service in Beta?", a: "Yes, Kubernetes is currently in Beta — API and panel may change." },
    ],
  },
  databases: {
    heroTitle: "Managed databases",
    heroSubtitle:
      "Run PostgreSQL, MySQL and Redis without worrying about maintenance. Backups, monitoring and upgrades are on us.",
    engines: ["PostgreSQL", "MySQL", "Redis"],
    features: [
      { title: "Automatic backups", desc: "Regular backups and point-in-time recovery." },
      { title: "Performance monitoring", desc: "Connection, query and load metrics in real time." },
      { title: "Secure access", desc: "Private networks, role-based access and connection encryption." },
      { title: "Scaling", desc: "Change instance resources without manual data migration." },
    ],
  },
  storage: {
    heroTitle: "S3 object storage",
    heroSubtitle:
      "S3-compatible storage for backups, media and static assets. Serve content through a CDN from edge nodes.",
    features: [
      { title: "S3-compatible API", desc: "Works with familiar SDKs and tools (aws-cli, s3cmd, etc.)." },
      { title: "CDN delivery", desc: "Caching and content delivery closer to the user." },
      { title: "Pay for what you store", desc: "Pay only for space actually used and traffic." },
      { title: "Versioning", desc: "Keep object versions and guard against accidental deletion." },
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
    tagline: "A GitOps-based cloud platform.",
    productsTitle: "Products",
    products: [
      { label: "Cloud servers", href: "/cloud-servers" },
      { label: "Kubernetes", href: "/kubernetes" },
      { label: "Databases", href: "/databases" },
      { label: "Object storage", href: "/storage" },
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
