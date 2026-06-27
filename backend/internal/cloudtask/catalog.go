// Package cloudtask holds the curated cloud-task catalog: each entry maps a
// task_type to an agent skill plus a server-side parameter resolver. No user
// form in MVP — the cloud resolves params from config and live app state.
package cloudtask

import "fmt"

// ResolverCfg carries the server-side inputs a task's param resolver may need.
// MetrikaOAuthToken is a config secret. SiteURL is a best-effort live value the
// handler fills in from the app's public domain when cheaply resolvable; empty
// means "unknown", and the resolver omits it rather than blocking.
type ResolverCfg struct {
	MetrikaOAuthToken string
	SiteURL           string
}

// Entry is one curated cloud-task: which agent skill runs, where it surfaces,
// and how the cloud resolves its params server-side.
type Entry struct {
	TaskType      string
	SkillID       string
	Label         string
	Summary       string
	AppliesTo     func(kind string) bool
	ResolveParams func(cfg ResolverCfg) (map[string]any, error)
}

func isWeb(kind string) bool { return kind == "web" || kind == "App" || kind == "app" }

var defaultMetrikaGoals = []map[string]string{
	{"name": "Отправка формы", "identifier": "form_submit"},
	{"name": "Заполнил контактные данные", "identifier": "form_start"},
	{"name": "Клик по CTA", "identifier": "cta_contact_click"},
	{"name": "Клик по мессенджеру или телефону", "identifier": "messenger_click"},
}

// Catalog is the curated cloud-task set. Adding a task = one Entry here plus a
// matching cloud-task-tagged skill on the agent.
func Catalog() []Entry {
	return []Entry{
		{
			TaskType:  "yandex-metrika-goals",
			SkillID:   "yandex-metrika-goals",
			Label:     "Yandex Metrika + goals",
			Summary:   "Wire Yandex Metrika counter and conversion goals into the app, open a PR.",
			AppliesTo: isWeb,
			ResolveParams: func(cfg ResolverCfg) (map[string]any, error) {
				if cfg.MetrikaOAuthToken == "" {
					return nil, fmt.Errorf("METRIKA_OAUTH_TOKEN not configured")
				}
				params := map[string]any{
					"metrika_oauth_token": cfg.MetrikaOAuthToken,
					"goals":               defaultMetrikaGoals,
				}
				if cfg.SiteURL != "" {
					params["site_url"] = cfg.SiteURL
				}
				return params, nil
			},
		},
	}
}

// Lookup returns the catalog entry for a task_type, if present.
func Lookup(taskType string) (Entry, bool) {
	for _, e := range Catalog() {
		if e.TaskType == taskType {
			return e, true
		}
	}
	return Entry{}, false
}
