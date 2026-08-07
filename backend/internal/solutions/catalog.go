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
// Every entry is a REPOSITORY, not a published image. The install runs the
// ordinary customer path — link the public repo, detect the framework, build it
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
// # Why every v1 entry is stateless
//
// An app created by the build pipeline takes its spec from git_repos
// (port/replicas/profile) and there is no volume in that spec, so a project
// that keeps state on disk would silently lose it on every redeploy. Until the
// build path can carry a volume, the catalog only lists projects whose state
// lives in the browser or nowhere at all. This is a real constraint, not an
// aesthetic: shipping a note-taking app that eats notes is worse than not
// shipping it.
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
	CategoryDevTools  Category = "dev-tools"
	CategoryDocuments Category = "documents"
	CategoryAI        Category = "ai"
)

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

// V1 is the v1 catalog. Frozen at deploy time.
//
// Four entries, each verified to carry a Dockerfile at its repository root and
// to listen on the port recorded here. Small on purpose: a catalog of four
// projects that build is worth more than twenty assembled from READMEs, and the
// next entries earn their place by being deployed, not by being typed.
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
	for _, s := range V1 {
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
