// Package solutions holds the ready-made solution catalog: whole third-party
// products a customer installs onto a VM app server in one click, instead of
// bringing a repository of their own.
//
// The catalog is a frozen Go variable — same idiom, and same reason, as
// internal/boxcatalog and internal/profiles: edit this file and redeploy. A row
// an operator could INSERT at 3am would promise a product the platform cannot
// actually install, because a solution is only real once its image exists in a
// registry we can pull from AND the compose shape it renders has been run on a
// VM at least once. A deploy is exactly the event that also publishes and pins
// that image, so the catalog and the registry move together by construction.
//
// What a solution is NOT: it is not a starter repository (see
// api/demo_apps.go). A starter demo proves a deploy happened and is reaped
// after a few hours; a solution is a product the customer keeps and uses, with
// its own data volume, credentials and upgrade path. The two answer the same
// onboarding question — "I have no code, show me something" — with opposite
// levels of ambition.
//
// Every solution renders to ordinary first-class VM Applications: one App per
// compose service, deployed into the environment's single aggregate stack
// (renderer.EnvComposeGitPath). Nothing about a solution is a special runtime,
// so per-app logs, metrics, env editing, restart and delete all work the day it
// is installed, and an operator who never opens this catalog cannot tell a
// solution app from a hand-made one.
package solutions

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Category groups solutions in the console's catalog UI.
type Category string

const (
	CategoryAIAgent    Category = "ai-agent"
	CategoryAutomation Category = "automation"
	CategoryDatabase   Category = "database"
	CategoryTools      Category = "tools"
)

// ParamKind is how the console renders one install parameter, and how the API
// validates it.
type ParamKind string

const (
	// ParamText is a plain single-line value (a URL, a model name).
	ParamText ParamKind = "text"
	// ParamSecret is a value the customer supplies that must never be echoed
	// back: an upstream API key. Stored encrypted like any other secret env var.
	ParamSecret ParamKind = "secret"
	// ParamSelect is a closed set of allowed values (Options).
	ParamSelect ParamKind = "select"
)

// Param is one value the customer supplies at install time.
//
// EnvKey is the container environment variable the value lands in. That is the
// whole contract with the upstream product: the catalog never patches config
// files inside a solution's data volume, because that volume belongs to the
// customer the moment the first container writes to it. Anything the platform
// cannot express as an env var either gets a documented first-boot bootstrap
// command (see Service.Command) or does not get automated at all.
type Param struct {
	Key         string    // stable identifier in the install request
	EnvKey      string    // container env var the value is written to
	Label       string    // console label (Russian: the console's product language)
	Help        string    // one-line hint under the field
	Kind        ParamKind //
	Required    bool      //
	Default     string    // pre-filled for text/select; never for secrets
	Options     []string  // ParamSelect only
	Placeholder string    //
}

// Generated is a credential the PLATFORM mints at install time rather than
// asking the customer for: a dashboard password, a session-signing secret.
//
// This is what makes a one-click install of an agent that runs shell commands
// defensible. The upstream product ships with its dashboard bound to loopback
// precisely because it holds API keys and has no password by default; publishing
// that port on a VM with a public IP would be handing out a root shell. So the
// platform generates real credentials, stores them encrypted, and injects them —
// the customer gets a dashboard that is reachable AND authenticated, and never
// has to know that the alternative was an open one.
type Generated struct {
	Key    string // stable identifier, referenced by RevealKeys
	EnvKey string // container env var the generated value is written to
	Kind   GeneratedKind
	Label  string // shown next to the value on the "installed" screen
}

// GeneratedKind selects the generator. Deliberately a tiny closed set: every
// kind here is a credential with a known consumer, not a general-purpose random
// string service.
type GeneratedKind string

const (
	// GeneratedPassword is a human-typeable password (hex, 16 bytes). The
	// customer reads it off the console and types it into a login form.
	GeneratedPassword GeneratedKind = "password"
	// GeneratedSecret is a machine-only signing key (hex, 32 bytes). Nobody ever
	// types it, so it is twice as long.
	GeneratedSecret GeneratedKind = "secret"
)

// Service is one compose service of a solution — and therefore one first-class
// Application after install.
//
// NameSuffix is appended to the instance name the customer chose, so an install
// named "hermes" produces apps "hermes" (suffix "") and "hermes-dashboard"
// (suffix "dashboard"). Exactly one service must carry the empty suffix: it is
// the one the instance is named after, and the one the console links to.
type Service struct {
	NameSuffix string
	// Image is the registry reference WITHOUT a digest; Digest pins it. Split
	// because the console shows the human-readable ref and the renderer emits
	// ref@digest — an unpinned solution is not installable at all (see Pinned).
	Image  string
	Digest string
	// Command overrides the image entrypoint's arguments. Solutions built from
	// one image that supervises several roles (an agent gateway and its web
	// dashboard, say) need this to tell the roles apart.
	Command []string
	// Ports are compose "host:container" publish rules. Empty means the service
	// is reachable only from inside the stack — the correct default: a service
	// gets a published port only when a human has to open it in a browser.
	Ports []string
	// Volumes are compose "name:/path" mounts. A bare name (no leading /) is a
	// named docker volume; the installer prefixes it with the instance name so
	// two installs of the same solution on one VM never share state.
	Volumes []string
	// Primary marks the service whose published port the console links to.
	Primary bool
	// Description is shown in the console so the customer understands why one
	// install produced more than one app.
	Description string
}

// Solution is one installable product.
type Solution struct {
	Slug     string
	Name     string
	Tagline  string   // one line, catalog card
	About    string   // paragraph, detail page
	Bullets  []string // what it does, catalog card
	Category Category
	Vendor   string
	Homepage string
	License  string
	// DocsSlug is the console documentation page (frontend/content/docs) that
	// explains the install in prose. Empty means no page yet.
	DocsSlug string
	// MinVCPU / MinMemoryMB / MinDiskGB are the VM floor. The installer checks
	// nothing against them today — VM sizing lives with the customer's provider
	// order, not with us — but the console shows them BEFORE the install so
	// nobody installs an agent runtime onto a 1GB box and then files a bug about
	// the OOM killer.
	MinVCPU     int
	MinMemoryMB int
	MinDiskGB   int
	// Warning is the one thing a customer must read before installing. Rendered
	// prominently, not as fine print. Empty for solutions that carry no unusual
	// risk.
	Warning string
	Params  []Param
	Secrets []Generated
	// RevealKeys lists Generated.Key values the install response returns in
	// cleartext exactly once. Everything else the customer supplied is theirs
	// already; these are values the platform invented and the customer has no
	// other way to learn (they can still be re-read later through the ordinary
	// env-var reveal path, which is audited).
	RevealKeys []string
	Services   []Service
}

// Pinned reports whether every service image carries a digest.
//
// An unpinned solution is listed in the catalog but cannot be installed. That
// combination is deliberate: the catalog entry is code and ships with the
// release, while publishing the image is a separate operation that may land
// later. Listing it early tells the customer what is coming; refusing to install
// it keeps a one-click button from resolving to a tag that does not exist yet,
// which would fail deep inside a Portainer pull with an error nobody can read.
func (s Solution) Pinned() bool {
	for _, svc := range s.Services {
		if strings.TrimSpace(svc.Digest) == "" {
			return false
		}
	}
	return len(s.Services) > 0
}

// ImageRef returns the fully pinned image reference for a service.
func (svc Service) ImageRef() string {
	if strings.TrimSpace(svc.Digest) == "" {
		return svc.Image
	}
	return svc.Image + "@" + svc.Digest
}

// AppName returns the Application name a service gets for an install named
// instance.
func (svc Service) AppName(instance string) string {
	if svc.NameSuffix == "" {
		return instance
	}
	return instance + "-" + svc.NameSuffix
}

// hermesDataVolume is the single named volume both Hermes services mount. It
// holds ~/.hermes — config, sessions, memory, learned skills. It is the whole
// product: an agent that "gets better the longer it runs" is exactly this
// directory, so it is a named volume rather than a bind mount, and nothing in
// the install path ever deletes it.
const hermesDataVolume = "data"

// V1 is the v1 solution catalog. Frozen at deploy time.
//
// One entry on purpose. A catalog page with a single honest, fully-tested
// solution beats twelve entries assembled from upstream READMEs, because the
// promise a one-click install makes is not "we transcribed a compose file" but
// "this works, and we will answer the ticket when it does not". The next entries
// (n8n, ClickHouse, and the rest of the usual marketplace roster) each earn
// their place by being installed and run, not by being typed here.
var V1 = []Solution{
	{
		Slug:     "hermes-agent",
		Name:     "Hermes Agent",
		Tagline:  "Автономный AI-агент, который живёт на вашем сервере и учится на своей работе",
		Category: CategoryAIAgent,
		Vendor:   "Nous Research",
		Homepage: "https://github.com/NousResearch/hermes-agent",
		License:  "MIT",
		DocsSlug: "solutions-hermes-agent",
		About: "Hermes Agent — открытый автономный агент с собственным терминалом, файловой " +
			"системой и памятью. В отличие от плагина к IDE он не заканчивается вместе с сессией: " +
			"состояние, история и накопленные навыки лежат на диске сервера и переживают " +
			"перезапуск. Работает с любым OpenAI-совместимым провайдером — модель выбираете вы.",
		Bullets: []string{
			"Выполняет команды и код в собственном терминале",
			"Веб-дашборд с чатом, историей и поиском по сессиям",
			"Планировщик задач и делегирование субагентам",
			"Память и навыки, которые переживают перезапуск",
			"Любой OpenAI-совместимый провайдер модели",
		},
		// The numbers are the upstream image's own shape: a Debian image with a
		// Python venv, a node toolchain and a Playwright browser cache, plus
		// whatever the agent installs into its data volume while it works.
		MinVCPU:     2,
		MinMemoryMB: 4096,
		MinDiskGB:   20,
		Warning: "Агент выполняет команды в терминале сервера, читает и пишет файлы и ходит в " +
			"интернет. Ставьте его на отдельную VM, а ключи и токены выдавайте по мере " +
			"необходимости — каждый выданный ключ агент сможет использовать. Дашборд " +
			"публикуется на IP машины по HTTP: вход защищён логином и паролем, но сам трафик " +
			"не шифруется, поэтому открывайте его из доверенной сети или закрывайте своим " +
			"прокси с TLS.",
		Params: []Param{
			{
				Key:         "llm_base_url",
				EnvKey:      "OPENAI_BASE_URL",
				Label:       "Endpoint провайдера",
				Help:        "OpenAI-совместимый URL. Любой провайдер или ваш собственный сервер.",
				Kind:        ParamText,
				Required:    true,
				Default:     "https://api.openai.com/v1",
				Placeholder: "https://api.openai.com/v1",
			},
			{
				Key:      "llm_api_key",
				EnvKey:   "OPENAI_API_KEY",
				Label:    "API-ключ провайдера",
				Help:     "Хранится зашифрованным и виден агенту как переменная окружения.",
				Kind:     ParamSecret,
				Required: true,
			},
			{
				Key:    "model",
				EnvKey: "HERMES_MODEL",
				Label:  "Модель",
				Help: "Записывается в конфиг агента при первом запуске. Дальше модель " +
					"переключается в самом дашборде.",
				Kind:        ParamText,
				Required:    true,
				Placeholder: "gpt-5.2",
			},
			{
				Key:      "dashboard_user",
				EnvKey:   "HERMES_DASHBOARD_BASIC_AUTH_USERNAME",
				Label:    "Логин для дашборда",
				Kind:     ParamText,
				Required: true,
				Default:  "admin",
			},
		},
		Secrets: []Generated{
			{
				Key:    "dashboard_password",
				EnvKey: "HERMES_DASHBOARD_BASIC_AUTH_PASSWORD",
				Kind:   GeneratedPassword,
				Label:  "Пароль дашборда",
			},
			{
				// Without a stable signing key the dashboard mints a random one
				// per process, so every restart logs everyone out. Upstream logs
				// that at INFO and moves on; a managed install should not make
				// the customer discover it.
				Key:    "dashboard_session_secret",
				EnvKey: "HERMES_DASHBOARD_BASIC_AUTH_SECRET",
				Kind:   GeneratedSecret,
				Label:  "Ключ подписи сессий",
			},
		},
		RevealKeys: []string{"dashboard_password"},
		Services: []Service{
			{
				NameSuffix: "",
				Image:      hermesImage,
				Digest:     hermesDigest,
				Primary:    false,
				// The gateway is the agent itself. The bootstrap in front of it
				// runs ONCE per data volume: the upstream product keeps the
				// chosen model in its own config file (env is not read for it),
				// so a fresh volume needs one write to become usable — and a
				// volume that already has one must never be overwritten, or the
				// console would silently undo a model the customer changed in
				// the dashboard.
				Command: []string{"sh", "-lc", hermesBootstrap + "exec hermes gateway run"},
				Volumes: []string{hermesDataVolume + ":/opt/data"},
				Description: "Агент: терминал, инструменты, память, планировщик. Наружу не " +
					"публикуется.",
			},
			{
				NameSuffix: "dashboard",
				Image:      hermesImage,
				Digest:     hermesDigest,
				Primary:    true,
				// Bound to 0.0.0.0 — which upstream allows ONLY when an auth
				// provider is registered, and refuses otherwise (--insecure is
				// a no-op there since the June 2026 hardening). The install
				// always generates a password and a signing secret, so the
				// provider is always registered and the bind is always
				// authenticated. Do not "simplify" this by dropping the
				// generated credentials: the container would then fail closed
				// at boot, which is the correct upstream behaviour.
				Command: []string{"dashboard", "--host", "0.0.0.0", "--port", "9119", "--no-open"},
				Ports:   []string{"9119:9119"},
				Volumes: []string{hermesDataVolume + ":/opt/data"},
				Description: "Веб-дашборд: чат с агентом, история и поиск по сессиям. Порт 9119, " +
					"вход по логину и паролю.",
			},
		},
	},
}

// hermesImage / hermesDigest are the image the platform publishes for this
// solution.
//
// Upstream ships a Dockerfile and a compose file that BUILDS it (`build: .`);
// its CI builds an image named `hermes-agent:test` and never pushes it, so there
// is no official published image to point at. The platform therefore builds and
// publishes its own — see solutions/hermes-agent/README.md for the exact
// clone-build-push-pin recipe. Until the digest below is filled in, the entry is
// listed and NOT installable (Solution.Pinned), which is the honest state: the
// code is ready, the artifact is not.
const (
	hermesImage  = "ghcr.io/dadadevelopment/hermes-agent:v1"
	hermesDigest = ""
)

// hermesBootstrap seeds the model into the agent's config exactly once per data
// volume, then gets out of the way.
//
// Written as a shell prefix rather than an init service because a compose
// service in this platform always carries `restart: unless-stopped`: a one-shot
// init container would exit 0 and be restarted forever. The marker file lives in
// the data volume, so "once" means once per install, surviving container
// replacement and image upgrades.
const hermesBootstrap = `if [ ! -f /opt/data/.dada-bootstrapped ]; then ` +
	`hermes config set model "$HERMES_MODEL" >/dev/null 2>&1 || true; ` +
	`touch /opt/data/.dada-bootstrapped; fi; `

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

// instanceNameRe is the accepted install name: a compose service key and a
// Kubernetes-ish resource name at the same time, because the name becomes both.
var instanceNameRe = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// ValidateInstanceName checks the name an install will be recorded under.
//
// The limit is 40 rather than 63 because the longest service suffix is appended
// to it, and the result still has to be a legal app name.
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

// ResolveParams validates the supplied values against the solution's parameters
// and returns the environment variables they produce.
//
// Values are trimmed, missing optional values fall back to Default, and unknown
// keys are rejected rather than ignored — a typo in a parameter name is a
// misconfigured install that would otherwise surface as an agent that silently
// talks to the wrong endpoint.
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
			// A newline would split one .env line into two, turning the tail of a
			// value into an attacker-chosen variable.
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
