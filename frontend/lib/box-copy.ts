// Copy for the Dada Box private-preview landing (/box, /en/box).
//
// Deliberately kept OUT of lib/i18n/dict.ts: Dict is a strict contract shared by
// ~20 shipped marketing pages, and this page is a validation experiment whose
// copy will churn fast. Isolating it keeps the shared contract untouched. When
// Box graduates from experiment to product, fold this into dict.ts.
//
// See docs/product/box-product-brief.md for what this page is testing.

import type { Locale } from "@/lib/i18n/dict";

/** One line of the scripted terminal replay. Not a live session — see box-demo.tsx. */
export interface DemoLine {
  kind: "cmd" | "out" | "ok" | "note";
  text: string;
}

export interface BoxCopy {
  badge: string;
  heroTitle: string;
  heroSubtitle: string;
  heroPrimary: string;
  heroSecondary: string;
  heroNote: string;

  /** Promo band on the main marketing page. Box is becoming the central product. */
  spotlight: {
    eyebrow: string;
    title: string;
    body: string;
    bullets: string[];
    cta: string;
  };

  problem: {
    title: string;
    subtitle: string;
    items: { title: string; desc: string }[];
  };

  how: {
    title: string;
    subtitle: string;
    steps: { cmd: string; title: string; desc: string }[];
  };

  /** "Connect in 60 seconds" — the actual working path, right after "how it works". */
  connect: {
    title: string;
    subtitle: string;
    tabClaude: string;
    tabOther: string;
    claudeStep1Label: string;
    claudeStep1Cmd: string;
    claudeStep2Label: string;
    claudeStep2Cmd: string;
    otherLabel: string;
    otherCmd: string;
    otherNote: string;
    copyLabel: string;
    footNote: string;
    helpLink: string;
  };

  demo: {
    title: string;
    subtitle: string;
    recordingLabel: string;
    playLabel: string;
    replayLabel: string;
    lines: DemoLine[];
  };

  crystal: {
    title: string;
    subtitle: string;
    carriedTitle: string;
    carried: string[];
    note: string;
  };

  vps: {
    title: string;
    subtitle: string;
    rows: { claim: string; answer: string }[];
  };

  pricing: {
    title: string;
    subtitle: string;
    tiers: { name: string; price: string; note: string }[];
    disclaimer: string;
  };

  honesty: {
    title: string;
    subtitle: string;
    worksTitle: string;
    works: string[];
    notYetTitle: string;
    notYet: string[];
  };

  faq: {
    title: string;
    items: { q: string; a: string }[];
  };

  form: {
    title: string;
    subtitle: string;
    emailLabel: string;
    emailPlaceholder: string;
    contactLabel: string;
    contactPlaceholder: string;
    agentLabel: string;
    agentOptions: string[];
    parallelLabel: string;
    parallelOptions: string[];
    useCaseLabel: string;
    useCasePlaceholder: string;
    priceLabel: string;
    priceOptions: string[];
    submit: string;
    submitting: string;
    errorRequired: string;
    errorEmail: string;
    errorGeneric: string;
    successTitle: string;
    successBody: string;
    claimLabel: string;
    crystalTitle: string;
    crystalBody: string;
    crystalOptions: string[];
    crystalSubmit: string;
    crystalDone: string;
    privacy: string;
    privacyLink: string;
  };
}

const ru: BoxCopy = {
  badge: "Доступно сейчас",
  heroTitle: "Тело для твоего агента",
  heroSubtitle:
    "Бокс с рутом поднимается за секунды. Твой Claude, Cursor или Codex подключается и работает как на своей машине — только это не твоя машина. Прототип выжил — кристаллизуй его в постоянную VM с доменом.",
  heroPrimary: "Подключить агента",
  heroSecondary: "Посмотреть, как это работает",
  heroNote: "Своего агента приводишь сам. Токены мы не перепродаём.",

  spotlight: {
    eyebrow: "Новое · приватный превью",
    title: "Dada Box — тело для твоего агента",
    body: "Бокс с рутом за секунды, твой Claude или Cursor подключается и работает как на своей машине. База и S3 доцепляются на ходу, а выживший прототип кристаллизуется в постоянную VM с доменом — без переезда.",
    bullets: [
      "Десять агентов параллельно вместо одного",
      "Ноутбук остаётся чистым",
      "Одно окружение от мысли до прода",
    ],
    cta: "Посмотреть Box",
  },

  problem: {
    title: "Ноутбук — плохое тело для агента",
    subtitle:
      "Агент не устаёт, не боится сломать систему и готов работать всю ночь. Ограничение — не он, а машина, на которой он запущен.",
    items: [
      {
        title: "Третий агент убивает ноутбук",
        desc: "Один агент — терпимо. Три параллельно — вентилятор на максимуме, сборка стоит в очереди, IDE не отвечает. Параллелизм упирается в железо, которое ты носишь с собой.",
      },
      {
        title: "После агента остаётся мусор",
        desc: "Он поставил четыре версии Node, глобальные пакеты, докер-образы на 12 ГБ и systemd-юнит, о котором ты узнаешь через месяц. Своя машина слишком дорога, чтобы ей рисковать.",
      },
      {
        title: "Показать нечего",
        desc: "Прототип живёт на localhost. Чтобы дать ссылку клиенту, нужен адрес, TLS, база и место, где всё это не умрёт при закрытии крышки.",
      },
    ],
  },

  how: {
    title: "Как это работает",
    subtitle: "Четыре шага. Первый занимает секунды, последний превращает эксперимент в прод.",
    steps: [
      {
        cmd: "dada box up",
        title: "Тело за секунды",
        desc: "Прогретый бокс с рутом: node, python, go, docker, git и компиляторы уже внутри. Агент не тратит первые минуты на apt install.",
      },
      {
        cmd: "агент подключается",
        title: "Твой агент, наше тело",
        desc: "Claude Code, Cursor или Codex остаются у тебя, инструменты исполняются в боксе. Мы не хостим модель и не видим твою подписку.",
      },
      {
        cmd: "dada box attach db / s3",
        title: "Ресурсы на ходу",
        desc: "Понадобилась база или бакет — доцепляются в работающий бокс, без переезда и переписывания конфигов. Управляемые, с бэкапами.",
      },
      {
        cmd: "dada box crystallize",
        title: "Кристаллизация в VM",
        desc: "Прототип выжил — тот же объект становится постоянной VM с доменом и TLS. Не пересоздание, не миграция: продолжение жизни того же окружения.",
      },
    ],
  },

  connect: {
    title: "Подключить за 60 секунд",
    subtitle:
      "Без формы и без ожидания оператора: агент сам поднимает бокс, как только у него есть доступ к платформе.",
    tabClaude: "Claude Code",
    tabOther: "Любой другой агент",
    claudeStep1Label: "Добавь маркетплейс",
    claudeStep1Cmd: "/plugin marketplace add DadaDevelopment/dada-cloud-console",
    claudeStep2Label: "Поставь плагин",
    claudeStep2Cmd: "/plugin install dada-cloud@dada-cloud",
    otherLabel: "Cursor, Codex, Claude Desktop — любой MCP-клиент. Конфиг руками:",
    otherCmd:
      '{\n  "mcpServers": {\n    "dada": {\n      "command": "npx",\n      "args": [\n        "-y",\n        "mcp-remote",\n        "https://console.dada-tuda.ru/mcp",\n        "--static-oauth-client-info",\n        "{\\"client_id\\":\\"dada-mcp\\"}"\n      ]\n    }\n  }\n}',
    otherNote:
      "client_id прибит явно — анонимная авто-регистрация клиента у нас закрыта, без этого флага клиент не подключится.",
    copyLabel: "Скопировать",
    footNote:
      "Нужен аккаунт Dada Cloud, вход через браузер. Free-план даёт 300 box-минут — этого хватит попробовать.",
    helpLink: "Нужна помощь с переездом — поговорим",
  },

  demo: {
    title: "Один и тот же объект от мысли до прода",
    subtitle:
      "Сегодня песочница и прод — разные продукты, разные форматы, и переход между ними это переписывание. Здесь — одна среда, у которой стадия жизненного цикла является свойством, а не сортом.",
    recordingLabel: "демо-запись, не живая сессия",
    playLabel: "Запустить демо",
    replayLabel: "Повторить",
    lines: [
      { kind: "cmd", text: "dada box up --warm" },
      { kind: "out", text: "образ: warm-base:2026.07 (node 24, python 3.13, go 1.25, docker)" },
      { kind: "ok", text: "бокс b-7f3a9c готов за 3.1 с · root · 8 vCPU / 16 ГБ" },
      { kind: "note", text: "агент подключён · инструменты исполняются в боксе" },
      { kind: "cmd", text: "dada box attach db --engine postgres --size small" },
      { kind: "ok", text: "postgres 17 подключён · DATABASE_URL проброшен в окружение" },
      { kind: "cmd", text: "dada box attach s3 --bucket assets" },
      { kind: "ok", text: "бакет assets создан · S3_* проброшены" },
      { kind: "cmd", text: "dada box expose --port 3000" },
      { kind: "ok", text: "https://b-7f3a9c.box.dada-tuda.ru · TLS выпущен" },
      { kind: "note", text: "прототип живёт, ссылку можно показать клиенту" },
      { kind: "cmd", text: "dada box crystallize --domain my-proto.ru" },
      { kind: "out", text: "накатываем на VM: файловую систему, тома, env, адрес · база и бакет остаются на месте, меняется владелец" },
      { kind: "ok", text: "VM vm-2c81 · то же окружение, теперь постоянное · домен привязан" },
    ],
  },

  crystal: {
    title: "Кристаллизация",
    subtitle:
      "Главное отличие от песочницы: эксперимент не выбрасывается и не пересобирается. Он переезжает целиком и взрослеет. Одно удостоверение проходит весь путь.",
    carriedTitle: "Что переносится",
    carried: [
      "файловая система бокса целиком, как есть",
      "тома и данные побайтово, без дампов и восстановлений (для базы внутри бокса — короткая остановка на финальной синхронизации)",
      "переменные окружения и секреты",
      "подцепленные база и бакет, теми же строками подключения",
      "публичный адрес — с временного на твой домен",
      "порты как есть, процессы — те же команды, перезапущенные один раз",
    ],
    note:
      "Это механический перенос объекта, а не пересборка по описанию. Модель в этом пути не участвует — значит нечему угадать неправильно.",
  },

  vps: {
    title: "Почему не просто VPS",
    subtitle: "Честные возражения и честные ответы.",
    rows: [
      {
        claim: "Возьму VPS за 300 ₽ и всё.",
        answer:
          "Для одного агента — разумно. Для трёх параллельно нужен не один VPS, а три, и они простаивают 90% времени. Бокс тарифицируется за активные минуты: простой не стоит ничего.",
      },
      {
        claim: "У меня уже есть сервер.",
        answer:
          "Тогда у тебя есть одно тело, которое агент постепенно засоряет и которое страшно ломать. Смысл бокса в одноразовости: снёс и поднял новый за секунды.",
      },
      {
        claim: "Настрою окружение сам, агент справится.",
        answer:
          "Справится — и потратит на это первые минуты каждой сессии и твои токены. Прогретый образ убирает этот налог.",
      },
      {
        claim: "А продакшен куда потом?",
        answer:
          "Туда же. Кристаллизация превращает бокс в постоянную VM с доменом, без переезда. Это и есть смысл: одно окружение от мысли до прода.",
      },
    ],
  },

  pricing: {
    title: "Гипотеза цены",
    subtitle:
      "Цифры ниже — гипотеза, которую мы проверяем этим превью. В форме есть вопрос, сколько это должно стоить по-твоему.",
    tiers: [
      {
        name: "Бокс",
        price: "за активные минуты",
        note: "Простой не тарифицируется. Забытый бокс усыпляется, а не жжёт счёт.",
      },
      {
        name: "База и S3",
        price: "по тарифам Dada Cloud",
        note: "Управляемый Postgres и объектное хранилище с бэкапами — те же, что в консоли.",
      },
      {
        name: "Кристаллизованная VM",
        price: "месячная подписка",
        note: "Постоянное тело, домен, TLS. Сопоставимо с VPS плюс управляемые сервисы.",
      },
    ],
    disclaimer:
      "На превью доступ бесплатный и выдаётся вручную. Тарификация включится не раньше, чем мы поймём, что боксом действительно пользуются.",
  },

  honesty: {
    title: "Что уже работает, а что нет",
    subtitle:
      "Box — не приватный превью на ручной выдаче, это работает прямо сейчас, самообслуживанием. Кристаллизация — ещё нет, вот честная граница.",
    worksTitle: "Работает сейчас",
    works: [
      "самообслуживание — бокс поднимается по вызову агента, без оператора и без заявки",
      "старт из тёплого пула — Ready за 350-450 мс, проверено живым MCP-вызовом",
      "публичный адрес с TLS — https://<box>-<port>.box.dada-tuda.ru отдаёт 200 с валидным сертификатом",
      "тарификация по активным минутам — считаем реальные деньги, не оценку",
      "управляемый Postgres, S3, домены и TLS для постоянных VM — основная платформа, в проде",
    ],
    notYetTitle: "Ещё нет",
    notYet: [
      "кристаллизация в один шаг — инструмент есть, но сквозной прогон бокс → прод вживую не доказан",
      "перенос без простоя — короткая пауза на финальной синхронизации остаётся, это не ноль",
      "гарантии времени старта в худшем случае — публично называем медиану, не SLA на хвост",
    ],
  },

  faq: {
    title: "Вопросы",
    items: [
      {
        q: "Вы перепродаёте доступ к Claude?",
        a: "Нет, и не планируем. Ты приводишь своего агента и свою подписку. Мы даём тело, в котором он работает. Так у нас нет ни наценки на токены, ни доступа к твоей подписке.",
      },
      {
        q: "Мой код и данные попадают к вам?",
        a: "Код и данные лежат в боксе, который развёрнут на нашей инфраструктуре в России. На превью боксы выдаются вручную и живут в изолированном контуре. Не заливай в превью то, что нельзя показывать посторонним, — сейчас это честнее любых обещаний.",
      },
      {
        q: "Чем это отличается от Codespaces или Gitpod?",
        a: "Те продукты строились для человека внутри среды, и умерли на человеческих претензиях — задержка и привязанность к своему сетапу. Агенту всё равно на задержку и у него нет дотфайлов. Плюс у них нет выпускного пути: бокс там не становится продом.",
      },
      {
        q: "Что если агент внутри что-то сломает?",
        a: "Ничего страшного, в этом смысл. Бокс одноразовый: сносишь и поднимаешь новый. Пока он не кристаллизован, ломать его — нормальный режим работы.",
      },
      {
        q: "Можно поднять несколько боксов сразу?",
        a: "Это основной сценарий, ради которого всё делается. Параллельные агенты — то, что локальная машина не выдерживает.",
      },
      {
        q: "Когда открытый доступ?",
        a: "Зависит от результата этого превью. Мы сознательно не строим автоматику до того, как убедимся, что боксом пользуются повторно, а не один раз из любопытства.",
      },
    ],
  },

  form: {
    title: "Получить доступ к превью",
    subtitle:
      "Доступ выдаём вручную и небольшими партиями. Расскажи, что собираешься запускать — так мы поднимем бокс под твой сценарий, а не универсальный.",
    emailLabel: "Email",
    emailPlaceholder: "you@example.com",
    contactLabel: "Telegram или другой контакт",
    contactPlaceholder: "@username — необязательно",
    agentLabel: "Каким агентом пользуешься",
    agentOptions: ["Claude Code", "Cursor", "Codex", "Несколькими", "Другим"],
    parallelLabel: "Сколько агентов гоняешь одновременно",
    parallelOptions: ["Один", "Два-три", "Больше трёх", "Пока не пробовал параллельно"],
    useCaseLabel: "Что будешь запускать в боксе",
    useCasePlaceholder:
      "Например: ночные рефакторинги на трёх агентах, прототипы для клиентов, эксперименты с моделями…",
    priceLabel: "Сколько это должно стоить, по-твоему",
    priceOptions: [
      "Только бесплатно",
      "До 500 ₽/мес",
      "500–2000 ₽/мес",
      "Больше 2000 ₽/мес",
      "Готов платить за минуты, а не за месяц",
    ],
    submit: "Оставить заявку",
    submitting: "Отправляем…",
    errorRequired: "Заполни email и что собираешься запускать.",
    errorEmail: "Похоже, email указан неверно.",
    errorGeneric: "Не получилось отправить. Попробуй ещё раз или напиши нам в Telegram.",
    successTitle: "Заявка принята",
    successBody:
      "Боксы на превью поднимает живой человек, поэтому напишем лично — обычно в течение рабочего дня. Никакой автоматической выдачи пока нет, и мы не будем делать вид, что она есть.",
    claimLabel: "Код заявки",
    crystalTitle: "Нужен перенос в постоянную VM?",
    crystalBody:
      "Это самая дорогая часть продукта, и мы строим её только если она действительно нужна. Отметь, что для тебя важно перенести.",
    crystalOptions: [
      "Данные базы без дампов и восстановления",
      "Файлы и тома как есть",
      "Секреты и переменные окружения",
      "Свой домен и TLS",
      "Запущенные процессы без перезапуска",
    ],
    crystalSubmit: "Мне это нужно",
    crystalDone: "Записали. Это сильно влияет на то, что мы будем строить дальше — спасибо.",
    privacy:
      "Используем контакт только чтобы выдать доступ и спросить, как прошло. Ни рассылок, ни передачи третьим лицам.",
    privacyLink: "Политика конфиденциальности",
  },
};

const en: BoxCopy = {
  badge: "Available now",
  heroTitle: "A body for your agent",
  heroSubtitle:
    "A root box boots in seconds. Your Claude, Cursor or Codex connects and works like it owns the machine — except it isn't your machine. Prototype survived? Crystallize it into a permanent VM with a domain.",
  heroPrimary: "Connect your agent",
  heroSecondary: "See how it works",
  heroNote: "You bring your own agent. We don't resell tokens.",

  spotlight: {
    eyebrow: "New · private preview",
    title: "Dada Box — a body for your agent",
    body: "A root box in seconds; your Claude or Cursor connects and works like it owns the machine. A database and S3 attach mid-flight, and a surviving prototype crystallizes into a permanent VM with a domain — no migration.",
    bullets: [
      "Ten agents in parallel instead of one",
      "Your laptop stays clean",
      "One environment from thought to production",
    ],
    cta: "See Box",
  },

  problem: {
    title: "A laptop is a bad body for an agent",
    subtitle:
      "The agent doesn't get tired, isn't afraid of breaking the system and will happily work all night. The constraint isn't the agent — it's the machine it runs on.",
    items: [
      {
        title: "The third agent kills your laptop",
        desc: "One agent is fine. Three in parallel means fans at full tilt, builds queued and an unresponsive IDE. Parallelism hits the hardware you carry around.",
      },
      {
        title: "Agents leave a mess behind",
        desc: "Four Node versions, global packages, 12 GB of images and a systemd unit you'll discover next month. Your own machine is too expensive to risk.",
      },
      {
        title: "Nothing to show",
        desc: "The prototype lives on localhost. To hand a client a link you need an address, TLS, a database and somewhere that survives closing the lid.",
      },
    ],
  },

  how: {
    title: "How it works",
    subtitle: "Four steps. The first takes seconds, the last turns an experiment into production.",
    steps: [
      {
        cmd: "dada box up",
        title: "A body in seconds",
        desc: "A warm root box with node, python, go, docker, git and compilers already inside. The agent doesn't spend its first minutes on apt install.",
      },
      {
        cmd: "agent connects",
        title: "Your agent, our body",
        desc: "Claude Code, Cursor or Codex stay with you; tools execute in the box. We don't host the model and never see your subscription.",
      },
      {
        cmd: "dada box attach db / s3",
        title: "Resources mid-flight",
        desc: "Need a database or a bucket? They attach to the running box, with no migration and no config rewrite. Managed, with backups.",
      },
      {
        cmd: "dada box crystallize",
        title: "Crystallize into a VM",
        desc: "The prototype survived — the same object becomes a permanent VM with a domain and TLS. Not a rebuild, not a migration: the same environment, continued.",
      },
    ],
  },

  connect: {
    title: "Connect in 60 seconds",
    subtitle:
      "No form, no waiting on an operator: the agent boots its own box the moment it has access to the platform.",
    tabClaude: "Claude Code",
    tabOther: "Any other agent",
    claudeStep1Label: "Add the marketplace",
    claudeStep1Cmd: "/plugin marketplace add DadaDevelopment/dada-cloud-console",
    claudeStep2Label: "Install the plugin",
    claudeStep2Cmd: "/plugin install dada-cloud@dada-cloud",
    otherLabel: "Cursor, Codex, Claude Desktop — any MCP client. Config by hand:",
    otherCmd:
      '{\n  "mcpServers": {\n    "dada": {\n      "command": "npx",\n      "args": [\n        "-y",\n        "mcp-remote",\n        "https://console.dada-tuda.ru/mcp",\n        "--static-oauth-client-info",\n        "{\\"client_id\\":\\"dada-mcp\\"}"\n      ]\n    }\n  }\n}',
    otherNote:
      "client_id is pinned on purpose — anonymous client auto-registration is closed on our side, and a client without this flag will not connect.",
    copyLabel: "Copy",
    footNote:
      "Needs a Dada Cloud account, signed in through the browser. The free plan gives 300 box-minutes — enough to try it.",
    helpLink: "Need help migrating instead — let's talk",
  },

  demo: {
    title: "One object, from thought to production",
    subtitle:
      "Today a sandbox and production are different products in different formats, and moving between them is a rewrite. Here it's one environment whose lifecycle stage is a property, not a species.",
    recordingLabel: "recorded demo, not a live session",
    playLabel: "Play demo",
    replayLabel: "Replay",
    lines: [
      { kind: "cmd", text: "dada box up --warm" },
      { kind: "out", text: "image: warm-base:2026.07 (node 24, python 3.13, go 1.25, docker)" },
      { kind: "ok", text: "box b-7f3a9c ready in 3.1s · root · 8 vCPU / 16 GB" },
      { kind: "note", text: "agent connected · tools executing inside the box" },
      { kind: "cmd", text: "dada box attach db --engine postgres --size small" },
      { kind: "ok", text: "postgres 17 attached · DATABASE_URL injected" },
      { kind: "cmd", text: "dada box attach s3 --bucket assets" },
      { kind: "ok", text: "bucket assets created · S3_* injected" },
      { kind: "cmd", text: "dada box expose --port 3000" },
      { kind: "ok", text: "https://b-7f3a9c.box.dada-tuda.ru · TLS issued" },
      { kind: "note", text: "the prototype is live — the link is shareable" },
      { kind: "cmd", text: "dada box crystallize --domain my-proto.dev" },
      { kind: "out", text: "applying onto the VM: filesystem, volumes, env, address · db and bucket stay put, only the owner changes" },
      { kind: "ok", text: "VM vm-2c81 · same environment, now permanent · domain bound" },
    ],
  },

  crystal: {
    title: "Crystallization",
    subtitle:
      "The real difference from a sandbox: the experiment is neither thrown away nor rebuilt. It moves across whole, and grows up. One identity for the whole journey.",
    carriedTitle: "What carries over",
    carried: [
      "the box filesystem, exactly as it is",
      "volumes and data byte for byte, with no dump-and-restore (a database inside the box needs a short pause for the final sync)",
      "environment variables and secrets",
      "attached database and bucket, same connection strings",
      "the public address — from a temporary one to your domain",
      "the same ports, and the same processes relaunched once — same commands, same working dirs",
    ],
    note:
      "This is a mechanical move of an object, not a rebuild from a description. No model participates in this path — so there is nothing to guess wrong.",
  },

  vps: {
    title: "Why not just a VPS",
    subtitle: "Honest objections, honest answers.",
    rows: [
      {
        claim: "I'll grab a $5 VPS and be done.",
        answer:
          "For a single agent, reasonable. For three in parallel you need three, and they idle 90% of the time. A box bills active minutes: idle costs nothing.",
      },
      {
        claim: "I already have a server.",
        answer:
          "Then you have one body that agents slowly pollute and that you're afraid to break. The point of a box is disposability: destroy it and boot a new one in seconds.",
      },
      {
        claim: "The agent can set up the environment itself.",
        answer:
          "It can — spending the first minutes of every session and your tokens doing it. A warm image removes that tax.",
      },
      {
        claim: "Where does production live then?",
        answer:
          "Same place. Crystallization turns the box into a permanent VM with a domain, with no migration. That's the whole point: one environment from thought to production.",
      },
    ],
  },

  pricing: {
    title: "Pricing hypothesis",
    subtitle:
      "The numbers below are a hypothesis this preview is testing. The form asks what you think it should cost.",
    tiers: [
      {
        name: "Box",
        price: "per active minute",
        note: "Idle isn't billed. A forgotten box goes to sleep instead of burning your budget.",
      },
      {
        name: "Database and S3",
        price: "standard Dada Cloud rates",
        note: "Managed Postgres and object storage with backups — the same ones as in the console.",
      },
      {
        name: "Crystallized VM",
        price: "monthly",
        note: "A permanent body, domain, TLS. Comparable to a VPS plus managed services.",
      },
    ],
    disclaimer:
      "Preview access is free and granted by hand. Billing won't switch on until we know boxes are actually being used.",
  },

  honesty: {
    title: "What works today and what doesn't",
    subtitle:
      "Box isn't a hand-provisioned private preview anymore — it's self-service, working right now. Crystallization is the honest edge that's still open.",
    worksTitle: "Works today",
    works: [
      "self-service — a box boots on the agent's own call, no operator and no request",
      "boot from a warm pool — Ready in 350-450ms, verified with a live MCP call",
      "a public address with TLS — https://<box>-<port>.box.dada-tuda.ru serves 200 with a valid certificate",
      "per-active-minute billing — we track real money, not an estimate",
      "managed Postgres, S3, domains and TLS for permanent VMs — our core platform, in production",
    ],
    notYetTitle: "Not yet",
    notYet: [
      "one-step crystallization — the tool exists, but an end-to-end box-to-production run hasn't been proven live",
      "zero-downtime promotion — there's still a short pause on the final sync, that's not zero",
      "worst-case start-time guarantees — we quote the median publicly, not a tail SLA",
    ],
  },

  faq: {
    title: "Questions",
    items: [
      {
        q: "Are you reselling access to Claude?",
        a: "No, and we don't plan to. You bring your own agent and your own subscription. We provide the body it works in. That way there's no token markup and no access to your subscription.",
      },
      {
        q: "Do my code and data end up with you?",
        a: "Code and data live in a box on our infrastructure in Russia. During the preview, boxes are provisioned by hand and live in an isolated segment. Don't put anything into the preview that outsiders mustn't see — right now that's more honest than any promise.",
      },
      {
        q: "How is this different from Codespaces or Gitpod?",
        a: "Those were built for a human inside the environment, and died on human complaints — latency and attachment to a personal setup. An agent doesn't care about latency and has no dotfiles. They also have no graduation path: the box never becomes production.",
      },
      {
        q: "What if the agent breaks something inside?",
        a: "That's the point. The box is disposable: destroy it and boot another. Until it's crystallized, breaking it is normal operation.",
      },
      {
        q: "Can I run several boxes at once?",
        a: "That's the primary scenario this exists for. Parallel agents are exactly what a local machine can't take.",
      },
      {
        q: "When does it open up?",
        a: "It depends on this preview. We're deliberately not building automation until we're sure boxes get used more than once out of curiosity.",
      },
    ],
  },

  form: {
    title: "Request preview access",
    subtitle:
      "We grant access by hand, in small batches. Tell us what you plan to run so we can prepare a box for your scenario rather than a generic one.",
    emailLabel: "Email",
    emailPlaceholder: "you@example.com",
    contactLabel: "Telegram or another contact",
    contactPlaceholder: "@username — optional",
    agentLabel: "Which agent do you use",
    agentOptions: ["Claude Code", "Cursor", "Codex", "Several", "Something else"],
    parallelLabel: "How many agents do you run at once",
    parallelOptions: ["One", "Two or three", "More than three", "Haven't tried parallel yet"],
    useCaseLabel: "What will you run in the box",
    useCasePlaceholder:
      "For example: overnight refactors across three agents, client prototypes, model experiments…",
    priceLabel: "What should this cost, in your view",
    priceOptions: [
      "Free only",
      "Up to $5/mo",
      "$5–25/mo",
      "More than $25/mo",
      "Happy to pay per minute rather than per month",
    ],
    submit: "Request access",
    submitting: "Sending…",
    errorRequired: "Please fill in your email and what you plan to run.",
    errorEmail: "That email doesn't look right.",
    errorGeneric: "Couldn't send that. Try again, or ping us on Telegram.",
    successTitle: "Request received",
    successBody:
      "Preview boxes are provisioned by a human, so we'll reply personally — usually within a business day. There's no automatic provisioning yet, and we won't pretend otherwise.",
    claimLabel: "Request code",
    crystalTitle: "Do you need the move to a permanent VM?",
    crystalBody:
      "It's the most expensive part of the product, and we'll only build it if it's genuinely needed. Mark what matters for you to carry over.",
    crystalOptions: [
      "Database data with no dump-and-restore",
      "Files and volumes as they are",
      "Secrets and environment variables",
      "My own domain and TLS",
      "Running processes without a restart",
    ],
    crystalSubmit: "I need this",
    crystalDone: "Noted. This weighs heavily on what we build next — thank you.",
    privacy:
      "We use your contact only to grant access and ask how it went. No newsletters, no sharing with third parties.",
    privacyLink: "Privacy policy",
  },
};

export const boxCopy: Record<Locale, BoxCopy> = { ru, en };
