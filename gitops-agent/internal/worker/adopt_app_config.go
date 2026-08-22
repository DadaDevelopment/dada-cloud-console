package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/crypto"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// adoptAppConfigPayload names the app whose git-held configuration is being
// pulled into the console's model.
type adoptAppConfigPayload struct {
	AppName string `json:"app_name"`
}

// adoptedValues is what one app's values.yaml says about the things the console
// claims ownership of: the environment and the service port.
type adoptedValues struct {
	Plain       map[string]string
	Refs        []renderer.SecretRefEnvVar
	ServicePort int
	UseDotEnv   string
}

// adoptReport is written to operations.validation_result so the caller learns
// exactly what entered the console's model, key by key, without reading logs.
// Values never appear in it: it names keys and references only.
type adoptReport struct {
	App              string   `json:"app"`
	ValuesPath       string   `json:"values_path"`
	AdoptedPlain     []string `json:"adopted_plain"`
	AdoptedSecretRef []string `json:"adopted_secret_ref"`
	AlreadyInConsole []string `json:"already_in_console"`
	PortAdopted      *int     `json:"port_adopted,omitempty"`
	PortWas          *int     `json:"port_was,omitempty"`
	UseDotEnv        string   `json:"use_dot_env,omitempty"`
	Summary          string   `json:"summary"`
}

// doAdoptAppConfig makes an app that was never created through the console
// manageable BY the console, without changing a byte of what is running.
//
// The console renders values.yaml from its own database. An app that arrived
// any other way -- a hand-written app-of-apps entry, an infra service, a bot --
// has configuration the console's database has never heard of, and the two
// answers available until now were both bad: render anyway and delete what the
// console cannot express (which is how internal/prod/telemost-bot lost its
// service port, its .env mount and eight secret references on 2026-08-21), or
// refuse the write and leave the user with no lever at all. Adoption is the
// third answer: read what git says, record it as the console's own state, and
// from then on a console render REPRODUCES the app instead of replacing it.
//
// It writes nothing to git deliberately. The proof that adoption worked is that
// the next render is a no-op, and a git commit here would destroy the evidence
// by making the file agree with the console for a reason other than the console
// being right.
//
// Existing console rows win over git: a key the console already carries is a
// declared intent that a later deploy would apply anyway, so adoption adds only
// what is missing. That makes the operation idempotent -- running it twice
// adopts nothing the second time.
func (w *DBWatcher) doAdoptAppConfig(ctx context.Context, op db.Operation) error {
	var p adoptAppConfigPayload
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if strings.TrimSpace(p.AppName) == "" {
		return fmt.Errorf("adopt app config: app_name is required")
	}
	if op.EnvironmentID == nil {
		return fmt.Errorf("adopt app config: environment is required")
	}

	projectName, envName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}
	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return err
	}
	if _, err := mgr.Pull(); err != nil {
		return err
	}

	valuesPath := renderer.AppHelmValuesGitPath(projectName, envName, p.AppName)
	raw, err := mgr.ReadFile(valuesPath)
	if err != nil {
		return fmt.Errorf("adopt app config: %s has no %s in git, so there is nothing to adopt", p.AppName, valuesPath)
	}
	adopted, err := parseAdoptableValues(raw)
	if err != nil {
		return fmt.Errorf("adopt app config: %w", err)
	}

	report := adoptReport{
		App:              p.AppName,
		ValuesPath:       valuesPath,
		AdoptedPlain:     []string{},
		AdoptedSecretRef: []string{},
		AlreadyInConsole: []string{},
		UseDotEnv:        adopted.UseDotEnv,
	}

	known, err := w.consoleEnvKeys(ctx, *op.EnvironmentID, p.AppName)
	if err != nil {
		return err
	}

	for _, k := range sortedEnvKeys(adopted.Plain) {
		if known[k] {
			report.AlreadyInConsole = append(report.AlreadyInConsole, k)
			continue
		}
		if err := w.insertAdoptedValue(ctx, *op.EnvironmentID, p.AppName, k, adopted.Plain[k], op.ActorID); err != nil {
			return err
		}
		report.AdoptedPlain = append(report.AdoptedPlain, k)
	}
	for _, r := range adopted.Refs {
		if known[r.Name] {
			report.AlreadyInConsole = append(report.AlreadyInConsole, r.Name)
			continue
		}
		if err := w.insertAdoptedSecretRef(ctx, *op.EnvironmentID, p.AppName, r, op.ActorID); err != nil {
			return err
		}
		report.AdoptedSecretRef = append(report.AdoptedSecretRef, fmt.Sprintf("%s -> secret/%s:%s", r.Name, r.SecretName, r.SecretKey))
	}
	sort.Strings(report.AlreadyInConsole)

	if adopted.ServicePort > 0 {
		was, changed, err := w.adoptServicePort(ctx, op, p.AppName, adopted.ServicePort)
		if err != nil {
			return err
		}
		if changed {
			port := adopted.ServicePort
			report.PortAdopted = &port
			if was > 0 {
				prev := was
				report.PortWas = &prev
			}
		}
	}

	report.Summary = fmt.Sprintf(
		"adopted %d plain and %d secret-reference variables from %s; %d already known to the console",
		len(report.AdoptedPlain), len(report.AdoptedSecretRef), valuesPath, len(report.AlreadyInConsole),
	)
	result, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return db.MarkValidated(ctx, w.pool, op.ID, result, report.Summary)
}

// consoleEnvKeys reports which variables the console already carries for an
// app, so adoption never overwrites a value a human typed into the console.
func (w *DBWatcher) consoleEnvKeys(ctx context.Context, envID uuid.UUID, appName string) (map[string]bool, error) {
	rows, err := w.pool.Query(ctx,
		`SELECT key FROM env_vars WHERE environment_id = $1 AND app_name = $2`, envID, appName)
	if err != nil {
		return nil, fmt.Errorf("query env_vars: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// insertAdoptedValue stores one literal variable read out of git. is_secret is
// false because the value was already in a git repository in the clear -- calling
// it a secret here would only hide it from the person who can read the file.
func (w *DBWatcher) insertAdoptedValue(ctx context.Context, envID uuid.UUID, appName, key, value string, actorID uuid.UUID) error {
	enc, err := crypto.EncryptToken(w.cfg.EncryptionKey, []byte(value))
	if err != nil {
		return fmt.Errorf("encrypt adopted value for %s: %w", key, err)
	}
	_, err = w.pool.Exec(ctx, `
		INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope, created_by)
		VALUES ($1, $2, $3, $4, false, 'runtime', $5)
		ON CONFLICT (environment_id, app_name, key) DO NOTHING
	`, envID, appName, key, enc, actorID)
	if err != nil {
		return fmt.Errorf("insert adopted env var %s: %w", key, err)
	}
	return nil
}

// insertAdoptedSecretRef stores a pointer to a Secret the console does not own.
// Nothing is read out of that Secret: the reference is the whole record, which
// is what lets an app keep credentials the console must never see.
func (w *DBWatcher) insertAdoptedSecretRef(ctx context.Context, envID uuid.UUID, appName string, ref renderer.SecretRefEnvVar, actorID uuid.UUID) error {
	_, err := w.pool.Exec(ctx, `
		INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope, created_by, secret_ref_name, secret_ref_key)
		VALUES ($1, $2, $3, NULL, true, 'runtime', $4, $5, $6)
		ON CONFLICT (environment_id, app_name, key) DO NOTHING
	`, envID, appName, ref.Name, actorID, ref.SecretName, ref.SecretKey)
	if err != nil {
		return fmt.Errorf("insert adopted secret ref %s: %w", ref.Name, err)
	}
	return nil
}

// adoptServicePort makes the console's snapshot agree with the port the app is
// actually served on. Without it the console keeps a guess (telemost-bot's
// snapshot said 8080 while git and the running pod said 8000) and the first
// deploy that is allowed to write the port would break the app.
func (w *DBWatcher) adoptServicePort(ctx context.Context, op db.Operation, appName string, port int) (was int, changed bool, err error) {
	var summary []byte
	err = w.pool.QueryRow(ctx, `
		SELECT summary_json FROM resource_snapshots
		WHERE environment_id = $1 AND kind = 'App' AND name = $2
	`, op.EnvironmentID, appName).Scan(&summary)
	if err != nil {
		return 0, false, fmt.Errorf("read app snapshot: %w", err)
	}
	cur := map[string]any{}
	_ = json.Unmarshal(summary, &cur)
	if p, ok := cur["port"].(float64); ok {
		was = int(p)
	}
	if was == port {
		return was, false, nil
	}
	patch, _ := json.Marshal(map[string]any{
		"port":          port,
		"port_source":   appPortSourceAdopted,
		"adopted_at":    time.Now().UTC().Format(time.RFC3339),
		"adopted_by_op": op.ID.String(),
	})
	if _, err := w.pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET summary_json = COALESCE(summary_json, '{}'::jsonb) || $1::jsonb
		WHERE environment_id = $2 AND kind = 'App' AND name = $3
	`, patch, op.EnvironmentID, appName); err != nil {
		return was, false, fmt.Errorf("write adopted port: %w", err)
	}
	return was, true, nil
}

// appPortSourceAdopted marks a port the console learned from git rather than
// chose. It is deliberately distinct from the user/framework-default sources the
// backend writes: an adopted port is a report, not a decision.
const appPortSourceAdopted = "adopted"

// parseAdoptableValues reads the parts of an app's values.yaml the console has
// an opinion about. Everything else in the file stays git's business and is
// preserved by the merge, not by this function.
func parseAdoptableValues(raw string) (adoptedValues, error) {
	var doc struct {
		Common struct {
			ServicePort int    `yaml:"servicePort"`
			UseDotEnv   string `yaml:"useDotEnv"`
			ExtraEnv    []struct {
				Name      string  `yaml:"name"`
				Value     *string `yaml:"value"`
				ValueFrom *struct {
					SecretKeyRef *struct {
						Name string `yaml:"name"`
						Key  string `yaml:"key"`
					} `yaml:"secretKeyRef"`
				} `yaml:"valueFrom"`
			} `yaml:"extraEnv"`
		} `yaml:"common"`
	}
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return adoptedValues{}, fmt.Errorf("parsing values.yaml: %w", err)
	}
	out := adoptedValues{
		Plain:       map[string]string{},
		ServicePort: doc.Common.ServicePort,
		UseDotEnv:   doc.Common.UseDotEnv,
	}
	for _, e := range doc.Common.ExtraEnv {
		if e.Name == "" {
			continue
		}
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Name != "" {
			out.Refs = append(out.Refs, renderer.SecretRefEnvVar{
				Name:       e.Name,
				SecretName: e.ValueFrom.SecretKeyRef.Name,
				SecretKey:  e.ValueFrom.SecretKeyRef.Key,
			})
			continue
		}
		if e.Value != nil {
			out.Plain[e.Name] = *e.Value
		}
	}
	sort.Slice(out.Refs, func(i, j int) bool { return out.Refs[i].Name < out.Refs[j].Name })
	return out, nil
}

// sortedEnvKeys keeps the adoption report and its inserts in a stable order.
func sortedEnvKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
