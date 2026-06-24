// Marketing-site copy dictionary. RU is the default locale (parity with the
// reference cloud-provider landing); EN mirrors it for the language toggle.
// Console UI strings are intentionally NOT here — this is marketing only.

export type Locale = "ru" | "en";

export const LOCALES: Locale[] = ["ru", "en"];
export const DEFAULT_LOCALE: Locale = "ru";

type Service = { key: string; title: string; desc: string; href: string; badge?: string };
type Feature = { title: string; desc: string };
type Faq = { q: string; a: string };
type Plan = { key: string; name: string; price: string; period: string; tagline: string; features: string[]; cta: string; highlight?: boolean };
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
    heroTitle: "Запустите backend из GitHub за несколько минут",
    heroSubtitle:
      "Подключите репозиторий, добавьте Postgres и домен — получайте стабильные деплои, логи и откат. Без отдельной DevOps-команды.",
    heroPrimary: "Подключить GitHub",
    heroSecondary: "Как это работает",
    heroTertiary: "Запросить пилот / миграцию",
    stepsTitle: "От репозитория до продакшена — три шага",
    stepsSubtitle: "Без ручного CI/CD и SSH-релизов.",
    steps: [
      { num: "01", title: "Подключите GitHub", desc: "Выберите репозиторий. Сборка и деплой настраиваются автоматически — из Dockerfile или прямо из исходников." },
      { num: "02", title: "Добавьте Postgres и домен", desc: "База данных, собственный домен и HTTPS поднимаются в том же потоке. Без отдельных тикетов в инфраструктуру." },
      { num: "03", title: "Деплойте и откатывайте", desc: "Каждый push — новый деплой. Логи в реальном времени, откат на прошлую версию в один клик." },
    ],
    valueTitle: "Что вы получаете",
    valueSubtitle: "Язык результата, а не конфигов.",
    value: [
      { title: "GitHub → production без ручного CI/CD", desc: "Push в ветку — и сервис собран, задеплоен и доступен по HTTPS. Пайплайны руками не нужны." },
      { title: "База и домен в одном потоке", desc: "Postgres, домен и SSL подключаются там же, где деплой. Один проект — вся backend-обвязка." },
      { title: "Логи, откат и понятные лимиты", desc: "Видно, что происходит и почему упало. Откат в клик, hard-лимиты и оценка цены до деплоя." },
    ],
    scenariosTitle: "Под ваш сценарий",
    scenariosSubtitle: "От пет-проекта до растущей команды.",
    scenarios: [
      { tag: "Solo / founder", title: "Запуск сервиса за минуты", desc: "Перенесите backend с VPS + Compose в один проект. Без ручных SSH-релизов и самописных скриптов деплоя." },
      { tag: "Стартап 2–10", title: "Команда без DevOps", desc: "Деплой из GitHub, общий доступ с ролями, единые логи и откаты. Инженеры катят сами, без выделенного ops." },
      { tag: "Агентство", title: "Клиенты в одном месте", desc: "Домены, базы и доступы клиентов централизованы. Новый проект — за минуты, а не за день настройки." },
    ],
    proofTitle: "Истории команд",
    proofSubtitle: "Скоро здесь появятся реальные кейсы.",
    proof: [
      { quote: "Перевели backend с VPS + Compose в один проект — без ручных SSH-релизов.", author: "Кейс команды · скоро" },
      { quote: "Запуск нового сервиса сократился с часов до минут.", author: "Кейс стартапа · скоро" },
      { quote: "Централизовали домены, базы и доступы клиентов в одной панели.", author: "Кейс агентства · скоро" },
    ],
    pricingTitle: "Прозрачные планы",
    pricingSubtitle: "Без сюрпризов по счёту. Hard-лимит и оценка цены до деплоя.",
    pricingTiers: [
      { name: "Sandbox", price: "0 ₽", tagline: "Попробовать и пет-проекты", bullets: ["1 проект", "Деплой из GitHub", "Postgres для теста", "Базовые логи"] },
      { name: "Solo", price: "от 1 490 ₽", tagline: "Один разработчик в продакшене", bullets: ["Несколько сервисов", "Managed Postgres", "Домен + HTTPS", "Логи и откаты"], highlight: true },
      { name: "Startup-team", price: "Индивидуально", tagline: "Растущая команда 2–10", bullets: ["Командные роли", "Гибкие квоты", "Приоритетная поддержка", "Помощь с миграцией"] },
    ],
    pricingNote: "Цены ориентировочные на этапе запуска. Полная таблица — на странице цен.",
    faqTitle: "Частые возражения",
    faq: [
      { q: "У меня уже есть VPS", a: "VPS вы обслуживаете сами: обновления, бэкапы, деплой-скрипты, SSH. Здесь деплой, база, домен и откаты работают из коробки — VPS можно мигрировать в один проект." },
      { q: "У нас есть GitHub Actions", a: "Actions собирают артефакт, но дальше нужно куда-то катить, держать базу, домен и откаты. Мы закрываем путь от push до работающего сервиса с HTTPS — без своего CD-пайплайна." },
      { q: "Мы ещё маленькие", a: "Поэтому и подходит: один проект вместо ручной инфраструктуры. Sandbox бесплатный, hard-лимиты не дадут счёту улететь, расти можно без переезда." },
    ],
    ctaTitle: "Запустите backend из GitHub сегодня",
    ctaSubtitle: "Подключите репозиторий за минуту. Платите только за используемые ресурсы.",
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
    heroSubtitle: "Платите за используемые ресурсы. Без скрытых платежей.",
    plans: [
      {
        key: "starter",
        name: "Starter",
        price: "0 ₽",
        period: "",
        tagline: "Для пробы и пет-проектов",
        features: ["1 проект", "Облачные серверы по часам", "Объектное хранилище", "Базовый мониторинг", "Поддержка по email"],
        cta: "Создать аккаунт",
      },
      {
        key: "pro",
        name: "Pro",
        price: "от 1 490 ₽",
        period: "/мес",
        tagline: "Для растущих команд",
        features: ["Несколько проектов", "Managed Kubernetes", "Управляемые БД", "CDN и приватные сети", "Расширенный мониторинг и алерты"],
        cta: "Начать",
        highlight: true,
      },
      {
        key: "business",
        name: "Business",
        price: "Индивидуально",
        period: "",
        tagline: "Для нагруженных проектов",
        features: ["SLA и приоритетная поддержка", "Гибкие квоты ресурсов", "Командные роли и RBAC", "Выделенные ресурсы", "Помощь с миграцией"],
        cta: "Получить консультацию",
      },
    ],
    note: "Цены ориентировочные и приведены для примера на этапе запуска платформы.",
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
    heroTitle: "Ship your backend from GitHub in minutes",
    heroSubtitle:
      "Connect a repo, add Postgres and a domain — get stable deploys, logs and rollback. No separate DevOps team.",
    heroPrimary: "Connect GitHub",
    heroSecondary: "How it works",
    heroTertiary: "Request a pilot / migration",
    stepsTitle: "From repo to production in three steps",
    stepsSubtitle: "No manual CI/CD, no SSH releases.",
    steps: [
      { num: "01", title: "Connect GitHub", desc: "Pick a repository. Build and deploy are wired up automatically — from a Dockerfile or straight from source." },
      { num: "02", title: "Add Postgres and a domain", desc: "Database, custom domain and HTTPS come up in the same flow. No separate infrastructure tickets." },
      { num: "03", title: "Deploy and roll back", desc: "Every push is a new deploy. Real-time logs, one-click rollback to the previous version." },
    ],
    valueTitle: "What you get",
    valueSubtitle: "Outcomes, not config files.",
    value: [
      { title: "GitHub → production without manual CI/CD", desc: "Push to a branch and the service is built, deployed and live over HTTPS. No pipelines by hand." },
      { title: "Database and domain in one flow", desc: "Postgres, domain and SSL connect where you deploy. One project — the whole backend stack." },
      { title: "Logs, rollback and clear limits", desc: "See what's happening and why it broke. One-click rollback, hard limits and a price estimate before deploy." },
    ],
    scenariosTitle: "Built for your case",
    scenariosSubtitle: "From a pet project to a growing team.",
    scenarios: [
      { tag: "Solo / founder", title: "Launch a service in minutes", desc: "Move a backend off VPS + Compose into one project. No manual SSH releases or homegrown deploy scripts." },
      { tag: "Startup 2–10", title: "A team without DevOps", desc: "Deploy from GitHub, shared role-based access, unified logs and rollbacks. Engineers ship themselves, no dedicated ops." },
      { tag: "Agency", title: "Clients in one place", desc: "Client domains, databases and access centralized. A new project in minutes, not a day of setup." },
    ],
    proofTitle: "Team stories",
    proofSubtitle: "Real case studies coming soon.",
    proof: [
      { quote: "Moved our backend off VPS + Compose into one project — no manual SSH releases.", author: "Team case · soon" },
      { quote: "Cut the launch of a new service from hours to minutes.", author: "Startup case · soon" },
      { quote: "Centralized client domains, databases and access in one panel.", author: "Agency case · soon" },
    ],
    pricingTitle: "Transparent plans",
    pricingSubtitle: "No billing surprises. Hard limit and a price estimate before deploy.",
    pricingTiers: [
      { name: "Sandbox", price: "$0", tagline: "Trials and pet projects", bullets: ["1 project", "Deploy from GitHub", "Postgres for testing", "Basic logs"] },
      { name: "Solo", price: "from $19", tagline: "One developer in production", bullets: ["Multiple services", "Managed Postgres", "Domain + HTTPS", "Logs and rollbacks"], highlight: true },
      { name: "Startup-team", price: "Custom", tagline: "Growing team of 2–10", bullets: ["Team roles", "Flexible quotas", "Priority support", "Migration help"] },
    ],
    pricingNote: "Indicative pricing at launch. Full table on the pricing page.",
    faqTitle: "Common objections",
    faq: [
      { q: "I already have a VPS", a: "A VPS you maintain yourself: updates, backups, deploy scripts, SSH. Here deploy, database, domain and rollbacks work out of the box — and a VPS can be migrated into one project." },
      { q: "We have GitHub Actions", a: "Actions build an artifact, but you still need somewhere to ship it, a database, a domain and rollbacks. We cover the path from push to a live HTTPS service — no CD pipeline of your own." },
      { q: "We're still small", a: "That's exactly why it fits: one project instead of manual infrastructure. Sandbox is free, hard limits keep the bill in check, and you can grow without migrating." },
    ],
    ctaTitle: "Ship your backend from GitHub today",
    ctaSubtitle: "Connect a repo in a minute. Pay only for the resources you use.",
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
    heroSubtitle: "Pay for the resources you use. No hidden fees.",
    plans: [
      {
        key: "starter",
        name: "Starter",
        price: "$0",
        period: "",
        tagline: "For trials and pet projects",
        features: ["1 project", "Hourly cloud servers", "Object storage", "Basic monitoring", "Email support"],
        cta: "Create account",
      },
      {
        key: "pro",
        name: "Pro",
        price: "from $19",
        period: "/mo",
        tagline: "For growing teams",
        features: ["Multiple projects", "Managed Kubernetes", "Managed databases", "CDN and private networks", "Advanced monitoring and alerts"],
        cta: "Get started",
        highlight: true,
      },
      {
        key: "business",
        name: "Business",
        price: "Custom",
        period: "",
        tagline: "For demanding workloads",
        features: ["SLA and priority support", "Flexible resource quotas", "Team roles and RBAC", "Dedicated resources", "Migration assistance"],
        cta: "Contact sales",
      },
    ],
    note: "Prices are indicative and shown as examples at the platform launch stage.",
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
