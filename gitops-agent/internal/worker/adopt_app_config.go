package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	Plain        map[string]string
	Refs         []renderer.SecretRefEnvVar
	ServicePort  int
	UseDotEnv    string
	Image        string
	Replicas     *int
	Resources    *renderer.AppResources
	WorkloadType string
	StartCommand string
	Volume       *adoptedVolume
}

// adoptedVolume is the persistent directory an app already has in git.
type adoptedVolume struct {
	Path         string `json:"path"`
	Size         string `json:"size"`
	StorageClass string `json:"storage_class,omitempty"`
	FSGroup      int64  `json:"fs_group,omitempty"`
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
	AdoptedShape     []string `json:"adopted_shape"`
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
	report, err := w.adoptAppConfigFor(ctx, op, p.AppName)
	if err != nil {
		return err
	}
	result, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return db.MarkValidated(ctx, w.pool, op.ID, result, report.Summary)
}

// adoptAppConfigFor is adoption itself, separated from the operation that
// records it. The deploy path calls this directly: a deploy that is about to be
// refused because git holds configuration the console never learned does not
// need a human to go run the adopt verb and come back, it needs the console to
// learn. See adoptRetryAfterClobber.
func (w *DBWatcher) adoptAppConfigFor(ctx context.Context, op db.Operation, appName string) (adoptReport, error) {
	p := adoptAppConfigPayload{AppName: appName}
	if strings.TrimSpace(p.AppName) == "" {
		return adoptReport{}, fmt.Errorf("adopt app config: app_name is required")
	}
	if op.EnvironmentID == nil {
		return adoptReport{}, fmt.Errorf("adopt app config: environment is required")
	}

	projectName, envName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return adoptReport{}, fmt.Errorf("project/env lookup: %w", err)
	}
	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return adoptReport{}, err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return adoptReport{}, err
	}
	if _, err := mgr.Pull(); err != nil {
		return adoptReport{}, err
	}

	valuesPath := renderer.AppHelmValuesGitPath(projectName, envName, p.AppName)
	raw, err := mgr.ReadFile(valuesPath)
	if err != nil {
		return adoptReport{}, fmt.Errorf("adopt app config: %s has no %s in git, so there is nothing to adopt", p.AppName, valuesPath)
	}
	adopted, err := parseAdoptableValues(raw)
	if err != nil {
		return adoptReport{}, fmt.Errorf("adopt app config: %w", err)
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
		return adoptReport{}, err
	}

	for _, k := range sortedEnvKeys(adopted.Plain) {
		if known[k] {
			report.AlreadyInConsole = append(report.AlreadyInConsole, k)
			continue
		}
		if err := w.insertAdoptedValue(ctx, *op.EnvironmentID, p.AppName, k, adopted.Plain[k], op.ActorID); err != nil {
			return adoptReport{}, err
		}
		report.AdoptedPlain = append(report.AdoptedPlain, k)
	}
	for _, r := range adopted.Refs {
		if known[r.Name] {
			report.AlreadyInConsole = append(report.AlreadyInConsole, r.Name)
			continue
		}
		if err := w.insertAdoptedSecretRef(ctx, *op.EnvironmentID, p.AppName, r, op.ActorID); err != nil {
			return adoptReport{}, err
		}
		report.AdoptedSecretRef = append(report.AdoptedSecretRef, fmt.Sprintf("%s -> secret/%s:%s", r.Name, r.SecretName, r.SecretKey))
	}
	sort.Strings(report.AlreadyInConsole)

	shapeChanges, portAdopted, portWas, err := w.adoptAppShape(ctx, op, p.AppName, adopted)
	if err != nil {
		return adoptReport{}, err
	}
	report.AdoptedShape = shapeChanges
	if report.AdoptedShape == nil {
		report.AdoptedShape = []string{}
	}
	report.PortAdopted = portAdopted
	report.PortWas = portWas

	report.Summary = fmt.Sprintf(
		"adopted %d plain and %d secret-reference variables plus %d shape keys (%s) from %s; %d variables already known to the console",
		len(report.AdoptedPlain), len(report.AdoptedSecretRef), len(report.AdoptedShape),
		describeShape(report.AdoptedShape), valuesPath, len(report.AlreadyInConsole),
	)
	return report, nil
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
		INSERT INTO env_vars (environment_id, app_name, key, value_encrypted, is_secret, scope, created_by, secret_ref_name, secret_ref_key, secret_ref_optional)
		VALUES ($1, $2, $3, NULL, true, 'runtime', $4, $5, $6, $7)
		ON CONFLICT (environment_id, app_name, key) DO NOTHING
	`, envID, appName, ref.Name, actorID, ref.SecretName, ref.SecretKey, ref.Optional)
	if err != nil {
		return fmt.Errorf("insert adopted secret ref %s: %w", ref.Name, err)
	}
	return nil
}

// adoptAppShape makes the console's snapshot agree with the app that git
// already describes, for every key the console CLAIMS ownership of and would
// therefore write over on its next render.
//
// The values merge protects only keys the console has never heard of. For an
// owned key the merge replaces whatever git holds -- and the clobber guard is
// blind to a replacement, because it measures deletions. So an app whose
// snapshot disagrees with git is one console write away from being changed
// without a word: telemost-bot's snapshot said port 8080 while git and the pod
// said 8000, and its image tag, its resource envelope and its replica count are
// exactly as capable of drifting. Adoption settles the disagreement in git's
// favour, because git is what Argo applies and therefore what is running.
//
// The port additionally records port_source "adopted": a port learned from git
// is a report, not a decision, and the deploy path treats a decided port
// differently from a guessed one.
func (w *DBWatcher) adoptAppShape(ctx context.Context, op db.Operation, appName string, adopted adoptedValues) ([]string, *int, *int, error) {
	var summary []byte
	if err := w.pool.QueryRow(ctx, `
		SELECT summary_json FROM resource_snapshots
		WHERE environment_id = $1 AND kind = 'App' AND name = $2
	`, op.EnvironmentID, appName).Scan(&summary); err != nil {
		return nil, nil, nil, fmt.Errorf("read app snapshot: %w", err)
	}
	cur := map[string]any{}
	_ = json.Unmarshal(summary, &cur)

	patch := map[string]any{}
	changes := []string{}
	var portAdopted, portWas *int

	if adopted.ServicePort > 0 {
		was := 0
		if p, ok := cur["port"].(float64); ok {
			was = int(p)
		}
		if was != adopted.ServicePort {
			patch["port"] = adopted.ServicePort
			patch["port_source"] = appPortSourceAdopted
			port := adopted.ServicePort
			portAdopted = &port
			if was > 0 {
				prev := was
				portWas = &prev
			}
			changes = append(changes, fmt.Sprintf("port %s -> %d", describeWas(was > 0, was), adopted.ServicePort))
		}
	}
	if adopted.Image != "" {
		if was, _ := cur["image"].(string); was != adopted.Image {
			patch["image"] = adopted.Image
			changes = append(changes, fmt.Sprintf("image %s -> %s", describeWasString(was), adopted.Image))
		}
	}
	if adopted.Replicas != nil {
		was := 0
		if r, ok := cur["replicas"].(float64); ok {
			was = int(r)
		}
		if was != *adopted.Replicas {
			patch["replicas"] = *adopted.Replicas
			changes = append(changes, fmt.Sprintf("replicas %s -> %d", describeWas(was > 0, was), *adopted.Replicas))
		}
	}
	if adopted.Resources != nil {
		if !sameResources(resourcesFromSummary(cur), adopted.Resources) {
			patch["resources"] = adopted.Resources
			changes = append(changes, fmt.Sprintf("resources -> requests %s/%s, limits %s/%s",
				adopted.Resources.CPURequest, adopted.Resources.MemoryRequest,
				adopted.Resources.CPULimit, adopted.Resources.MemoryLimit))
		}
	}
	if adopted.WorkloadType != "" {
		if was := workloadTypeFromSummary(cur); was != adopted.WorkloadType {
			patch["workload_type"] = adopted.WorkloadType
			changes = append(changes, fmt.Sprintf("workload_type %s -> %s", describeWasString(was), adopted.WorkloadType))
		}
	}
	if adopted.StartCommand != "" {
		if was, _ := cur["start_command"].(string); was != adopted.StartCommand {
			patch["start_command"] = adopted.StartCommand
			changes = append(changes, "start_command adopted from git")
		}
	}
	if v := adopted.Volume; v != nil {
		path, size, class, fsGroup := volumeFromSummary(cur)
		if path != v.Path || size != v.Size || class != v.StorageClass || fsGroup != v.FSGroup {
			patch["volume"] = v
			changes = append(changes, fmt.Sprintf("volume %s (%s) adopted from git", v.Path, v.Size))
		}
	}
	if len(patch) == 0 {
		return changes, portAdopted, portWas, nil
	}
	patch["adopted_at"] = time.Now().UTC().Format(time.RFC3339)
	patch["adopted_by_op"] = op.ID.String()
	encoded, err := json.Marshal(patch)
	if err != nil {
		return nil, nil, nil, err
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET summary_json = COALESCE(summary_json, '{}'::jsonb) || $1::jsonb
		WHERE environment_id = $2 AND kind = 'App' AND name = $3
	`, encoded, op.EnvironmentID, appName); err != nil {
		return nil, nil, nil, fmt.Errorf("write adopted app shape: %w", err)
	}
	return changes, portAdopted, portWas, nil
}

// extraResourceKeys returns the resource keys of one requests/limits block that
// renderer.AppResources has no field for. They travel verbatim so a render
// cannot delete a dimension the console does not model.
func extraResourceKeys(m map[string]string) map[string]string {
	var out map[string]string
	for k, v := range m {
		switch k {
		case "cpu", "memory", "ephemeral-storage":
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out
}

// sameResources compares two resource envelopes, treating a missing one as
// different from any present one.
func sameResources(a, b *renderer.AppResources) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.CPURequest == b.CPURequest && a.MemoryRequest == b.MemoryRequest &&
		a.CPULimit == b.CPULimit && a.MemoryLimit == b.MemoryLimit &&
		a.EphemeralRequest == b.EphemeralRequest && a.EphemeralLimit == b.EphemeralLimit &&
		sameStringMap(a.ExtraRequests, b.ExtraRequests) &&
		sameStringMap(a.ExtraLimits, b.ExtraLimits)
}

// sameStringMap compares two resource-key maps by content.
func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// describeWas names the console's previous value for the adoption report, or
// says the console had none.
func describeWas(known bool, was int) string {
	if !known {
		return "(unset)"
	}
	return fmt.Sprintf("%d", was)
}

func describeWasString(was string) string {
	if was == "" {
		return "(unset)"
	}
	return was
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
			ServicePort  int    `yaml:"servicePort"`
			UseDotEnv    string `yaml:"useDotEnv"`
			Replicas     *int   `yaml:"replicas"`
			WorkloadType string `yaml:"workloadType"`
			StartCommand string `yaml:"startCommand"`
			Image        *struct {
				Name string `yaml:"name"`
				Tag  string `yaml:"tag"`
			} `yaml:"image"`
			Resources *struct {
				Requests map[string]string `yaml:"requests"`
				Limits   map[string]string `yaml:"limits"`
			} `yaml:"resources"`
			Pvc *struct {
				Size         string `yaml:"size"`
				StorageClass string `yaml:"storageClass"`
				Path         string `yaml:"path"`
			} `yaml:"pvc"`
			PodSecurityContext *struct {
				FSGroup int64 `yaml:"fsGroup"`
			} `yaml:"podSecurityContext"`
			ExtraEnv []struct {
				Name      string  `yaml:"name"`
				Value     *string `yaml:"value"`
				ValueFrom *struct {
					SecretKeyRef *struct {
						Name     string `yaml:"name"`
						Key      string `yaml:"key"`
						Optional bool   `yaml:"optional"`
					} `yaml:"secretKeyRef"`
				} `yaml:"valueFrom"`
			} `yaml:"extraEnv"`
		} `yaml:"common"`
	}
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return adoptedValues{}, fmt.Errorf("parsing values.yaml: %w", err)
	}
	out := adoptedValues{
		Plain:        map[string]string{},
		ServicePort:  doc.Common.ServicePort,
		UseDotEnv:    doc.Common.UseDotEnv,
		Replicas:     doc.Common.Replicas,
		WorkloadType: doc.Common.WorkloadType,
		StartCommand: doc.Common.StartCommand,
	}
	if img := doc.Common.Image; img != nil {
		switch {
		case img.Name != "" && img.Tag != "":
			out.Image = img.Name + ":" + img.Tag
		case img.Name != "":
			out.Image = img.Name
		case img.Tag != "":
			out.Image = ":" + img.Tag
		}
	}
	if r := doc.Common.Resources; r != nil {
		env := renderer.AppResources{
			CPURequest:       r.Requests["cpu"],
			MemoryRequest:    r.Requests["memory"],
			CPULimit:         r.Limits["cpu"],
			MemoryLimit:      r.Limits["memory"],
			EphemeralRequest: r.Requests["ephemeral-storage"],
			EphemeralLimit:   r.Limits["ephemeral-storage"],
		}
		env.ExtraRequests = extraResourceKeys(r.Requests)
		env.ExtraLimits = extraResourceKeys(r.Limits)
		if env.Complete() {
			out.Resources = &env
		}
	}
	if v := doc.Common.Pvc; v != nil && v.Path != "" {
		vol := adoptedVolume{Path: v.Path, Size: v.Size, StorageClass: v.StorageClass}
		if sc := doc.Common.PodSecurityContext; sc != nil {
			vol.FSGroup = sc.FSGroup
		}
		out.Volume = &vol
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
				Optional:   e.ValueFrom.SecretKeyRef.Optional,
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

// describeShape renders the adopted app-shape changes for the one-line summary.
func describeShape(changes []string) string {
	if len(changes) == 0 {
		return "the console already agreed with git"
	}
	return strings.Join(changes, "; ")
}

// adoptRetryAfterClobber decides what happens to a deploy the clobber guard is
// about to refuse: adopt the app's git-held configuration and run the render
// again, or hand the refusal back.
//
// The guard exists because rendering an app the console only half knows deletes
// the half it does not: on 2026-08-21 one env-var save stripped eight secret
// references, a service port and a .env mount off internal/prod/telemost-bot.
// Refusing protects the app. It does not, on its own, give anybody a way
// forward -- and the way forward it named was a second verb the caller had to
// know about, call by hand, and then retry the first one with. That is a repair
// the platform can do for itself: adoption reads git, writes nothing to git,
// changes nothing about the running app, and only teaches the console keys it
// was missing. Every ingredient of "safe to do automatically" is already true
// of it.
//
// retriedAfterAdopt is what bounds the recursion: on the retry the answer is
// always the refusal, so a deploy can trigger at most one adoption. If adoption
// adds nothing, the drops were never explainable by unlearned configuration and
// the caller gets the guard's refusal unchanged -- a deploy that would still
// delete real configuration must still be refused, and looping on it would only
// refuse more slowly.
func (w *DBWatcher) adoptRetryAfterClobber(ctx context.Context, op db.Operation, appName string, guardErr error, retriedAfterAdopt bool) (bool, error) {
	if retriedAfterAdopt {
		return false, guardErr
	}
	report, err := w.adoptForDeploy(ctx, op, appName)
	if err != nil {
		log.Printf("gitops: %s would be refused (%v) and adoption could not run: %v", appName, guardErr, err)
		return false, guardErr
	}
	learned := len(report.AdoptedPlain) + len(report.AdoptedSecretRef) + len(report.AdoptedShape)
	if learned == 0 {
		return false, guardErr
	}
	log.Printf("gitops: %s: adopted %d keys from git before deploying (%s); retrying the render",
		appName, learned, report.Summary)
	return true, nil
}

// adoptForDeploy is the adoption the deploy path runs, behind a field so a test
// can supply one that cannot touch a database or a git remote.
func (w *DBWatcher) adoptForDeploy(ctx context.Context, op db.Operation, appName string) (adoptReport, error) {
	if w.adoptForDeployFn != nil {
		return w.adoptForDeployFn(ctx, op, appName)
	}
	return w.adoptAppConfigFor(ctx, op, appName)
}
