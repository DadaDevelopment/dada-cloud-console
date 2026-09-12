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
  heroTitle: "Облачный компьютер для вашего AI-агента",
  heroSubtitle:
    "Пусть Claude, Cursor или Codex пишет код, ставит зависимости и запускает сборки в Box. На вашем ноутбуке ничего устанавливать не нужно.",
  heroPrimary: "Подключить агента",
  heroSecondary: "Как это работает",
  heroNote: "Вы используете своего агента и свою подписку на модель.",

  spotlight: {
    eyebrow: "Dada Box",
    title: "Отдельный компьютер для вашего AI-агента",
    body: "Запускайте задачи Claude, Cursor или Codex в облачном окружении с root-доступом. Рабочая машина остаётся свободной.",
    bullets: [
      "Ваш агент и инструменты",
      "Окружение в облаке",
      "Состояние в консоли"
    ],
    cta: "Как работает Box"
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
    title: "Подключите своего агента",
    subtitle:
      "Выберите клиент, добавьте DADA Cloud и войдите в аккаунт. Затем попросите агента создать Box для вашей задачи.",
    tabClaude: "Claude Code",
    tabOther: "Любой другой агент",
    claudeStep1Label: "1. Добавьте маркетплейс в Claude Code",
    claudeStep1Cmd: "/plugin marketplace add DadaDevelopment/dada-cloud-console",
    claudeStep2Label: "2. Установите плагин",
    claudeStep2Cmd: "/plugin install dada-cloud@dada-cloud",
    otherLabel: "Добавьте этот конфиг в настройки MCP вашего клиента:",
    otherCmd:
      '{\n  "mcpServers": {\n    "dada": {\n      "command": "npx",\n      "args": [\n        "-y",\n        "mcp-remote",\n        "https://console.dada-tuda.ru/mcp",\n        "--static-oauth-client-info",\n        "{\\"client_id\\":\\"dada-mcp\\"}"\n      ]\n    }\n  }\n}',
    otherNote:
      "Оставьте client_id dada-mcp в конфиге: он нужен для входа через браузер.",
    copyLabel: "Скопировать",
    footNote:
      "Нужен аккаунт DADA Cloud. План Free включает 300 активных box-минут в календарный месяц.",
    helpLink: "Нужна помощь с подключением?",
  },

  demo: {
    title: "Пример работы с Box",
    subtitle:
      "Иллюстрация команд и ответов. Это заранее подготовленный сценарий, а не запущенное окружение.",
    recordingLabel: "Иллюстративный сценарий",
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
    title: "Перенос Box в постоянную VM",
    subtitle:
      "Экспериментальная возможность: перенести приложение и его окружение на постоянный сервер. Сквозной перенос пока не заявлен как гарантированно работающий сценарий.",
    carriedTitle: "Что предусмотрено переносом",
    carried: [
      "файлы приложения и подключённые тома",
      "переменные окружения и секреты",
      "команда запуска и опубликованные порты",
      "подключённые ресурсы и собственный домен",
    ],
    note:
      "Автоматический перенос в постоянную VM остаётся экспериментальным. Потребуется короткая остановка; работа без простоя не гарантируется. VM оплачивается по месячному тарифу.",
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
    title: "Оплата за время работы",
    subtitle:
      "План Free включает 300 активных box-минут в месяц. Дополнительные ресурсы учитываются отдельно.",
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
      "Минуты входят в квоту вашего плана. Проверяйте лимиты и условия перед запуском.",
  },

  honesty: {
    title: "Возможности и ограничения",
    subtitle:
      "Создание Box и публикация приложения доступны через агента. Перенос в постоянную VM имеет отдельные ограничения.",
    worksTitle: "Доступно",
    works: [
      "Создание окружения по запросу агента через MCP",
      "Запуск команд, сборок и приложений с root-доступом",
      "Публикация порта приложения по адресу с HTTPS",
      "Учёт активного времени Box в квотах плана",
    ],
    notYetTitle: "Ограничения",
    notYet: [
      "Автоматический перенос Box в постоянную VM остаётся экспериментальным",
      "При переносе предусмотрена короткая остановка, без гарантии нулевого простоя",
      "Время запуска зависит от наличия подготовленных окружений; фиксированное время не гарантируется",
    ],
  },

  faq: {
    title: "Вопросы о Box",
    items: [
      { q: "Нужна отдельная подписка на AI-модель?", a: "Да. Вы подключаете своего агента и свою подписку или ключ модели. DADA Cloud предоставляет вычислительное окружение и не перепродаёт доступ к Claude, Cursor или Codex." },
      { q: "Где хранятся код и данные?", a: "В Box на инфраструктуре DADA Cloud в России. Это удалённое окружение: загруженные файлы хранятся в облаке, а не только на вашем ноутбуке." },
      { q: "Чем Box отличается от обычного VPS?", a: "Box подходит для отдельных задач агента: создать окружение, выполнить работу и удалить его. VPS рассчитан на постоянную работу. Автоматический перенос Box в постоянную VM остаётся экспериментальной возможностью." },
      { q: "Что будет, если агент сломает окружение?", a: "Можно удалить Box и создать новый. Перед удалением сохраните нужные файлы: удаление окружения не заменяет резервное копирование." },
      { q: "Можно запустить несколько боксов?", a: "Да, отдельные задачи можно выполнять в отдельных окружениях. Их суммарное потребление учитывается в лимитах вашего плана." },
      { q: "Нужно ждать подтверждения доступа?", a: "Для самостоятельного запуска через MCP заявка не нужна. Подключите клиент и войдите в аккаунт DADA Cloud. Форма на странице предназначена для помощи с вашим сценарием." },
    ],
  },

  form: {
    title: "Нужна помощь с Box?",
    subtitle:
      "Расскажите о задаче и оставьте контакт. Это запрос команде, а не обязательный шаг для подключения агента.",
    emailLabel: "Email",
    emailPlaceholder: "you@example.com",
    contactLabel: "Telegram или другой контакт",
    contactPlaceholder: "@username — необязательно",
    agentLabel: "Каким агентом пользуетесь",
    agentOptions: ["Claude Code", "Cursor", "Codex", "Несколькими", "Другим"],
    parallelLabel: "Сколько агентов запускаете одновременно",
    parallelOptions: ["Один", "Два-три", "Больше трёх", "Пока не пробовал параллельно"],
    useCaseLabel: "Что хотите запустить в Box",
    useCasePlaceholder:
      "Например: ночные рефакторинги на трёх агентах, прототипы для клиентов, эксперименты с моделями…",
    priceLabel: "Какой бюджет вам подходит",
    priceOptions: [
      "Только бесплатно",
      "До 500 ₽/мес",
      "500–2000 ₽/мес",
      "Больше 2000 ₽/мес",
      "Готов платить за минуты, а не за месяц",
    ],
    submit: "Отправить запрос",
    submitting: "Отправляем…",
    errorRequired: "Укажите email и задачу для Box.",
    errorEmail: "Проверьте адрес email.",
    errorGeneric: "Не удалось отправить запрос. Попробуйте ещё раз.",
    successTitle: "Запрос принят",
    successBody:
      "Сохранили ваш запрос. Команда сможет связаться с вами по указанному контакту. Отправка формы не создаёт Box.",
    claimLabel: "Код заявки",
    crystalTitle: "Нужен перенос в постоянную VM?",
    crystalBody:
      "Если планируете постоянный сервер, отметьте, что важно сохранить при переносе.",
    crystalOptions: [
      "Данные базы без дампов и восстановления",
      "Файлы и тома как есть",
      "Секреты и переменные окружения",
      "Свой домен и TLS",
      "Запущенные процессы без перезапуска",
    ],
    crystalSubmit: "Мне это нужно",
    crystalDone: "Сохранили ваши пожелания.",
    privacy:
      "Используем контакт для ответа на запрос и уточнения вашего сценария.",
    privacyLink: "Политика конфиденциальности",
  },
};

const en: BoxCopy = {
  badge: "Available now",
  heroTitle: "A cloud computer for your AI agent",
  heroSubtitle:
    "Let Claude, Cursor or Codex write code, install dependencies and run builds in a Box. Keep those tools off your own laptop.",
  heroPrimary: "Connect your agent",
  heroSecondary: "How it works",
  heroNote: "Bring your own agent and model subscription.",

  spotlight: {
    eyebrow: "Dada Box",
    title: "A separate computer for your AI agent",
    body: "Run Claude, Cursor or Codex tasks in a cloud environment with root access. Keep your own machine free.",
    bullets: [
      "Your agent and tools",
      "A cloud environment",
      "Visible in your console"
    ],
    cta: "How Box works"
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
    title: "Connect your agent",
    subtitle:
      "Choose a client, add DADA Cloud and sign in. Then ask your agent to create a Box for your task.",
    tabClaude: "Claude Code",
    tabOther: "Any other agent",
    claudeStep1Label: "1. Add the marketplace in Claude Code",
    claudeStep1Cmd: "/plugin marketplace add DadaDevelopment/dada-cloud-console",
    claudeStep2Label: "2. Install the plugin",
    claudeStep2Cmd: "/plugin install dada-cloud@dada-cloud",
    otherLabel: "Add this configuration to your client’s MCP settings:",
    otherCmd:
      '{\n  "mcpServers": {\n    "dada": {\n      "command": "npx",\n      "args": [\n        "-y",\n        "mcp-remote",\n        "https://console.dada-tuda.ru/mcp",\n        "--static-oauth-client-info",\n        "{\\"client_id\\":\\"dada-mcp\\"}"\n      ]\n    }\n  }\n}',
    otherNote:
      "Keep client_id dada-mcp in the config: it is required for browser sign-in.",
    copyLabel: "Copy",
    footNote:
      "A DADA Cloud account is required. Free includes 300 active box-minutes per calendar month.",
    helpLink: "Need help connecting?",
  },

  demo: {
    title: "An example Box workflow",
    subtitle:
      "An illustration of commands and responses. This is a prepared scenario, not a running environment.",
    recordingLabel: "Illustrative scenario",
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
    title: "Move a Box to a permanent VM",
    subtitle:
      "An experimental capability for moving an app and its environment to a permanent server. The end-to-end migration is not yet offered as a guaranteed working flow.",
    carriedTitle: "What the migration is designed to carry",
    carried: [
      "application files and attached volumes",
      "environment variables and secrets",
      "the startup command and published ports",
      "connected resources and a custom domain",
    ],
    note:
      "Automatic migration to a permanent VM remains experimental. A brief pause is required; zero downtime is not guaranteed. The VM uses monthly billing.",
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
    title: "Pay for active time",
    subtitle:
      "Free includes 300 active box-minutes each month. Additional resources are accounted for separately.",
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
      "Minutes count against your plan’s allowance. Check the limits and terms before starting.",
  },

  honesty: {
    title: "Capabilities and limits",
    subtitle:
      "Create a Box and publish an app through your agent. Migration to a permanent VM has separate limitations.",
    worksTitle: "Available",
    works: [
      "Create an environment through your agent over MCP",
      "Run commands, builds and apps with root access",
      "Publish an application port at an HTTPS address",
      "Active Box time counts against your plan’s allowance",
    ],
    notYetTitle: "Limitations",
    notYet: [
      "Automatic Box migration to a permanent VM remains experimental",
      "Migration requires a brief pause; zero downtime is not guaranteed",
      "Startup time depends on warm environment availability; no fixed startup time is guaranteed",
    ],
  },

  faq: {
    title: "Box questions",
    items: [
      { q: "Do I need my own AI model subscription?", a: "Yes. Bring your own agent and model subscription or API key. DADA Cloud provides the computing environment and does not resell access to Claude, Cursor or Codex." },
      { q: "Where do my code and data live?", a: "Inside a Box on DADA Cloud infrastructure in Russia. It is a remote environment: uploaded files are stored in the cloud, not only on your laptop." },
      { q: "How is this different from a regular VPS?", a: "A Box is designed for individual agent tasks: create an environment, do the work, then delete it. A VPS is intended to run continuously. Automatic migration from Box to a permanent VM remains experimental." },
      { q: "What if the agent breaks its environment?", a: "You can delete the Box and create a new one. Save the files you need before deleting it: replacing an environment is not a backup." },
      { q: "Can I run several boxes?", a: "Yes. Separate tasks can run in separate environments. Their combined usage counts against your plan’s limits." },
      { q: "Do I need to wait for access approval?", a: "No request is needed for self-service access over MCP. Connect your client and sign in to DADA Cloud. The form on this page is for help with your use case." },
    ],
  },

  form: {
    title: "Need help with Box?",
    subtitle:
      "Tell us about your task and leave a contact. This is a request to the team, not a required step to connect your agent.",
    emailLabel: "Email",
    emailPlaceholder: "you@example.com",
    contactLabel: "Telegram or another contact",
    contactPlaceholder: "@username — optional",
    agentLabel: "Which agent do you use?",
    agentOptions: ["Claude Code", "Cursor", "Codex", "Several", "Something else"],
    parallelLabel: "How many agents do you run at once?",
    parallelOptions: ["One", "Two or three", "More than three", "Haven't tried parallel yet"],
    useCaseLabel: "What would you like to run in Box?",
    useCasePlaceholder:
      "For example: overnight refactors across three agents, client prototypes, model experiments…",
    priceLabel: "What budget works for you?",
    priceOptions: [
      "Free only",
      "Up to $5/mo",
      "$5–25/mo",
      "More than $25/mo",
      "Happy to pay per minute rather than per month",
    ],
    submit: "Send request",
    submitting: "Sending…",
    errorRequired: "Enter your email and describe your task.",
    errorEmail: "Check your email address.",
    errorGeneric: "Could not send the request. Please try again.",
    successTitle: "Request received",
    successBody:
      "We saved your request. The team can follow up using the contact you provided. Submitting this form does not create a Box.",
    claimLabel: "Request code",
    crystalTitle: "Do you need the move to a permanent VM?",
    crystalBody:
      "If you need a permanent server, select what matters to you during migration.",
    crystalOptions: [
      "Database data with no dump-and-restore",
      "Files and volumes as they are",
      "Secrets and environment variables",
      "My own domain and TLS",
      "Running processes without a restart",
    ],
    crystalSubmit: "I need this",
    crystalDone: "Your preferences have been saved.",
    privacy:
      "We use your contact to respond to the request and clarify your use case.",
    privacyLink: "Privacy policy",
  },
};

export const boxCopy: Record<Locale, BoxCopy> = { ru, en };
