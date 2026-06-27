// Package cloudtask holds the curated cloud-task catalog: each entry maps a
// task_type to an agent skill plus a server-side parameter resolver. No user
// form in MVP — the cloud resolves params from live cluster state.
package cloudtask

import "fmt"

// ResolverCfg carries the server-side inputs a task's param resolver needs.
//
// CounterID is resolved live from the project's YandexMetrikaCounter CR and is
// the authoritative Metrika counter id; empty means "not resolved" and the
// resolver fails rather than guessing. ProjectType is the app shape the skill
// instruments ("front" for web apps in MVP). Archetype is an optional cheap
// signal ("landing"/"dashboard"/"app"/"api"); empty lets the skill propose one.
//
// The Metrika OAuth token is intentionally absent: the metrika-instrumentor
// skill resolves it itself from the cluster secret, so the cloud never passes it.
type ResolverCfg struct {
	CounterID   string
	ProjectType string
	Archetype   string
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
				if cfg.CounterID == "" {
					return nil, fmt.Errorf("YandexMetrikaCounter counterId not resolved")
				}
				pt := cfg.ProjectType
				if pt == "" {
					pt = "front"
				}
				params := map[string]any{
					"counterId":   cfg.CounterID,
					"projectType": pt,
				}
				if cfg.Archetype != "" {
					params["archetype"] = cfg.Archetype
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
