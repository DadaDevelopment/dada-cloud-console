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
    heroTitle: string;
    heroSubtitle: string;
    heroPrimary: string;
    heroSecondary: string;
    servicesTitle: string;
    servicesSubtitle: string;
    services: Service[];
    whyTitle: string;
    whySubtitle: string;
    why: Feature[];
    panelTitle: string;
    panelSubtitle: string;
    panelBullets: string[];
    stats: { value: string; label: string }[];
    statsNote: string;
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
    heroTitle: "Облачная платформа для ваших проектов",
    heroSubtitle:
      "Виртуальные серверы, Kubernetes, управляемые базы данных и объектное хранилище — единая GitOps-консоль для инфраструктуры любого масштаба.",
    heroPrimary: "Создать аккаунт",
    heroSecondary: "Документация",
    servicesTitle: "Всё, что нужно для запуска",
    servicesSubtitle: "Собственная панель управления. Запуск сервиса в пару кликов.",
    services: [
      { key: "vps", title: "Облачные серверы", desc: "VPS/VDS на NVMe с почасовой оплатой и снапшотами.", href: "/cloud-servers" },
      { key: "k8s", title: "Kubernetes", desc: "Управляемые кластеры: ноды, аддоны, мониторинг из коробки.", href: "/kubernetes", badge: "Beta" },
      { key: "db", title: "Облачные базы данных", desc: "PostgreSQL, MySQL, Redis. Бэкапы и failover на нас.", href: "/databases" },
      { key: "s3", title: "Объектное хранилище", desc: "S3-совместимое хранилище для бэкапов, медиа и статики.", href: "/storage" },
      { key: "cdn", title: "CDN", desc: "Раздача статики с пограничных узлов и кэширование.", href: "/storage" },
      { key: "monitoring", title: "Мониторинг", desc: "Метрики, логи и алерты по всем ресурсам проекта.", href: "/cloud-servers" },
    ],
    whyTitle: "Почему DADA Cloud",
    whySubtitle: "Инфраструктура как код — без боли.",
    why: [
      { title: "GitOps по умолчанию", desc: "Каждое изменение — это коммит. Полный аудит, откат в один клик, воспроизводимые окружения." },
      { title: "SLA и отказоустойчивость", desc: "Гео-распределённые узлы, защита от DDoS и резервное копирование без простоя." },
      { title: "Безопасность", desc: "Изоляция по namespace, RBAC, политики ресурсов и SSO через Keycloak." },
    ],
    panelTitle: "Удовольствие в каждом клике",
    panelSubtitle: "Управляйте всей инфраструктурой из одной панели.",
    panelBullets: [
      "Проекты и окружения в едином дереве",
      "Деплой из Git с автоматической сборкой",
      "Метрики и логи в реальном времени",
      "Командный доступ с гибкими ролями",
    ],
    stats: [
      { value: "99.9%", label: "целевой uptime SLA" },
      { value: "10+", label: "облачных сервисов" },
      { value: "24/7", label: "поддержка" },
      { value: "GitOps", label: "в основе платформы" },
    ],
    statsNote: "Целевые показатели платформы на этапе запуска.",
    ctaTitle: "Запустите первый сервис сегодня",
    ctaSubtitle: "Регистрация занимает минуту. Платите только за используемые ресурсы.",
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
    heroTitle: "A cloud platform for your projects",
    heroSubtitle:
      "Virtual servers, Kubernetes, managed databases and object storage — one GitOps console for infrastructure at any scale.",
    heroPrimary: "Create account",
    heroSecondary: "Documentation",
    servicesTitle: "Everything you need to ship",
    servicesSubtitle: "Our own control panel. Launch a service in a couple of clicks.",
    services: [
      { key: "vps", title: "Cloud servers", desc: "NVMe-backed VPS/VDS with hourly billing and snapshots.", href: "/cloud-servers" },
      { key: "k8s", title: "Kubernetes", desc: "Managed clusters: nodes, addons and monitoring out of the box.", href: "/kubernetes", badge: "Beta" },
      { key: "db", title: "Cloud databases", desc: "PostgreSQL, MySQL, Redis. Backups and failover handled for you.", href: "/databases" },
      { key: "s3", title: "Object storage", desc: "S3-compatible storage for backups, media and static assets.", href: "/storage" },
      { key: "cdn", title: "CDN", desc: "Serve static content from edge nodes with caching.", href: "/storage" },
      { key: "monitoring", title: "Monitoring", desc: "Metrics, logs and alerts across every project resource.", href: "/cloud-servers" },
    ],
    whyTitle: "Why DADA Cloud",
    whySubtitle: "Infrastructure as code — without the pain.",
    why: [
      { title: "GitOps by default", desc: "Every change is a commit. Full audit trail, one-click rollback, reproducible environments." },
      { title: "SLA & resilience", desc: "Geo-distributed nodes, DDoS protection and zero-downtime backups." },
      { title: "Security", desc: "Namespace isolation, RBAC, resource policies and SSO via Keycloak." },
    ],
    panelTitle: "Delightful in every click",
    panelSubtitle: "Manage your whole infrastructure from a single panel.",
    panelBullets: [
      "Projects and environments in one tree",
      "Deploy from Git with automated builds",
      "Real-time metrics and logs",
      "Team access with flexible roles",
    ],
    stats: [
      { value: "99.9%", label: "target SLA uptime" },
      { value: "10+", label: "cloud services" },
      { value: "24/7", label: "support" },
      { value: "GitOps", label: "at the core" },
    ],
    statsNote: "Target platform figures at launch.",
    ctaTitle: "Launch your first service today",
    ctaSubtitle: "Sign up in a minute. Pay only for the resources you use.",
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
