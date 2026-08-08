// Package solutions holds the ready-made project catalog: real open-source
// projects a customer deploys into a cloud (Kubernetes) environment in one
// click, when they have nothing of their own to deploy yet.
//
// It replaces the starter templates (dada-nextjs-starter and friends, see
// api/demo_apps.go). A starter proves that a deploy happened and is then reaped;
// nobody opens it twice, because there is nothing inside it. A whiteboard, a
// toolbox, an offline documentation browser — those a customer might keep, and
// they arrive on the platform the same way the customer's own code will.
//
// The catalog is a frozen Go variable — same idiom, and same reason, as
// internal/boxcatalog and internal/profiles: edit this file and redeploy.
//
// # Built from source, on purpose
//
// The built track takes a REPOSITORY, not a published image. Its install runs
// the ordinary customer path — link the public repo, detect the framework, build it
// in our pipeline, deploy the result — so a catalog card exercises exactly the
// machinery a customer's first repository will hit. Pulling `n8nio/n8n:2.34.2`
// would make prettier cards and prove nothing: it would be our platform running
// somebody else's build.
//
// That choice has a price, and it is the point. Real repositories are monorepos,
// build in two stages, read their port from an environment variable, and put
// their Dockerfile three directories down. Where the auto-detector misses, the
// card fails visibly and we fix the detector — which is worth more than a
// catalog that never touches it. tasks/autodeploy-benchmark-50-oss.md is that
// feedback loop written down.
//
// # Two tracks, and why the second one exists
//
// An app created by the build pipeline takes its spec from git_repos
// (port/replicas/profile) and there is no volume in that spec, so a project
// that keeps state on disk would silently lose it on every redeploy. That ruled
// out every stateful project — which is most of what a person actually wants to
// run on a cloud.
//
// So an entry may instead name a published Image (and, when it keeps state, a
// Volume). Installing one skips the build pipeline entirely and creates an
// ordinary image app, the same shape a customer gets from POST /apps: a real
// volume, a real backup story, no forty-minute first build. The card says which
// track it took, because the two are not the same promise — a built entry
// proves our pipeline handles that repository, an image entry proves nothing
// about the pipeline and is not meant to.
package solutions

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Category groups projects in the catalog UI.
type Category string

const (
	CategoryDevTools     Category = "dev-tools"
	CategoryDocuments    Category = "documents"
	CategoryAI           Category = "ai"
	CategoryData         Category = "data"
	CategoryMedia        Category = "media"
	CategorySecurity     Category = "security"
	CategoryProductivity Category = "productivity"
	CategoryMonitoring   Category = "monitoring"
	CategoryAutomation   Category = "automation"
)

// CategoryTitles is the display name of every category, in display order. The
// console renders the catalog grouped by this list rather than by whatever
// order the entries happen to be in, so a new entry cannot quietly invent a
// group nobody named.
var CategoryTitles = []struct {
	Category Category
	Title    string
}{
	{CategoryDevTools, "Инструменты разработчика"},
	{CategoryAutomation, "Автоматизация"},
	{CategoryData, "Данные и базы"},
	{CategoryAI, "ИИ"},
	{CategoryDocuments, "Документы и заметки"},
	{CategoryProductivity, "Работа и команда"},
	{CategoryMonitoring, "Мониторинг"},
	{CategorySecurity, "Безопасность"},
	{CategoryMedia, "Медиа"},
}

// ParamKind is how the console renders one install parameter, and how the API
// validates it.
type ParamKind string

const (
	ParamText   ParamKind = "text"
	ParamSecret ParamKind = "secret"
	ParamSelect ParamKind = "select"
)

// Param is one value the customer supplies at install time. EnvKey is the
// container environment variable it lands in.
type Param struct {
	Key         string
	EnvKey      string
	Label       string
	Help        string
	Kind        ParamKind
	Required    bool
	Default     string
	Options     []string
	Placeholder string
}

// Volume is the persistent data directory an image entry mounts. Size is a
// Kubernetes quantity ("10Gi"); the storage class is left to the platform
// default so the catalog does not pin a class that may not exist everywhere.
//
// FSGroup is the group id the volume is handed to, and it is required for every
// image that runs as a non-root user: a fresh volume arrives owned by root, so
// Grafana (472), Metabase (2000) or anything running as 1000 crash-loops on
// its own data directory until the group is set. Zero means the image runs as
// root and needs nothing.
type Volume struct {
	Path    string
	Size    string
	FSGroup int64
}

// Solution is one installable open-source project.
//
// Aliases are the other words a person types for this project: the English name
// when the product is known by a Russian one, the abbreviation, the thing it
// replaces. Without them the resolver only answers people who already know what
// an entry is called, which is the opposite of who the catalog exists for.
type Solution struct {
	Slug     string
	Name     string
	Tagline  string
	About    string
	Bullets  []string
	Category Category
	Homepage string
	License  string
	Aliases  []string

	// Repo is the public GitHub repository, "owner/name". It is cloned without
	// an installation token: these are public projects, and requiring a customer
	// to connect a GitHub account before they can see anything deploy is the
	// exact wall the starter cards existed to get around.
	Repo string
	// Branch is the branch to build. Pinned by name rather than by commit
	// because a card should ship what upstream currently calls released; a
	// broken upstream build is a signal we want to see, not hide behind a pin.
	Branch string
	// RootDir is the directory to build from, "." for the repository root.
	RootDir string
	// Framework overrides auto-detection. Set to "dockerfile" for every entry
	// that ships its own Dockerfile: the repository's own build is what upstream
	// tests, and second-guessing it with a framework template is how a card
	// starts failing on an upstream refactor nobody told us about.
	Framework string
	// Port is what the built image actually listens on, read from the
	// repository's Dockerfile rather than assumed from the framework.
	Port int
	// Profile is the compute envelope (small | medium | large).
	Profile string

	// Image is a published container image, "repo:tag". When it is set the
	// install skips the build pipeline and creates an image app directly, and
	// Repo/Branch/RootDir/Framework are not used for anything but the card.
	// Tags are pinned to a major line rather than to "latest": a card that
	// silently follows upstream's newest tag is a card that changes what it
	// installs without anyone deciding to.
	Image string
	// Volume is the persistent data directory an image entry needs. Only image
	// entries may have one, because the build track has nowhere to put it.
	Volume *Volume
	// IconRepo is where the logo comes from for entries that have no Repo of
	// their own to take it from: a GitHub account name, or "owner/name" when
	// that reads better at the call site. Only the account half is used.
	IconRepo string
	// Env is fixed configuration the image needs and the customer has no opinion
	// about — where an image keeps its database file, which port it binds. It is
	// separate from Params because a value nobody should be asked for does not
	// belong in a form; a Param of the same name wins over it.
	Env map[string]string

	Params []Param

	// Needs are the managed resources this project cannot run without, by engine
	// name ("postgres"). Declaring them here rather than telling the customer to
	// go and create a database afterwards is what turns a two-step install into
	// one button: the installer orders the database in the same call and binds it
	// to the app, so the project comes up already connected.
	Needs []string

	// Warning is the one thing a customer must read before deploying, rendered
	// prominently rather than as fine print.
	Warning string
	// FirstRun is what to do once it is up.
	FirstRun string
	// BuildNote is what to expect from the BUILD, not the product: an honest
	// heads-up that a real repository takes longer to build than a starter.
	BuildNote string
}

// V1 is the catalog. Frozen at deploy time.
//
// The first four entries are built from source: each carries a Dockerfile at
// its repository root and listens on the port recorded here. The rest run a
// published image, pinned to a tag that was checked against the registry when
// it was written here — a tag nobody verified is a card that fails at pull time,
// which is the one failure the customer reads as "the platform is broken".
//
// Ports and data paths come from upstream's own compose file or Dockerfile, not
// from a guess: a wrong port makes a card that deploys green and answers
// nothing, and a wrong data path makes one that loses everything on restart.
var V1 = []Solution{
	{
		Slug:     "excalidraw",
		Name:     "Excalidraw",
		Tagline:  "Доска для схем от руки — та самая, с «карандашным» стилем",
		Category: CategoryDevTools,
		Homepage: "https://excalidraw.com",
		License:  "MIT",
		Aliases:  []string{"экскалидроу", "доска", "whiteboard", "схемы", "диаграммы", "draw", "рисование", "miro"},
		About: "Виртуальная доска для схем, диаграмм и набросков: рисует так, будто чертили от " +
			"руки на бумаге. Рисунки хранятся в браузере, экспорт в PNG и SVG, ссылка на " +
			"доску открывается у коллеги без регистрации.",
		Bullets: []string{
			"Схемы и наброски в «карандашном» стиле",
			"Экспорт в PNG, SVG и файл проекта",
			"Ничего не хранится на сервере — всё в браузере",
		},
		Repo:      "excalidraw/excalidraw",
		Branch:    "master",
		RootDir:   ".",
		Framework: "dockerfile",
		Port:      80,
		Profile:   "small",
		FirstRun:  "Открывайте и рисуйте — регистрация не нужна, доски сохраняются в браузере.",
		BuildNote: "Сборка фронтенда занимает несколько минут: это настоящий репозиторий, а не заготовка.",
	},
	{
		Slug:     "it-tools",
		Name:     "IT-Tools",
		Tagline:  "Больше сотни инструментов разработчика в одном месте",
		Category: CategoryDevTools,
		Homepage: "https://it-tools.tech",
		License:  "GPL-3.0",
		Aliases:  []string{"ittools", "tools", "инструменты", "утилиты", "jwt", "hash", "хеш", "uuid", "json", "конвертер"},
		About: "Инструменты, за которыми обычно идут на случайные сайты: разбор JWT, хеши и UUID, " +
			"форматирование JSON, SQL и XML, конвертеры дат, кодировок и цветов, генератор " +
			"паролей. Всё считается в браузере и никуда не отправляется.",
		Bullets: []string{
			"Больше 100 инструментов: хеши, JWT, форматтеры, конвертеры",
			"Всё вычисляется в браузере",
			"Никаких данных на сервере",
		},
		Repo:      "CorentinTh/it-tools",
		Branch:    "main",
		RootDir:   ".",
		Framework: "dockerfile",
		Port:      80,
		Profile:   "small",
		FirstRun:  "Открывайте и пользуйтесь — учётная запись не нужна.",
	},
	{
		Slug:     "gitingest",
		Name:     "Gitingest",
		Tagline:  "Превращает любой репозиторий в один текст для языковой модели",
		Category: CategoryAI,
		Homepage: "https://gitingest.com",
		License:  "MIT",
		Aliases:  []string{"ingest", "repo2text", "llm", "нейросеть", "контекст", "промпт", "ai"},
		About: "Принимает ссылку на репозиторий и собирает его в один структурированный текст, " +
			"который можно целиком отдать модели: дерево файлов, содержимое, оценка размера " +
			"в токенах. Полезно ровно тогда, когда нужно объяснить модели незнакомый проект.",
		Bullets: []string{
			"Репозиторий → один текст с деревом файлов",
			"Оценка размера в токенах",
			"Фильтры по путям и расширениям",
		},
		Repo:      "cyclotruc/gitingest",
		Branch:    "main",
		RootDir:   ".",
		Framework: "dockerfile",
		Port:      8000,
		Profile:   "small",
		FirstRun:  "Вставьте ссылку на публичный репозиторий и получите текст для модели.",
		Warning: "Приложение ходит в интернет за содержимым репозиториев, которые вы ему называете. " +
			"Приватные репозитории оно без ваших ключей не увидит — и не должно.",
	},
	{
		Slug:     "devdocs",
		Name:     "DevDocs",
		Tagline:  "Документация 500+ библиотек в одном интерфейсе, с поиском",
		Category: CategoryDocuments,
		Homepage: "https://devdocs.io",
		License:  "MPL-2.0",
		Aliases:  []string{"docs", "документация", "справочник", "api docs", "manual", "мануал"},
		About: "Собирает документацию сотен языков и библиотек в один быстрый интерфейс с общим " +
			"поиском и горячими клавишами. Свой экземпляр удобен тем, что набор документаций " +
			"и их версии выбираете вы.",
		Bullets: []string{
			"Документация 500+ языков и библиотек",
			"Мгновенный поиск по всему набору",
			"Свой экземпляр — свой выбор версий",
		},
		Repo:      "freeCodeCamp/devdocs",
		Branch:    "main",
		RootDir:   ".",
		Framework: "dockerfile",
		Port:      9292,
		Profile:   "medium",
		FirstRun:  "Откройте приложение и включите нужные документации в настройках.",
		BuildNote: "Образ большой: сборка идёт дольше остальных карточек каталога.",
	},

	{
		Slug:     "n8n",
		Name:     "n8n",
		Tagline:  "Автоматизация без кода: связывает сервисы между собой",
		Category: CategoryAutomation,
		Homepage: "https://n8n.io",
		License:  "Sustainable Use License",
		Aliases:  []string{"н8н", "автоматизация", "workflow", "zapier", "make", "интеграции", "боты"},
		About: "Визуальный конструктор сценариев: получил письмо — положил строку в таблицу — " +
			"отправил сообщение в чат. Больше 400 готовых интеграций, а если нужного узла нет — " +
			"внутри сценария можно написать код.",
		Bullets: []string{
			"Сценарии собираются мышью, из готовых узлов",
			"400+ интеграций и HTTP-узел для всего остального",
			"История запусков и повтор упавшего шага",
		},
		Image:    "n8nio/n8n:2.34.4",
		IconRepo: "n8n-io/n8n",
		Port:     5678,
		Profile:  "medium",
		Volume:   &Volume{Path: "/home/node/.n8n", Size: "5Gi", FSGroup: 1000},
		FirstRun: "При первом открытии n8n попросит завести владельца — этот аккаунт и будет входом.",
		Warning: "Сценарии хранят ключи от ваших сервисов. Не оставляйте экземпляр без владельца: " +
			"кто первым откроет адрес, тот его и заведёт.",
	},
	{
		Slug:     "gitea",
		Name:     "Gitea",
		Tagline:  "Свой GitHub: репозитории, пул-реквесты, релизы",
		Category: CategoryDevTools,
		Homepage: "https://about.gitea.com",
		License:  "MIT",
		Aliases:  []string{"гитея", "git", "гит", "репозиторий", "github", "gitlab", "форж"},
		About: "Лёгкий git-сервер с веб-интерфейсом: репозитории, ветки, пул-реквесты, issue, " +
			"релизы и вики. Ест мало, поднимается за минуту и не требует ничего, кроме диска.",
		Bullets: []string{
			"Репозитории, пул-реквесты, issue и вики",
			"Работает по HTTPS и по SSH",
			"Хранит всё на своём диске — переносится копированием тома",
		},
		Image:    "gitea/gitea:1.27",
		IconRepo: "go-gitea/gitea",
		Port:     3000,
		Profile:  "small",
		Volume:   &Volume{Path: "/data", Size: "20Gi", FSGroup: 1000},
		FirstRun: "Первый экран — установка: выберите SQLite, дальше заведите администратора.",
	},
	{
		Slug:     "grafana",
		Name:     "Grafana",
		Tagline:  "Дашборды по любым метрикам",
		Category: CategoryMonitoring,
		Homepage: "https://grafana.com",
		License:  "AGPL-3.0",
		Aliases:  []string{"графана", "дашборды", "метрики", "графики", "мониторинг"},
		About: "Рисует графики по данным из Prometheus, ClickHouse, PostgreSQL и десятков других " +
			"источников. Свой экземпляр удобен, когда дашборды нужно показывать людям без " +
			"доступа к внутренней сети.",
		Bullets: []string{
			"Десятки источников данных из коробки",
			"Готовые дашборды из каталога Grafana",
			"Алерты по порогам",
		},
		Image:    "grafana/grafana:13.0",
		IconRepo: "grafana",
		Port:     3000,
		Profile:  "small",
		Volume:   &Volume{Path: "/var/lib/grafana", Size: "5Gi", FSGroup: 472},
		FirstRun: "Вход admin / admin, пароль Grafana попросит сменить сразу.",
	},
	{
		Slug:     "metabase",
		Name:     "Metabase",
		Tagline:  "Вопросы к базе данных без SQL",
		Category: CategoryData,
		Homepage: "https://www.metabase.com",
		License:  "AGPL-3.0",
		Aliases:  []string{"метабейс", "bi", "аналитика", "отчёты", "sql", "дашборд"},
		About: "Подключается к вашей базе и позволяет собирать отчёты мышью: выбрать таблицу, " +
			"сгруппировать, построить график. Кто умеет SQL — пишет SQL, остальные жмут кнопки.",
		Bullets: []string{
			"Отчёты и дашборды без SQL",
			"PostgreSQL, MySQL, ClickHouse и другие источники",
			"Отправка отчётов по расписанию",
		},
		Image:    "metabase/metabase:v0.63.5.2",
		IconRepo: "metabase",
		Port:     3000,
		Profile:  "medium",
		Volume:   &Volume{Path: "/metabase-data", Size: "5Gi", FSGroup: 2000},
		Env:      map[string]string{"MB_DB_FILE": "/metabase-data/metabase.db"},
		FirstRun: "Заведите администратора и подключите базу — Metabase сам разберёт её схему.",
	},
	{
		Slug:     "nocodb",
		Name:     "NocoDB",
		Tagline:  "Таблица как в Airtable поверх обычной базы",
		Category: CategoryData,
		Homepage: "https://nocodb.com",
		License:  "AGPL-3.0",
		Aliases:  []string{"нокодб", "airtable", "таблицы", "таблица", "no-code", "нокод"},
		About: "Превращает базу данных в удобную таблицу: строки, колонки нужных типов, формы, " +
			"календарь и канбан поверх тех же данных. Можно вести учёт прямо в нём, а можно " +
			"надеть интерфейс на существующую базу.",
		Bullets: []string{
			"Таблицы, формы, канбан и календарь",
			"Работает поверх своей базы или подключается к вашей",
			"Есть REST API к каждой таблице",
		},
		Image:    "nocodb/nocodb:2026.08.0",
		IconRepo: "nocodb",
		Port:     8080,
		Profile:  "medium",
		Volume:   &Volume{Path: "/usr/app/data", Size: "10Gi"},
		FirstRun: "Заведите аккаунт на первом экране — он станет владельцем.",
	},
	{
		Slug:     "qdrant",
		Name:     "Qdrant",
		Tagline:  "Векторная база для поиска по смыслу",
		Category: CategoryAI,
		Homepage: "https://qdrant.tech",
		License:  "Apache-2.0",
		Aliases:  []string{"квадрант", "кудрант", "векторная база", "embeddings", "эмбеддинги", "rag", "поиск"},
		About: "Хранит векторы и ищет по ним ближайших соседей — то, на чём держится поиск по " +
			"смыслу и RAG. Есть фильтры по метаданным, снапшоты и веб-интерфейс с консолью запросов.",
		Bullets: []string{
			"Поиск ближайших векторов с фильтрами",
			"REST и gRPC API, клиенты для Python и JS",
			"Веб-консоль на /dashboard",
		},
		Image:    "qdrant/qdrant:v1.19.0",
		IconRepo: "qdrant",
		Port:     6333,
		Profile:  "medium",
		Volume:   &Volume{Path: "/qdrant/storage", Size: "10Gi"},
		FirstRun: "Консоль — на /dashboard. API открыт без ключа: задайте QDRANT__SERVICE__API_KEY в переменных окружения.",
		Warning: "По умолчанию база отвечает всем, кто знает адрес. Если приложение смотрит наружу — " +
			"сразу задайте QDRANT__SERVICE__API_KEY.",
	},
	{
		Slug:     "uptime-kuma",
		Name:     "Uptime Kuma",
		Tagline:  "Следит, что ваши сайты живы, и пишет, когда нет",
		Category: CategoryMonitoring,
		Homepage: "https://uptime.kuma.pet",
		License:  "MIT",
		Aliases:  []string{"аптайм", "кума", "мониторинг", "пинг", "доступность", "статус"},
		About: "Проверяет сайты, порты и сертификаты по расписанию и присылает уведомление в " +
			"Telegram, почту или вебхук, когда что-то упало. Заодно рисует публичную страницу " +
			"статуса.",
		Bullets: []string{
			"HTTP, TCP, ping, DNS и срок сертификата",
			"90+ каналов уведомлений, включая Telegram",
			"Публичная страница статуса",
		},
		Image:    "louislam/uptime-kuma:2.5.0",
		IconRepo: "louislam/uptime-kuma",
		Port:     3001,
		Profile:  "small",
		Volume:   &Volume{Path: "/app/data", Size: "5Gi"},
		FirstRun: "Заведите администратора и добавьте первую проверку.",
	},
	{
		Slug:     "vaultwarden",
		Name:     "Vaultwarden",
		Tagline:  "Свой менеджер паролей, совместимый с Bitwarden",
		Category: CategorySecurity,
		Homepage: "https://github.com/dani-garcia/vaultwarden",
		License:  "AGPL-3.0",
		Aliases:  []string{"волтварден", "битварден", "bitwarden", "пароли", "менеджер паролей", "хранилище"},
		About: "Сервер, к которому подключаются обычные приложения и расширения Bitwarden. " +
			"Пароли лежат на вашем диске, а не в чужом облаке; шифрование — то же самое, " +
			"клиентское.",
		Bullets: []string{
			"Работает со всеми клиентами Bitwarden",
			"Организации, коллекции и общий доступ",
			"Данные шифруются на устройстве, сервер видит только шифротекст",
		},
		Image:    "vaultwarden/server:1.37.1-alpine",
		IconRepo: "dani-garcia/vaultwarden",
		Port:     80,
		Profile:  "small",
		Volume:   &Volume{Path: "/data", Size: "5Gi"},
		FirstRun: "Зарегистрируйтесь первым, затем задайте SIGNUPS_ALLOWED=false в переменных окружения.",
		Warning: "Регистрация открыта, пока вы её не закроете: заведите свою учётную запись сразу " +
			"после установки и выключите приём новых.",
	},
	{
		Slug:     "code-server",
		Name:     "code-server",
		Tagline:  "VS Code в браузере, на своём сервере",
		Category: CategoryDevTools,
		Homepage: "https://coder.com",
		License:  "MIT",
		Aliases:  []string{"вскод", "vscode", "vs code", "редактор", "ide", "браузерная ide"},
		About: "Настоящий VS Code, который открывается вкладкой браузера и держит проект на " +
			"сервере. Полезно, когда работать надо с планшета, с чужой машины или просто ближе " +
			"к данным.",
		Bullets: []string{
			"Полноценный VS Code с расширениями и терминалом",
			"Файлы живут на диске приложения, а не в браузере",
			"Открывается с любого устройства",
		},
		Image:    "ghcr.io/coder/code-server:4.130.0",
		IconRepo: "coder",
		Port:     8080,
		Profile:  "medium",
		Volume:   &Volume{Path: "/home/coder", Size: "20Gi", FSGroup: 1000},
		Env:      map[string]string{"HOST": "0.0.0.0"},
		Params: []Param{
			{
				Key: "password", EnvKey: "PASSWORD", Label: "Пароль на вход",
				Help: "Без него редактор откроется у любого, кто знает адрес.",
				Kind: ParamSecret, Required: true, Placeholder: "минимум 12 символов",
			},
		},
		FirstRun: "Откройте адрес приложения и введите пароль, который задали при установке.",
		Warning: "В редакторе есть терминал: это полный доступ к контейнеру. Пароль должен быть " +
			"настоящим, а не «12345».",
	},
	{
		Slug:     "open-webui",
		Name:     "Open WebUI",
		Tagline:  "Свой чат с моделями — как ChatGPT, но ваш",
		Category: CategoryAI,
		Homepage: "https://openwebui.com",
		License:  "BSD-3-Clause",
		Aliases:  []string{"опенвебуи", "чат", "chatgpt", "llm", "нейросеть", "ollama", "ии-чат"},
		About: "Интерфейс чата поверх любого OpenAI-совместимого API: своя история, свои промпты, " +
			"свои пользователи. Подключается и к нашему AI-шлюзу, и к локальной Ollama.",
		Bullets: []string{
			"История, папки и общие промпты",
			"Несколько моделей и несколько пользователей",
			"Работает с любым OpenAI-совместимым API",
		},
		Image:    "ghcr.io/open-webui/open-webui:v0.6.42",
		IconRepo: "open-webui",
		Port:     8080,
		Profile:  "medium",
		Volume:   &Volume{Path: "/app/backend/data", Size: "10Gi"},
		Params: []Param{
			{
				Key: "api_base", EnvKey: "OPENAI_API_BASE_URL", Label: "Адрес API модели",
				Help: "OpenAI-совместимый endpoint. Можно оставить пустым и настроить потом в интерфейсе.",
				Kind: ParamText, Placeholder: "https://api.openai.com/v1",
			},
			{
				Key: "api_key", EnvKey: "OPENAI_API_KEY", Label: "Ключ API",
				Kind: ParamSecret,
			},
		},
		FirstRun: "Первый зарегистрировавшийся становится администратором — сделайте это сразу.",
	},
	{
		Slug:     "flowise",
		Name:     "Flowise",
		Tagline:  "Сценарии для языковых моделей мышью",
		Category: CategoryAI,
		Homepage: "https://flowiseai.com",
		License:  "Apache-2.0",
		Aliases:  []string{"фловайз", "langchain", "агенты", "rag", "чатбот", "llm"},
		About: "Визуальный сборщик цепочек и агентов: загрузка документов, векторный поиск, вызов " +
			"модели, ответ — всё узлами на холсте. Готовый сценарий отдаётся как API или как " +
			"виджет чата.",
		Bullets: []string{
			"Цепочки и агенты собираются на холсте",
			"RAG по своим документам",
			"Каждый сценарий доступен по API",
		},
		Image:    "flowiseai/flowise:3.1.4",
		IconRepo: "FlowiseAI",
		Port:     3000,
		Profile:  "medium",
		Volume:   &Volume{Path: "/root/.flowise", Size: "10Gi"},
		Env:      map[string]string{"DATABASE_PATH": "/root/.flowise", "APIKEY_PATH": "/root/.flowise", "SECRETKEY_PATH": "/root/.flowise", "LOG_PATH": "/root/.flowise/logs"},
		Params: []Param{
			{
				Key: "username", EnvKey: "FLOWISE_USERNAME", Label: "Логин",
				Kind: ParamText, Required: true, Default: "admin",
			},
			{
				Key: "password", EnvKey: "FLOWISE_PASSWORD", Label: "Пароль",
				Kind: ParamSecret, Required: true, Placeholder: "минимум 12 символов",
			},
		},
		FirstRun: "Войдите под логином и паролем, которые задали при установке.",
	},
	{
		Slug:     "memos",
		Name:     "Memos",
		Tagline:  "Быстрые заметки, свой мини-твиттер для мыслей",
		Category: CategoryDocuments,
		Homepage: "https://usememos.com",
		License:  "MIT",
		Aliases:  []string{"мемос", "заметки", "notes", "дневник", "markdown", "todo"},
		About: "Лента коротких заметок в markdown: написал, поставил тег, нашёл поиском. Открывается " +
			"мгновенно, есть мобильный вид и API, а данные лежат в одном файле базы на вашем диске.",
		Bullets: []string{
			"Заметки в markdown, теги и поиск",
			"Публичные и приватные записи",
			"Лёгкий: работает на самом маленьком профиле",
		},
		Image:    "neosmemo/memos:0.30",
		IconRepo: "usememos",
		Port:     5230,
		Profile:  "small",
		Volume:   &Volume{Path: "/var/opt/memos", Size: "5Gi"},
		FirstRun: "Первый зарегистрированный пользователь становится владельцем.",
	},
	{
		Slug:     "vikunja",
		Name:     "Vikunja",
		Tagline:  "Задачи и проекты: список, канбан, календарь",
		Category: CategoryProductivity,
		Homepage: "https://vikunja.io",
		License:  "AGPL-3.0",
		Aliases:  []string{"викунья", "задачи", "todo", "канбан", "kanban", "trello", "проекты"},
		About: "Трекер задач для себя и небольшой команды: те же задачи показываются списком, " +
			"доской, календарём или диаграммой Ганта. Есть напоминания, повторяющиеся задачи и " +
			"подписка календаря по CalDAV.",
		Bullets: []string{
			"Список, канбан, календарь и Гантт по одним данным",
			"Напоминания и повторяющиеся задачи",
			"CalDAV: задачи видны в календаре телефона",
		},
		Image:    "vikunja/vikunja:2.5",
		IconRepo: "go-vikunja",
		Port:     3456,
		Profile:  "small",
		Volume:   &Volume{Path: "/app/vikunja/files", Size: "10Gi", FSGroup: 1000},
		Env:      map[string]string{"VIKUNJA_DATABASE_PATH": "/app/vikunja/files/vikunja.db"},
		FirstRun: "Зарегистрируйтесь и создайте первый проект.",
	},
	{
		Slug:     "freshrss",
		Name:     "FreshRSS",
		Tagline:  "Свой RSS-читатель вместо ленты соцсети",
		Category: CategoryProductivity,
		Homepage: "https://freshrss.org",
		License:  "AGPL-3.0",
		Aliases:  []string{"фрешрсс", "rss", "рсс", "читалка", "feedly", "новости", "лента"},
		About: "Собирает RSS и Atom-ленты в одну читалку: папки, метки, полнотекстовый поиск, " +
			"фильтры. Есть API, к которому подключаются мобильные читалки.",
		Bullets: []string{
			"Ленты, папки, метки и поиск",
			"Работает с мобильными приложениями через API",
			"Правила: помечать, скрывать, откладывать",
		},
		Image:    "freshrss/freshrss:1.29.1-alpine",
		IconRepo: "FreshRSS",
		Port:     80,
		Profile:  "small",
		Volume:   &Volume{Path: "/var/www/FreshRSS/data", Size: "5Gi"},
		FirstRun: "Мастер установки попросит выбрать SQLite и завести администратора.",
	},
	{
		Slug:     "stirling-pdf",
		Name:     "Stirling PDF",
		Tagline:  "Всё, что делают с PDF, в одном месте",
		Category: CategoryDocuments,
		Homepage: "https://stirlingpdf.com",
		License:  "MIT",
		Aliases:  []string{"стирлинг", "pdf", "пдф", "сжать pdf", "объединить pdf", "ocr", "сканы"},
		About: "Объединяет, режет, поворачивает и сжимает PDF, конвертирует в картинки и обратно, " +
			"распознаёт текст, снимает и ставит пароль. Файлы обрабатываются на вашем сервере и " +
			"никуда не уходят.",
		Bullets: []string{
			"50+ операций над PDF",
			"OCR и конвертация в другие форматы",
			"Файлы не покидают ваш сервер",
		},
		Image:    "ghcr.io/stirling-tools/stirling-pdf:1.0.2",
		IconRepo: "Stirling-Tools",
		Port:     8080,
		Profile:  "medium",
		FirstRun: "Открывайте и перетаскивайте файл — инструменты слева.",
	},
	{
		Slug:     "changedetection",
		Name:     "changedetection.io",
		Tagline:  "Следит за страницей и пишет, когда она изменилась",
		Category: CategoryAutomation,
		Homepage: "https://changedetection.io",
		License:  "Apache-2.0",
		Aliases:  []string{"чейнджтекшн", "слежение", "мониторинг сайта", "цена", "изменения", "парсер"},
		About: "Заходит на указанную страницу по расписанию и сообщает, если текст поменялся: цена " +
			"упала, появилась вакансия, вышло объявление. Умеет следить за куском страницы по " +
			"CSS-селектору и слать уведомления куда угодно.",
		Bullets: []string{
			"Слежение за куском страницы, а не за всей",
			"Уведомления в Telegram, почту, вебхук",
			"История изменений с подсветкой",
		},
		Image:    "ghcr.io/dgtlmoon/changedetection.io:0.55.4",
		IconRepo: "dgtlmoon",
		Port:     5000,
		Profile:  "small",
		Volume:   &Volume{Path: "/datastore", Size: "5Gi"},
		FirstRun: "Добавьте первый адрес и задайте пароль в Settings → General.",
		Warning:  "Пока пароль не задан, интерфейс открыт всем, кто знает адрес.",
	},
	{
		Slug:     "syncthing",
		Name:     "Syncthing",
		Tagline:  "Синхронизация папок между своими устройствами",
		Category: CategoryData,
		Homepage: "https://syncthing.net",
		License:  "MPL-2.0",
		Aliases:  []string{"синктинг", "синхронизация", "dropbox", "файлы", "бэкап", "облако"},
		About: "Держит папки одинаковыми на ноутбуке, телефоне и сервере напрямую, без чужого " +
			"облака. Экземпляр на сервере работает как всегда включённый узел, через который " +
			"устройства догоняют друг друга.",
		Bullets: []string{
			"Папки синхронизируются напрямую между устройствами",
			"Версии файлов и защита от удаления",
			"Сервер как всегда доступный узел",
		},
		Image:    "syncthing/syncthing:2.1",
		IconRepo: "syncthing",
		Port:     8384,
		Profile:  "small",
		Volume:   &Volume{Path: "/var/syncthing", Size: "50Gi", FSGroup: 1000},
		Env:      map[string]string{"STGUIADDRESS": "0.0.0.0:8384"},
		FirstRun: "В интерфейсе сразу задайте логин и пароль (Actions → Settings → GUI).",
		Warning: "Веб-интерфейс управляет вашими файлами и открыт без пароля до первой настройки — " +
			"задайте его сразу.",
	},
	{
		Slug:     "pgadmin",
		Name:     "pgAdmin",
		Tagline:  "Веб-клиент к PostgreSQL",
		Category: CategoryData,
		Homepage: "https://www.pgadmin.org",
		License:  "PostgreSQL License",
		Aliases:  []string{"пгадмин", "постгрес", "sql-клиент", "dbeaver", "субд-клиент"},
		About: "Привычный интерфейс к PostgreSQL прямо в браузере: обзор схем, редактор запросов, " +
			"планы выполнения, импорт и экспорт. Удобно, когда база в облаке, а ставить клиент " +
			"на каждую машину не хочется.",
		Bullets: []string{
			"Редактор запросов и просмотр планов",
			"Обзор таблиц, индексов и прав",
			"Импорт и экспорт данных",
		},
		Image:    "dpage/pgadmin4:9.17",
		IconRepo: "pgadmin-org",
		Port:     80,
		Profile:  "small",
		Volume:   &Volume{Path: "/var/lib/pgadmin", Size: "5Gi", FSGroup: 5050},
		Env:      map[string]string{"PGADMIN_LISTEN_PORT": "80"},
		Params: []Param{
			{
				Key: "email", EnvKey: "PGADMIN_DEFAULT_EMAIL", Label: "Почта для входа",
				Kind: ParamText, Required: true, Placeholder: "you@example.com",
			},
			{
				Key: "password", EnvKey: "PGADMIN_DEFAULT_PASSWORD", Label: "Пароль",
				Kind: ParamSecret, Required: true, Placeholder: "минимум 12 символов",
			},
		},
		FirstRun: "Войдите указанной почтой и паролем, затем добавьте подключение к своей базе.",
	},
	{
		Slug:     "homepage",
		Name:     "Homepage",
		Tagline:  "Стартовая страница со ссылками и статусами сервисов",
		Category: CategoryProductivity,
		Homepage: "https://gethomepage.dev",
		License:  "GPL-3.0",
		Aliases:  []string{"хоумпейдж", "дашборд", "стартовая", "закладки", "ссылки", "портал"},
		About: "Одна страница, с которой начинается день: плитки сервисов, погода, поиск, виджеты " +
			"с их состоянием. Настраивается YAML-файлами в томе приложения.",
		Bullets: []string{
			"Плитки сервисов с живыми виджетами",
			"Поиск и закладки",
			"Конфигурация обычными YAML-файлами",
		},
		Image:    "ghcr.io/gethomepage/homepage:v1.13.2",
		IconRepo: "gethomepage",
		Port:     3000,
		Profile:  "small",
		Volume:   &Volume{Path: "/app/config", Size: "1Gi", FSGroup: 1000},
		Env:      map[string]string{"HOMEPAGE_ALLOWED_HOSTS": "*"},
		FirstRun: "Правьте файлы в /app/config через файловый менеджер приложения — страница перечитает их сама.",
	},
	{
		Slug:     "jellyfin",
		Name:     "Jellyfin",
		Tagline:  "Свой Netflix: медиатека фильмов и музыки",
		Category: CategoryMedia,
		Homepage: "https://jellyfin.org",
		License:  "GPL-2.0",
		Aliases:  []string{"джелифин", "медиасервер", "фильмы", "plex", "кино", "музыка", "сериалы"},
		About: "Медиасервер: раскладывает ваши фильмы, сериалы и музыку по обложкам и описаниям и " +
			"отдаёт их в браузер, на телевизор и на телефон. Ничего не покупает и никуда не " +
			"отправляет статистику.",
		Bullets: []string{
			"Обложки, описания и продолжение с того же места",
			"Клиенты для телевизора, телефона и браузера",
			"Несколько пользователей с родительским контролем",
		},
		Image:    "jellyfin/jellyfin:10.11",
		IconRepo: "jellyfin",
		Port:     8096,
		Profile:  "large",
		Volume:   &Volume{Path: "/config", Size: "100Gi"},
		Env:      map[string]string{"JELLYFIN_CACHE_DIR": "/config/cache"},
		FirstRun: "Мастер установки заведёт администратора; медиатеку укажите на папку внутри /config.",
		Warning: "Перекодирование видео на лету упирается в процессор: без видеокарты рассчитывайте " +
			"на прямое воспроизведение.",
	},
	{
		Slug:     "filebrowser",
		Name:     "File Browser",
		Tagline:  "Файловый менеджер в браузере",
		Category: CategoryData,
		Homepage: "https://filebrowser.org",
		License:  "Apache-2.0",
		Aliases:  []string{"файлбраузер", "файлы", "файловый менеджер", "ftp", "обмен файлами", "хранилище"},
		About: "Веб-интерфейс к папке на диске: загрузка и скачивание, просмотр картинок и видео, " +
			"редактор текстовых файлов, ссылки для обмена. Годится и как личное хранилище, и как " +
			"способ отдать файл человеку без мессенджера.",
		Bullets: []string{
			"Загрузка, скачивание и предпросмотр",
			"Ссылки для обмена, в том числе с паролем",
			"Несколько пользователей со своими правами",
		},
		Image:    "filebrowser/filebrowser:v2.63.23",
		IconRepo: "filebrowser",
		Port:     80,
		Profile:  "small",
		Volume:   &Volume{Path: "/srv", Size: "50Gi"},
		Env:      map[string]string{"FB_DATABASE": "/srv/.filebrowser.db", "FB_ROOT": "/srv", "FB_PORT": "80", "FB_ADDRESS": "0.0.0.0"},
		FirstRun: "Вход admin / admin — смените пароль в настройках сразу после входа.",
		Warning:  "Пароль по умолчанию admin / admin. Меняйте его до того, как дадите кому-то адрес.",
	},
	{
		Slug:     "searxng",
		Name:     "SearXNG",
		Tagline:  "Поисковик без слежки: спрашивает других за вас",
		Category: CategoryProductivity,
		Homepage: "https://docs.searxng.org",
		License:  "AGPL-3.0",
		Aliases:  []string{"сёрксng", "searx", "поиск", "приватный поиск", "метапоисковик", "google"},
		About: "Метапоисковик: отправляет ваш запрос в десятки поисковых систем и показывает " +
			"сводный результат, не сохраняя историю и не отдавая им ваш профиль.",
		Bullets: []string{
			"Один запрос — результаты из десятков поисковиков",
			"Не хранит историю и не ставит трекеры",
			"Работает как поисковая строка браузера",
		},
		Image:    "searxng/searxng:2026.8.5-1689cb1b5",
		IconRepo: "searxng",
		Port:     8080,
		Profile:  "small",
		Volume:   &Volume{Path: "/etc/searxng", Size: "1Gi"},
		FirstRun: "Готово сразу: добавьте адрес приложения как поисковую систему в браузере.",
	},
}

// IsImage reports whether this entry runs a published image instead of being
// built from its repository.
func (s Solution) IsImage() bool { return s.Image != "" }

// Source is what the card tells the customer about where the running thing came
// from: "image" for a published image, "repo" for our own build of the source.
func (s Solution) Source() string {
	if s.IsImage() {
		return "image"
	}
	return "repo"
}

// Icon is the logo URL for this entry — IconRepo when the entry has one,
// otherwise the repository it is built from.
func (s Solution) Icon() string {
	src := s.IconRepo
	if src == "" {
		src = s.Repo
	}
	owner, _, _ := strings.Cut(src, "/")
	return avatarForOwner(owner)
}

// Lookup returns the Solution by slug. Second return is false if slug is unknown.
func Lookup(slug string) (Solution, bool) {
	for _, s := range V1 {
		if s.Slug == slug {
			return s, true
		}
	}
	return Solution{}, false
}

// Slugs returns the catalog slugs in display order.
func Slugs() []string {
	out := make([]string, len(V1))
	for i, s := range V1 {
		out[i] = s.Slug
	}
	return out
}

// IsCatalogRepo reports whether repoFullName is one of the catalog's own
// repositories. Case-insensitive, because GitHub owner/repo names are.
//
// This is what makes a catalog deploy a DEMO for the reaper (api/demo_apps.go):
// a project the platform offered, that nobody claimed, is exactly the app that
// used to idle for eighteen days. A repository the customer pasted themselves is
// never a demo — they chose it, and deleting their work on a timer would be a
// different product than the one we are building. Matching on the full name
// means a fork ("acme/it-tools") is the customer's, not ours.
func IsCatalogRepo(repoFullName string) bool {
	if repoFullName == "" {
		return false
	}
	for _, s := range V1 {
		if s.Repo == "" {
			continue
		}
		if strings.EqualFold(s.Repo, repoFullName) {
			return true
		}
	}
	return false
}

// instanceNameRe is the accepted app name: lowercase, alphanumeric and dashes,
// starting with a letter — a legal Kubernetes resource name.
var instanceNameRe = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// ValidateInstanceName checks the name the deployed app will carry.
func ValidateInstanceName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 40 {
		return fmt.Errorf("name must be at most 40 characters")
	}
	if !instanceNameRe.MatchString(name) {
		return fmt.Errorf("name must be lowercase alphanumeric or '-', and start with a letter")
	}
	return nil
}

// repoFullNameRe is a GitHub "owner/name" pair. GitHub allows alphanumerics,
// dash, underscore and dot in both halves.
var repoFullNameRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9_])?/[A-Za-z0-9._-]+$`)

// ParseRepoURL turns what a person actually pastes into an "owner/name" pair.
//
// Accepts the browser URL, the clone URL, the SSH remote, and a bare
// "owner/name" — because all four are what ends up on a clipboard, and a form
// that takes only one of them is a form that rejects the customer's first
// attempt. Anything else is refused rather than guessed at: a wrong guess here
// deploys somebody else's repository under the customer's name.
func ParseRepoURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("paste a link to a public GitHub repository")
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimPrefix(s, "git+")

	switch {
	case strings.HasPrefix(s, "git@github.com:"):
		s = strings.TrimPrefix(s, "git@github.com:")
	case strings.HasPrefix(s, "ssh://git@github.com/"):
		s = strings.TrimPrefix(s, "ssh://git@github.com/")
	case strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "http://"):
		rest := s[strings.Index(s, "//")+2:]
		host, path, found := strings.Cut(rest, "/")
		if !found {
			return "", fmt.Errorf("that link has no repository in it")
		}
		if !strings.EqualFold(host, "github.com") && !strings.EqualFold(host, "www.github.com") {
			return "", fmt.Errorf("only public GitHub repositories are supported here")
		}
		s = path
	}
	s = strings.TrimSuffix(s, ".git")

	// A deep link (…/tree/main/apps/web) carries a branch and a subdirectory the
	// caller has to choose deliberately, so keep only the repository and let
	// them set branch and root directory in the form.
	parts := strings.Split(s, "/")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	full := strings.Join(parts, "/")
	if !repoFullNameRe.MatchString(full) {
		return "", fmt.Errorf("expected a link like https://github.com/owner/repository")
	}
	return full, nil
}

// ResolveParams validates supplied values against the entry's parameters and
// returns the environment variables they produce. Unknown keys are rejected
// rather than ignored: a typo in a parameter name is a misconfigured deploy
// that would otherwise surface much later as a product behaving oddly.
func (s Solution) ResolveParams(in map[string]string) (map[string]string, error) {
	known := make(map[string]Param, len(s.Params))
	for _, p := range s.Params {
		known[p.Key] = p
	}
	unknown := make([]string, 0)
	for k := range in {
		if _, ok := known[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown parameter(s): %s", strings.Join(unknown, ", "))
	}

	env := make(map[string]string, len(s.Params))
	for _, p := range s.Params {
		v := strings.TrimSpace(in[p.Key])
		if v == "" {
			v = p.Default
		}
		if v == "" {
			if p.Required {
				return nil, fmt.Errorf("%s is required", p.Key)
			}
			continue
		}
		if p.Kind == ParamSelect && !contains(p.Options, v) {
			return nil, fmt.Errorf("%s must be one of: %s", p.Key, strings.Join(p.Options, ", "))
		}
		if strings.ContainsAny(v, "\n\r") {
			return nil, fmt.Errorf("%s must not contain line breaks", p.Key)
		}
		env[p.EnvKey] = v
	}
	return env, nil
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
