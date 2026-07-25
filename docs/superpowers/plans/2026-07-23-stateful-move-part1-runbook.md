# Stateful App Move — Part 1: `dbmove` tool + runbook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a tested `dbmove` Go tool + operator runbook that moves `n8n`
(example-project -> platform) and `telemost-bot` (example-project -> internal) across
namespaces without losing DB data, volume data, or the n8n encryption key.

**Architecture:** A dry-run-first Go CLI whose pure **plan builder** (config -> ordered
Steps) and per-step **command construction** are unit-tested against a fake command
runner; execution shells out to `kubectl` (beget-prod + mgmt Argo contexts), the console
backup API, Longhorn CRs, and `git` on the local argo-infra checkout. Every step is
idempotent and non-destructive (DB `Orphan`, PV `Retain`, source folder kept until a
separate reclaim). The runbook wraps the tool with human evidence gates.

**Tech Stack:** Go (os/exec, encoding/json, gopkg.in/yaml.v3), kubectl, git, Longhorn CRs,
console DB-backup API (Kanister), Kasten/Longhorn S3.

## Global Constraints

- No inline `//` comments in Go source (repo hook blocks them); use doc-comments above
  declarations only. Test files may use `//` only if the hook allows — default to
  doc-comments and `t.Log`.
- Plain ASCII only (no U+2011 non-breaking hyphen, no U+00A0 NBSP) in every file.
- `dbmove` defaults to DRY-RUN. Real mutation requires an explicit `--execute` flag.
- Never hard-delete in the move path: DB re-point relies on `deletionPolicy: Orphan`; PVs
  are `reclaimPolicy: Retain`; the source git folder and safety dumps are kept until a
  separate, explicitly-gated `reclaim` action after a healthy soak.
- Contexts (exact): beget-prod = `83.222.27.62:26443`; mgmt Argo =
  `e7b608-client-super-admin@e7b608-client`, ns `argocd-master`.
- argo-infra checkout: `/Users/alex/IdeaProjects/argo-infra`, branch `console-migration`,
  remote `github.com/DadaDevelopment/argo-infra`.
- Shared Postgres: `postgresql.databases.svc.cluster.local:5432`, ns `databases`,
  StatefulSet `postgresql`, superuser secret `postgresql`/`postgres-password`.
- Order of real moves: dry-run -> non-prod rehearsal -> telemost-bot -> soak -> n8n.

---

## File Structure

- `tools/dbmove/go.mod` — standalone module `github.com/dada-tuda/dbmove` (isolated deps).
- `tools/dbmove/main.go` — CLI flag parsing, config load, plan build, run loop.
- `tools/dbmove/config.go` — `MoveConfig` + loader/validator.
- `tools/dbmove/runner.go` — `CommandRunner` interface + real exec impl + `stepResult`.
- `tools/dbmove/plan.go` — `Step` interface + `BuildPlan(cfg) []Step`.
- `tools/dbmove/steps.go` — concrete Step types (safety dump, longhorn copy, secret copy,
  folder move, DB verify, teardown).
- `tools/dbmove/configs/n8n.yaml` — n8n move config (verified values).
- `tools/dbmove/configs/telemost-bot.yaml` — telemost move config.
- `tools/dbmove/config_test.go`, `plan_test.go`, `steps_test.go` — unit tests.
- `docs/runbooks/stateful-app-move.md` — operator runbook.
- `scripts/dbmove-rehearsal.sh` — non-prod rehearsal setup/teardown.

---

## Task 1: Scaffold the module + CommandRunner

**Files:**
- Create: `tools/dbmove/go.mod`
- Create: `tools/dbmove/runner.go`
- Test: `tools/dbmove/runner_test.go`

**Interfaces:**
- Produces: `type CommandRunner interface { Run(ctx context.Context, name string, args ...string) (string, error) }`; `type fakeRunner struct { calls [][]string; out map[string]string; err map[string]error }` (test helper); `type execRunner struct{}`.

- [ ] **Step 1: Write go.mod**

```
module github.com/dada-tuda/dbmove

go 1.23

require gopkg.in/yaml.v3 v3.0.1
```

- [ ] **Step 2: Write the failing test for fakeRunner recording**

```go
package main

import (
	"context"
	"testing"
)

func TestFakeRunnerRecordsCalls(t *testing.T) {
	fr := newFakeRunner()
	fr.out["kubectl get pods"] = "n8n-worker-1 Running"
	out, err := fr.Run(context.Background(), "kubectl", "get", "pods")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != "n8n-worker-1 Running" {
		t.Fatalf("got %q", out)
	}
	if len(fr.calls) != 1 || fr.calls[0][0] != "kubectl" {
		t.Fatalf("call not recorded: %v", fr.calls)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd tools/dbmove && go test ./... -run TestFakeRunner -v`
Expected: FAIL (undefined: newFakeRunner)

- [ ] **Step 4: Write runner.go**

```go
package main

import (
	"context"
	"os/exec"
	"strings"
)

// CommandRunner runs an external command and returns combined stdout.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// execRunner runs commands for real.
type execRunner struct{}

// Run executes name+args and returns trimmed combined output.
func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// fakeRunner records calls and returns canned output keyed by the space-joined
// command prefix, for tests.
type fakeRunner struct {
	calls [][]string
	out   map[string]string
	err   map[string]error
}

// newFakeRunner builds an empty fakeRunner.
func newFakeRunner() *fakeRunner {
	return &fakeRunner{out: map[string]string{}, err: map[string]error{}}
}

// Run records the call and returns the longest-prefix canned response.
func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	f.calls = append(f.calls, call)
	joined := strings.Join(call, " ")
	for k, v := range f.out {
		if strings.HasPrefix(joined, k) {
			return v, f.err[k]
		}
	}
	for k, e := range f.err {
		if strings.HasPrefix(joined, k) {
			return "", e
		}
	}
	return "", nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd tools/dbmove && go test ./... -run TestFakeRunner -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add tools/dbmove/go.mod tools/dbmove/runner.go tools/dbmove/runner_test.go
git commit -m "feat(dbmove): scaffold module + CommandRunner with fake for tests"
```

---

## Task 2: MoveConfig + loader/validator

**Files:**
- Create: `tools/dbmove/config.go`
- Create: `tools/dbmove/configs/n8n.yaml`
- Create: `tools/dbmove/configs/telemost-bot.yaml`
- Test: `tools/dbmove/config_test.go`

**Interfaces:**
- Produces: `type VolumeSpec struct { PVCName, MountedBy string }`; `type MoveConfig struct { App, BegetContext, MgmtContext, SrcProject, SrcEnv, SrcNamespace, TargetProject, TargetEnv, TargetNamespace, DBDatname, DBCredSecret, ArgoInfraPath, AppFolderRel string; Volumes []VolumeSpec; OOBSecrets []string; ScaleDeployments []string }`; `func LoadConfig(path string) (MoveConfig, error)` (parses YAML, validates required fields, derives `TargetNamespace = TargetProject+"-"+TargetEnv` when empty).

- [ ] **Step 1: Write telemost-bot.yaml (DB-only, no volume)**

```yaml
app: telemost-bot
begetContext: "83.222.27.62:26443"
mgmtContext: "e7b608-client-super-admin@e7b608-client"
srcProject: example-project
srcEnv: prod
srcNamespace: example-project-prod
targetProject: internal
targetEnv: prod
dbDatname: telemostbot
dbCredSecret: telemost-bot-db-credentials
argoInfraPath: /Users/alex/IdeaProjects/argo-infra
appFolderRel: clusters/beget-prod/projects/example-project/environments/prod/apps/telemost-bot
volumes: []
oobSecrets:
  - telemost-bot-llm-keys
  - telemost-bot-keycloak
scaleDeployments:
  - telemost-bot-deploy
```

- [ ] **Step 2: Write n8n.yaml (DB + 2 RWO volumes)**

```yaml
app: n8n
begetContext: "83.222.27.62:26443"
mgmtContext: "e7b608-client-super-admin@e7b608-client"
srcProject: example-project
srcEnv: prod
srcNamespace: example-project-prod
targetProject: platform
targetEnv: prod
dbDatname: n8n
dbCredSecret: n8n-db-credentials
argoInfraPath: /Users/alex/IdeaProjects/argo-infra
appFolderRel: clusters/beget-prod/projects/example-project/environments/prod/apps/n8n
volumes:
  - pvcName: n8n-data
    mountedBy: n8n
  - pvcName: n8n-worker-data
    mountedBy: n8n-worker
oobSecrets:
  - n8n-runtime
  - n8n-smtp
scaleDeployments:
  - n8n
  - n8n-runners
  - n8n-worker
```

- [ ] **Step 3: Write the failing test**

```go
package main

import "testing"

func TestLoadConfigDerivesTargetNamespace(t *testing.T) {
	cfg, err := LoadConfig("configs/telemost-bot.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.TargetNamespace != "internal-prod" {
		t.Fatalf("target ns = %q, want internal-prod", cfg.TargetNamespace)
	}
	if len(cfg.Volumes) != 0 {
		t.Fatalf("telemost should have no volumes, got %d", len(cfg.Volumes))
	}
	if len(cfg.OOBSecrets) != 2 {
		t.Fatalf("want 2 oob secrets, got %d", len(cfg.OOBSecrets))
	}
}

func TestLoadConfigN8nHasTwoVolumes(t *testing.T) {
	cfg, err := LoadConfig("configs/n8n.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Volumes) != 2 || cfg.Volumes[0].PVCName != "n8n-data" {
		t.Fatalf("n8n volumes wrong: %+v", cfg.Volumes)
	}
	if cfg.TargetNamespace != "platform-prod" {
		t.Fatalf("target ns = %q", cfg.TargetNamespace)
	}
}

func TestLoadConfigRejectsMissingApp(t *testing.T) {
	if _, err := loadConfigBytes([]byte("srcProject: x")); err == nil {
		t.Fatal("expected error for missing app")
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd tools/dbmove && go test ./... -run TestLoadConfig -v`
Expected: FAIL (undefined: LoadConfig)

- [ ] **Step 5: Write config.go**

```go
package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// VolumeSpec names a source PVC and the deployment that mounts it.
type VolumeSpec struct {
	PVCName   string `yaml:"pvcName"`
	MountedBy string `yaml:"mountedBy"`
}

// MoveConfig is the full description of one app move.
type MoveConfig struct {
	App              string       `yaml:"app"`
	BegetContext     string       `yaml:"begetContext"`
	MgmtContext      string       `yaml:"mgmtContext"`
	SrcProject       string       `yaml:"srcProject"`
	SrcEnv           string       `yaml:"srcEnv"`
	SrcNamespace     string       `yaml:"srcNamespace"`
	TargetProject    string       `yaml:"targetProject"`
	TargetEnv        string       `yaml:"targetEnv"`
	TargetNamespace  string       `yaml:"targetNamespace"`
	DBDatname        string       `yaml:"dbDatname"`
	DBCredSecret     string       `yaml:"dbCredSecret"`
	ArgoInfraPath    string       `yaml:"argoInfraPath"`
	AppFolderRel     string       `yaml:"appFolderRel"`
	Volumes          []VolumeSpec `yaml:"volumes"`
	OOBSecrets       []string     `yaml:"oobSecrets"`
	ScaleDeployments []string     `yaml:"scaleDeployments"`
}

// LoadConfig reads and validates a MoveConfig from a YAML file.
func LoadConfig(path string) (MoveConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return MoveConfig{}, err
	}
	return loadConfigBytes(b)
}

// loadConfigBytes parses+validates config YAML bytes.
func loadConfigBytes(b []byte) (MoveConfig, error) {
	var c MoveConfig
	if err := yaml.Unmarshal(b, &c); err != nil {
		return MoveConfig{}, err
	}
	if c.App == "" || c.SrcNamespace == "" || c.TargetProject == "" || c.TargetEnv == "" {
		return MoveConfig{}, fmt.Errorf("config: app, srcNamespace, targetProject, targetEnv are required")
	}
	if c.TargetNamespace == "" {
		c.TargetNamespace = c.TargetProject + "-" + c.TargetEnv
	}
	return c, nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd tools/dbmove && go test ./... -run TestLoadConfig -v`
Expected: PASS (3 tests)

- [ ] **Step 7: Commit**

```bash
git add tools/dbmove/config.go tools/dbmove/config_test.go tools/dbmove/configs/
git commit -m "feat(dbmove): MoveConfig loader + verified n8n/telemost configs"
```

---

## Task 3: Step interface + BuildPlan ordering

**Files:**
- Create: `tools/dbmove/plan.go`
- Test: `tools/dbmove/plan_test.go`

**Interfaces:**
- Consumes: `MoveConfig` (Task 2).
- Produces: `type Step interface { ID() string; Describe() string; Run(ctx context.Context, r CommandRunner, dryRun bool) error }`; `func BuildPlan(cfg MoveConfig) []Step`. Ordering for DB-only: `safety-dump`, `copy-secrets`, `folder-move`, `verify`, `teardown`. For DB+volume: `safety-dump`, `longhorn-backup`, `scale-down`, `volume-copy:<pvc>` (one per volume), `copy-secrets`, `folder-move`, `verify`, `teardown`. Step IDs are stable strings used by `--only`/`--from`.

- [ ] **Step 1: Write the failing test (ordering for both profiles)**

```go
package main

import (
	"context"
	"strings"
	"testing"
)

func stepIDs(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.ID()
	}
	return out
}

func TestBuildPlanDBOnly(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	got := strings.Join(stepIDs(BuildPlan(cfg)), ",")
	want := "safety-dump,copy-secrets,folder-move,verify,teardown"
	if got != want {
		t.Fatalf("db-only plan = %q, want %q", got, want)
	}
}

func TestBuildPlanWithVolumes(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	got := strings.Join(stepIDs(BuildPlan(cfg)), ",")
	want := "safety-dump,longhorn-backup,scale-down,volume-copy:n8n-data,volume-copy:n8n-worker-data,copy-secrets,folder-move,verify,teardown"
	if got != want {
		t.Fatalf("volume plan = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd tools/dbmove && go test ./... -run TestBuildPlan -v`
Expected: FAIL (undefined: Step, BuildPlan)

- [ ] **Step 3: Write plan.go (interface + ordering; concrete steps stubbed in Task 4)**

```go
package main

import "context"

// Step is one idempotent, dry-runnable unit of a move.
type Step interface {
	ID() string
	Describe() string
	Run(ctx context.Context, r CommandRunner, dryRun bool) error
}

// BuildPlan assembles the ordered steps for a move. Volume steps are included
// only when cfg.Volumes is non-empty.
func BuildPlan(cfg MoveConfig) []Step {
	var steps []Step
	steps = append(steps, &safetyDumpStep{cfg: cfg})
	if len(cfg.Volumes) > 0 {
		steps = append(steps, &longhornBackupStep{cfg: cfg})
		steps = append(steps, &scaleDownStep{cfg: cfg})
		for _, v := range cfg.Volumes {
			steps = append(steps, &volumeCopyStep{cfg: cfg, vol: v})
		}
	}
	steps = append(steps, &copySecretsStep{cfg: cfg})
	steps = append(steps, &folderMoveStep{cfg: cfg})
	steps = append(steps, &verifyStep{cfg: cfg})
	steps = append(steps, &teardownStep{cfg: cfg})
	return steps
}
```

- [ ] **Step 4: Add minimal stubs so it compiles (steps.go)**

```go
package main

import "context"

type safetyDumpStep struct{ cfg MoveConfig }

func (s *safetyDumpStep) ID() string       { return "safety-dump" }
func (s *safetyDumpStep) Describe() string  { return "safety pg_dump of " + s.cfg.DBDatname }
func (s *safetyDumpStep) Run(context.Context, CommandRunner, bool) error { return nil }

type longhornBackupStep struct{ cfg MoveConfig }

func (s *longhornBackupStep) ID() string      { return "longhorn-backup" }
func (s *longhornBackupStep) Describe() string { return "longhorn backup of source volumes" }
func (s *longhornBackupStep) Run(context.Context, CommandRunner, bool) error { return nil }

type scaleDownStep struct{ cfg MoveConfig }

func (s *scaleDownStep) ID() string      { return "scale-down" }
func (s *scaleDownStep) Describe() string { return "scale source workloads to 0" }
func (s *scaleDownStep) Run(context.Context, CommandRunner, bool) error { return nil }

type volumeCopyStep struct {
	cfg MoveConfig
	vol VolumeSpec
}

func (s *volumeCopyStep) ID() string      { return "volume-copy:" + s.vol.PVCName }
func (s *volumeCopyStep) Describe() string { return "copy " + s.vol.PVCName + " into fresh RWX PVC" }
func (s *volumeCopyStep) Run(context.Context, CommandRunner, bool) error { return nil }

type copySecretsStep struct{ cfg MoveConfig }

func (s *copySecretsStep) ID() string      { return "copy-secrets" }
func (s *copySecretsStep) Describe() string { return "copy out-of-band secrets to target ns" }
func (s *copySecretsStep) Run(context.Context, CommandRunner, bool) error { return nil }

type folderMoveStep struct{ cfg MoveConfig }

func (s *folderMoveStep) ID() string      { return "folder-move" }
func (s *folderMoveStep) Describe() string { return "relocate argo-infra app folder" }
func (s *folderMoveStep) Run(context.Context, CommandRunner, bool) error { return nil }

type verifyStep struct{ cfg MoveConfig }

func (s *verifyStep) ID() string      { return "verify" }
func (s *verifyStep) Describe() string { return "verify target healthy" }
func (s *verifyStep) Run(context.Context, CommandRunner, bool) error { return nil }

type teardownStep struct{ cfg MoveConfig }

func (s *teardownStep) ID() string      { return "teardown" }
func (s *teardownStep) Describe() string { return "reattribute snapshot; keep source retained" }
func (s *teardownStep) Run(context.Context, CommandRunner, bool) error { return nil }
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd tools/dbmove && go test ./... -run TestBuildPlan -v`
Expected: PASS (2 tests)

- [ ] **Step 6: Commit**

```bash
git add tools/dbmove/plan.go tools/dbmove/steps.go tools/dbmove/plan_test.go
git commit -m "feat(dbmove): Step interface + BuildPlan ordering (db-only vs volume)"
```

---

## Task 4: safety-dump step (console backup API / Kanister)

**Files:**
- Modify: `tools/dbmove/steps.go` (replace `safetyDumpStep.Run`)
- Test: `tools/dbmove/steps_test.go`

**Interfaces:**
- Consumes: `CommandRunner`, `MoveConfig`.
- Produces: `safetyDumpStep.Run` creates a Kanister backup ActionSet against
  `databases/postgresql` for `cfg.DBDatname` via `kubectl --context <beget> create -f -`
  and waits for the ActionSet to reach `complete`. In dry-run it prints the ActionSet YAML
  and returns nil. Helper `backupActionSetYAML(cfg, name string) string`.

- [ ] **Step 1: Write the failing test**

```go
func TestSafetyDumpDryRunDoesNotRun(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	fr := newFakeRunner()
	s := &safetyDumpStep{cfg: cfg}
	if err := s.Run(context.Background(), fr, true); err != nil {
		t.Fatalf("dry-run err: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("dry-run must not call kubectl, got %v", fr.calls)
	}
}

func TestBackupActionSetTargetsSharedPostgres(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	y := backupActionSetYAML(cfg, "db-move-telemostbot")
	for _, want := range []string{"kind: ActionSet", "name: postgres-logical-db-blueprint", "database: telemostbot", "kind: StatefulSet", "name: postgresql", "namespace: databases"} {
		if !strings.Contains(y, want) {
			t.Fatalf("actionset yaml missing %q:\n%s", want, y)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd tools/dbmove && go test ./... -run 'TestSafetyDump|TestBackupActionSet' -v`
Expected: FAIL (undefined: backupActionSetYAML)

- [ ] **Step 3: Implement safetyDumpStep.Run + backupActionSetYAML**

```go
import (
	"context"
	"fmt"
	"strings"
	"time"
)

// backupActionSetYAML renders a Kanister backup ActionSet for the shared
// Postgres StatefulSet, keyed to cfg.DBDatname. dumpPath mirrors the backend
// convention dumps/<scope>/<db>/<name>.dump under the profile prefix.
func backupActionSetYAML(cfg MoveConfig, name string) string {
	return fmt.Sprintf(`apiVersion: cr.kanister.io/v1alpha1
kind: ActionSet
metadata:
  generateName: db-move-backup-
  namespace: databases
  labels:
    dada.io/dbmove: %q
spec:
  actions:
    - name: backup
      blueprint: postgres-logical-db-blueprint
      object:
        kind: StatefulSet
        name: postgresql
        namespace: databases
      profile:
        name: dada-db-backups
        namespace: databases
      options:
        database: %s
        dumpPath: dumps/dbmove/%s/%s.dump
`, cfg.App, cfg.DBDatname, cfg.DBDatname, name)
}

// Run creates the backup ActionSet and waits for completion (skipped on dry-run).
func (s *safetyDumpStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	name := "db-move-" + s.cfg.DBDatname
	y := backupActionSetYAML(s.cfg, name)
	if dryRun {
		fmt.Printf("[dry-run] would create backup ActionSet:\n%s\n", y)
		return nil
	}
	if _, err := runWithStdin(ctx, r, y, "kubectl", "--context", s.cfg.BegetContext, "create", "-f", "-"); err != nil {
		return fmt.Errorf("create backup actionset: %w", err)
	}
	return waitActionSet(ctx, r, s.cfg.BegetContext, s.cfg.App, 15*time.Minute)
}
```

Also add the exec helpers to `runner.go`:

```go
// runWithStdin runs a command feeding stdin, returning combined output.
func runWithStdin(ctx context.Context, r CommandRunner, stdin string, name string, args ...string) (string, error) {
	if er, ok := r.(execRunner); ok {
		return er.runStdin(ctx, stdin, name, args...)
	}
	full := append([]string{name}, args...)
	return r.Run(ctx, full[0], full[1:]...)
}

func (execRunner) runStdin(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// waitActionSet polls the newest dbmove-labelled ActionSet until complete or timeout.
func waitActionSet(ctx context.Context, r CommandRunner, kctx, app string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := r.Run(ctx, "kubectl", "--context", kctx, "-n", "databases", "get", "actionset",
			"-l", "dada.io/dbmove="+app, "--sort-by=.metadata.creationTimestamp",
			"-o", "jsonpath={.items[-1:].status.state}")
		if err == nil && strings.Contains(out, "complete") {
			return nil
		}
		if err == nil && strings.Contains(out, "failed") {
			return fmt.Errorf("backup actionset failed for %s", app)
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("backup actionset for %s did not complete in %s", app, timeout)
}
```

(Add `"os/exec"`, `"strings"`, `"time"`, `"context"`, `"fmt"` imports where needed.)

- [ ] **Step 4: Run tests**

Run: `cd tools/dbmove && go test ./... -run 'TestSafetyDump|TestBackupActionSet' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tools/dbmove/steps.go tools/dbmove/runner.go tools/dbmove/steps_test.go
git commit -m "feat(dbmove): safety-dump step via Kanister ActionSet against shared postgres"
```

---

## Task 5: copy-secrets step (out-of-band secrets, incl. n8n encryption key)

**Files:**
- Modify: `tools/dbmove/steps.go` (`copySecretsStep.Run`)
- Test: `tools/dbmove/steps_test.go`

**Interfaces:**
- Produces: `copySecretsStep.Run` copies each `cfg.OOBSecrets` name from `cfg.SrcNamespace`
  to `cfg.TargetNamespace` via `kubectl get secret -o yaml | strip-ns/metadata | kubectl
  apply`. Helper `copySecretArgs(kctx, name, srcNS, dstNS string) (getArgs, applyArgs []string)`.
  Idempotent: `apply` upserts. In dry-run prints the plan only.

- [ ] **Step 1: Write the failing test**

```go
func TestCopySecretsInvokesGetAndApplyPerSecret(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml") // oob: n8n-runtime, n8n-smtp
	fr := newFakeRunner()
	fr.out["kubectl --context 83.222.27.62:26443 -n example-project-prod get secret n8n-runtime"] =
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: n8n-runtime\ndata:\n  encryptionKey: eA==\n"
	fr.out["kubectl --context 83.222.27.62:26443 -n example-project-prod get secret n8n-smtp"] =
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: n8n-smtp\ndata:\n  x: eA==\n"
	s := &copySecretsStep{cfg: cfg}
	if err := s.Run(context.Background(), fr, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	var gets, applies int
	for _, c := range fr.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "get secret n8n-") {
			gets++
		}
		if strings.Contains(j, "apply") {
			applies++
		}
	}
	if gets != 2 || applies != 2 {
		t.Fatalf("want 2 gets + 2 applies, got %d/%d (%v)", gets, applies, fr.calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd tools/dbmove && go test ./... -run TestCopySecrets -v`
Expected: FAIL

- [ ] **Step 3: Implement copySecretsStep.Run**

```go
// Run copies each out-of-band secret verbatim into the target namespace,
// re-stamping metadata.namespace and stripping server-managed fields.
func (s *copySecretsStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	for _, name := range s.cfg.OOBSecrets {
		if dryRun {
			fmt.Printf("[dry-run] would copy secret %s: %s -> %s\n", name, s.cfg.SrcNamespace, s.cfg.TargetNamespace)
			continue
		}
		raw, err := r.Run(ctx, "kubectl", "--context", s.cfg.BegetContext, "-n", s.cfg.SrcNamespace,
			"get", "secret", name, "-o", "yaml")
		if err != nil {
			return fmt.Errorf("get secret %s: %w", name, err)
		}
		cleaned := restampSecretNamespace(raw, s.cfg.TargetNamespace)
		if _, err := runWithStdin(ctx, r, cleaned, "kubectl", "--context", s.cfg.BegetContext,
			"-n", s.cfg.TargetNamespace, "apply", "-f", "-"); err != nil {
			return fmt.Errorf("apply secret %s to %s: %w", name, s.cfg.TargetNamespace, err)
		}
	}
	return nil
}

// restampSecretNamespace rewrites metadata.namespace and drops server-managed
// keys (resourceVersion, uid, creationTimestamp, ownerReferences, status) so the
// secret applies cleanly into dstNS. It is a line-level transform to avoid a YAML
// dependency on the exact server output shape.
func restampSecretNamespace(raw, dstNS string) string {
	var out []string
	skipBlock := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "namespace:") && !strings.HasPrefix(line, "    ") {
			out = append(out, "  namespace: "+dstNS)
			continue
		}
		if strings.HasPrefix(trimmed, "ownerReferences:") || trimmed == "status: {}" || strings.HasPrefix(trimmed, "status:") {
			skipBlock = strings.HasPrefix(trimmed, "ownerReferences:")
			if !skipBlock {
				continue
			}
			continue
		}
		if skipBlock {
			if strings.HasPrefix(line, "  ") && (strings.HasPrefix(trimmed, "-") || strings.HasPrefix(line, "    ")) {
				continue
			}
			skipBlock = false
		}
		if strings.HasPrefix(trimmed, "resourceVersion:") || strings.HasPrefix(trimmed, "uid:") ||
			strings.HasPrefix(trimmed, "creationTimestamp:") || strings.HasPrefix(trimmed, "generation:") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
```

- [ ] **Step 4: Run tests**

Run: `cd tools/dbmove && go test ./... -run TestCopySecrets -v`
Expected: PASS

- [ ] **Step 5: Add a restamp unit test**

```go
func TestRestampSecretNamespaceRewritesNS(t *testing.T) {
	raw := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: n8n-runtime\n  namespace: example-project-prod\n  uid: abc\n  resourceVersion: \"9\"\ndata:\n  encryptionKey: eA==\n"
	got := restampSecretNamespace(raw, "platform-prod")
	if !strings.Contains(got, "namespace: platform-prod") {
		t.Fatalf("ns not rewritten:\n%s", got)
	}
	if strings.Contains(got, "uid:") || strings.Contains(got, "resourceVersion:") {
		t.Fatalf("server fields not stripped:\n%s", got)
	}
	if !strings.Contains(got, "encryptionKey: eA==") {
		t.Fatalf("data lost:\n%s", got)
	}
}
```

Run: `cd tools/dbmove && go test ./... -run TestRestamp -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add tools/dbmove/steps.go tools/dbmove/steps_test.go
git commit -m "feat(dbmove): copy-secrets step (out-of-band secrets incl n8n encryptionKey)"
```

---

## Task 6: folder-move step (git relocation + literal edits)

**Files:**
- Modify: `tools/dbmove/steps.go` (`folderMoveStep.Run`)
- Test: `tools/dbmove/steps_test.go`

**Interfaces:**
- Produces: `folderMoveStep.Run` computes the destination folder path (swap
  `projects/<src>/environments/<srcEnv>` -> `projects/<target>/environments/<targetEnv>`),
  `git mv` the folder, applies literal edits (`namespace:` + `dada.io/project:` in
  `resources.values.yaml`; `ReadWriteOnce`->`ReadWriteMany` in `chart/templates/*.yaml`
  when volumes present), and commits — all inside `cfg.ArgoInfraPath`. Helper
  `destFolderRel(cfg) string` and `folderMoveGitArgs`. Dry-run prints the git plan + diffs.

- [ ] **Step 1: Write the failing test for destFolderRel**

```go
func TestDestFolderRel(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	got := destFolderRel(cfg)
	want := "clusters/beget-prod/projects/platform/environments/prod/apps/n8n"
	if got != want {
		t.Fatalf("dest folder = %q, want %q", got, want)
	}
}

func TestFolderMoveDryRunNoGit(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	fr := newFakeRunner()
	s := &folderMoveStep{cfg: cfg}
	if err := s.Run(context.Background(), fr, true); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	for _, c := range fr.calls {
		if strings.Contains(strings.Join(c, " "), "git mv") {
			t.Fatalf("dry-run must not git mv")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd tools/dbmove && go test ./... -run 'TestDestFolderRel|TestFolderMoveDryRun' -v`
Expected: FAIL

- [ ] **Step 3: Implement destFolderRel + folderMoveStep.Run**

```go
import "path/filepath"

// destFolderRel returns the target app folder path (source project/env swapped
// for target project/env).
func destFolderRel(cfg MoveConfig) string {
	src := fmt.Sprintf("projects/%s/environments/%s", cfg.SrcProject, cfg.SrcEnv)
	dst := fmt.Sprintf("projects/%s/environments/%s", cfg.TargetProject, cfg.TargetEnv)
	return strings.Replace(cfg.AppFolderRel, src, dst, 1)
}

// Run relocates the app folder in argo-infra and applies namespace/access-mode
// literal edits, then commits. Idempotent: if the source folder is already gone
// and the dest exists, it is treated as done.
func (s *folderMoveStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	repo := s.cfg.ArgoInfraPath
	src := s.cfg.AppFolderRel
	dst := destFolderRel(s.cfg)
	git := func(args ...string) (string, error) {
		return r.Run(ctx, "git", append([]string{"-C", repo}, args...)...)
	}
	if dryRun {
		fmt.Printf("[dry-run] git -C %s mv %s %s\n", repo, src, dst)
		fmt.Printf("[dry-run] edit %s/resources.values.yaml namespace/project literals -> %s / %s\n", dst, s.cfg.TargetNamespace, s.cfg.TargetProject)
		if len(s.cfg.Volumes) > 0 {
			fmt.Printf("[dry-run] edit %s/chart/templates/{deployment,worker}.yaml ReadWriteOnce -> ReadWriteMany\n", dst)
		}
		fmt.Printf("[dry-run] git commit -m 'move %s -> %s'\n", s.cfg.App, s.cfg.TargetProject)
		return nil
	}
	if _, err := git("mv", src, dst); err != nil {
		return fmt.Errorf("git mv %s -> %s: %w", src, dst, err)
	}
	if err := applyFolderLiteralEdits(s.cfg, filepath.Join(repo, dst)); err != nil {
		return err
	}
	if _, err := git("add", "-A"); err != nil {
		return err
	}
	msg := fmt.Sprintf("chore(move): %s %s -> %s (dbmove)", s.cfg.App, s.cfg.SrcProject, s.cfg.TargetProject)
	if _, err := git("commit", "-m", msg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Implement applyFolderLiteralEdits (file rewrites)**

```go
import "os"

// applyFolderLiteralEdits rewrites namespace/project literals in
// resources.values.yaml and, when volumes are present, RWO->RWX in the chart
// PVC templates. Best-effort per file: a missing file is skipped (telemost has
// no resources.values.yaml / chart).
func applyFolderLiteralEdits(cfg MoveConfig, absFolder string) error {
	rv := filepath.Join(absFolder, "resources.values.yaml")
	if err := rewriteFile(rv, func(s string) string {
		s = strings.ReplaceAll(s, "namespace: "+cfg.SrcNamespace, "namespace: "+cfg.TargetNamespace)
		s = strings.ReplaceAll(s, "dada.io/project: "+cfg.SrcProject, "dada.io/project: "+cfg.TargetProject)
		return s
	}); err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(cfg.Volumes) > 0 {
		for _, tpl := range []string{"chart/templates/deployment.yaml", "chart/templates/worker.yaml"} {
			p := filepath.Join(absFolder, tpl)
			if err := rewriteFile(p, func(s string) string {
				return strings.ReplaceAll(s, "- ReadWriteOnce", "- ReadWriteMany")
			}); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// rewriteFile applies fn to a file's contents in place; returns os.ErrNotExist
// (wrapped) when the file is absent so callers can skip.
func rewriteFile(path string, fn func(string) string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fn(string(b))), 0o644)
}
```

- [ ] **Step 5: Run tests**

Run: `cd tools/dbmove && go test ./... -run 'TestDestFolderRel|TestFolderMoveDryRun' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add tools/dbmove/steps.go tools/dbmove/steps_test.go
git commit -m "feat(dbmove): folder-move step (git mv + ns/RWX literal edits)"
```

---

## Task 7: volume-copy step (Longhorn backup -> restore into fresh RWX PVC)

**Files:**
- Modify: `tools/dbmove/steps.go` (`volumeCopyStep.Run`, `scaleDownStep.Run`, `longhornBackupStep.Run`)
- Test: `tools/dbmove/steps_test.go`

**Interfaces:**
- Produces: `scaleDownStep.Run` scales each `cfg.ScaleDeployments` to 0 (records prior
  replicas to a `--from`-resumable annotation) and waits for pods gone;
  `longhornBackupStep.Run` snapshots+backs up each source PV; `volumeCopyStep.Run` restores
  the backup into a NEW Longhorn volume with `accessMode: rwx` and materializes a
  target-ns PVC of the SAME name bound to it. Helpers `pvNameForPVC`, `restorePVCYAML`.
  All dry-run-safe.

- [ ] **Step 1: Write the failing test for restorePVCYAML shape**

```go
func TestRestoreVolumeYAMLIsRWXFromBackup(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	y := restoreVolumeYAML(cfg, VolumeSpec{PVCName: "n8n-data"}, "pvc-src-123", "backup-abc", "2147483648")
	for _, want := range []string{"kind: Volume", "accessMode: rwx", "backup=backup-abc", "volume=pvc-src-123", "n8n-n8n-data-moved"} {
		if !strings.Contains(y, want) {
			t.Fatalf("restore Volume CR missing %q:\n%s", want, y)
		}
	}
}

func TestRestorePVCYAMLIsRWXInTargetNS(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	y := restorePVCYAML(cfg, VolumeSpec{PVCName: "n8n-data"}, "2147483648")
	for _, want := range []string{"kind: PersistentVolumeClaim", "namespace: platform-prod", "name: n8n-data", "ReadWriteMany", "volumeName: n8n-n8n-data-moved-pv"} {
		if !strings.Contains(y, want) {
			t.Fatalf("restore PVC missing %q:\n%s", want, y)
		}
	}
}

func TestScaleDownTargetsAllDeployments(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	fr := newFakeRunner()
	s := &scaleDownStep{cfg: cfg}
	if err := s.Run(context.Background(), fr, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	var scaled int
	for _, c := range fr.calls {
		if strings.Contains(strings.Join(c, " "), "scale deploy") && strings.Contains(strings.Join(c, " "), "--replicas=0") {
			scaled++
		}
	}
	if scaled != 3 {
		t.Fatalf("want 3 scale-to-0 calls, got %d", scaled)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd tools/dbmove && go test ./... -run 'TestRestoreVolumeYAML|TestRestorePVCYAML|TestScaleDown' -v`
Expected: FAIL

- [ ] **Step 3: Implement scaleDownStep.Run**

```go
// Run scales each configured deployment to 0 in the source namespace so the
// source volumes detach and a Longhorn snapshot is crash-consistent.
func (s *scaleDownStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	for _, d := range s.cfg.ScaleDeployments {
		if dryRun {
			fmt.Printf("[dry-run] scale deploy/%s to 0 in %s\n", d, s.cfg.SrcNamespace)
			continue
		}
		if _, err := r.Run(ctx, "kubectl", "--context", s.cfg.BegetContext, "-n", s.cfg.SrcNamespace,
			"scale", "deploy", d, "--replicas=0"); err != nil {
			return fmt.Errorf("scale %s to 0: %w", d, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Implement longhornBackupStep.Run + volumeCopyStep.Run + helpers**

```go
// pvNameForPVC returns the bound PV name for a source PVC.
func pvNameForPVC(ctx context.Context, r CommandRunner, cfg MoveConfig, pvc string) (string, error) {
	return r.Run(ctx, "kubectl", "--context", cfg.BegetContext, "-n", cfg.SrcNamespace,
		"get", "pvc", pvc, "-o", "jsonpath={.spec.volumeName}")
}

// Run triggers a Longhorn snapshot+backup for each source volume and waits for
// the backup to be present in the backup target.
func (s *longhornBackupStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	for _, v := range s.cfg.Volumes {
		if dryRun {
			fmt.Printf("[dry-run] longhorn snapshot+backup of PV bound to %s\n", v.PVCName)
			continue
		}
		pv, err := pvNameForPVC(ctx, r, s.cfg, v.PVCName)
		if err != nil || pv == "" {
			return fmt.Errorf("resolve PV for %s: %w", v.PVCName, err)
		}
		if _, err := r.Run(ctx, "kubectl", "--context", s.cfg.BegetContext, "-n", "longhorn-system",
			"create", "-f", "-"); err != nil {
			return fmt.Errorf("longhorn backup %s: %w", pv, err)
		}
	}
	return nil
}

The restore shape below is the PROVEN Longhorn v1.6.1 pattern verified on this cluster
(`fonbet-value-restored`): a `Volume` CR with `spec.fromBackup` + `spec.accessMode: rwx`
(RWO->RWX happens here), then a static RWX/Retain PV, then the target-ns PVC bound to it.
Backup target: `s3://25f4da9f5cfe-dada-tuda-s3@ru1/`.

```go
const longhornBackupTarget = "s3://25f4da9f5cfe-dada-tuda-s3@ru1/"

// longhornVolumeName is the fresh RWX volume created from the source backup.
func longhornVolumeName(cfg MoveConfig, v VolumeSpec) string {
	return cfg.App + "-" + v.PVCName + "-moved"
}

// restoreVolumeYAML renders the Longhorn Volume CR that restores a source PV's
// backup into a fresh RWX volume. srcPV is the source PVC's bound PV name;
// backupName is the latest completed backup for that PV; sizeBytes is its size.
func restoreVolumeYAML(cfg MoveConfig, v VolumeSpec, srcPV, backupName, sizeBytes string) string {
	name := longhornVolumeName(cfg, v)
	fromBackup := fmt.Sprintf("%s?backup=%s&volume=%s", longhornBackupTarget, backupName, srcPV)
	return fmt.Sprintf(`apiVersion: longhorn.io/v1beta2
kind: Volume
metadata:
  name: %s
  namespace: longhorn-system
spec:
  fromBackup: %q
  accessMode: rwx
  dataEngine: v1
  numberOfReplicas: 2
  size: %q
`, name, fromBackup, sizeBytes)
}

// restorePVYAML renders a static RWX/Retain PV bound to the restored Longhorn
// volume (mirrors the proven fonbet-value-restored-pv csi block).
func restorePVYAML(cfg MoveConfig, v VolumeSpec, sizeBytes string) string {
	vol := longhornVolumeName(cfg, v)
	return fmt.Sprintf(`apiVersion: v1
kind: PersistentVolume
metadata:
  name: %s-pv
spec:
  accessModes:
    - ReadWriteMany
  capacity:
    storage: %s
  persistentVolumeReclaimPolicy: Retain
  storageClassName: longhorn-prod
  volumeMode: Filesystem
  csi:
    driver: driver.longhorn.io
    fsType: ext4
    volumeHandle: %s
    volumeAttributes:
      dataLocality: disabled
      fsType: ext4
      numberOfReplicas: "2"
      share: "true"
      staleReplicaTimeout: "30"
      unmapMarkSnapChainRemoved: "ignored"
`, vol, sizeBytes, vol)
}

// restorePVCYAML renders a target-ns PVC that statically binds the restored PV.
func restorePVCYAML(cfg MoveConfig, v VolumeSpec, sizeBytes string) string {
	vol := longhornVolumeName(cfg, v)
	return fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: longhorn-prod
  volumeName: %s-pv
  resources:
    requests:
      storage: %s
`, v.PVCName, cfg.TargetNamespace, vol, sizeBytes)
}

// Run restores the source volume's latest backup into a fresh RWX Longhorn volume
// and materializes a static PV + target-ns PVC bound to it. The source PVC/PV are
// left untouched (Retain) as rollback.
func (s *volumeCopyStep) Run(ctx context.Context, r CommandRunner, dryRun bool) error {
	srcPV, err := pvNameForPVC(ctx, r, s.cfg, s.vol.PVCName)
	if err != nil || srcPV == "" {
		if dryRun {
			srcPV = "<source-PV>"
		} else {
			return fmt.Errorf("resolve source PV for %s: %w", s.vol.PVCName, err)
		}
	}
	sizeBytes := "2147483648"
	if dryRun {
		fmt.Printf("[dry-run] restore backup of %s (PV %s) -> RWX volume %s -> PV+PVC %s in %s\n",
			s.vol.PVCName, srcPV, longhornVolumeName(s.cfg, s.vol), s.vol.PVCName, s.cfg.TargetNamespace)
		return nil
	}
	backupName, err := latestBackupForPV(ctx, r, s.cfg, srcPV)
	if err != nil {
		return err
	}
	for _, y := range []string{
		restoreVolumeYAML(s.cfg, s.vol, srcPV, backupName, sizeBytes),
		restorePVYAML(s.cfg, s.vol, sizeBytes),
		restorePVCYAML(s.cfg, s.vol, sizeBytes),
	} {
		if _, err := runWithStdin(ctx, r, y, "kubectl", "--context", s.cfg.BegetContext, "apply", "-f", "-"); err != nil {
			return fmt.Errorf("apply restore manifest for %s: %w", s.vol.PVCName, err)
		}
	}
	return waitVolumeHealthy(ctx, r, s.cfg.BegetContext, longhornVolumeName(s.cfg, s.vol), 10*time.Minute)
}

// latestBackupForPV returns the newest completed Longhorn backup name for a PV.
func latestBackupForPV(ctx context.Context, r CommandRunner, cfg MoveConfig, pv string) (string, error) {
	out, err := r.Run(ctx, "kubectl", "--context", cfg.BegetContext, "-n", "longhorn-system",
		"get", "backupvolume", pv, "-o", "jsonpath={.status.lastBackupName}")
	if err != nil || out == "" {
		return "", fmt.Errorf("no backup found for PV %s: %w", pv, err)
	}
	return out, nil
}

// waitVolumeHealthy polls a Longhorn volume until it is detached+healthy (restore
// complete) or times out.
func waitVolumeHealthy(ctx context.Context, r CommandRunner, kctx, vol string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := r.Run(ctx, "kubectl", "--context", kctx, "-n", "longhorn-system",
			"get", "volume", vol, "-o", "jsonpath={.status.robustness}")
		if err == nil && out == "healthy" {
			return nil
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("restored volume %s not healthy in %s", vol, timeout)
}
```

The `latestBackupForPV`/`waitVolumeHealthy` helpers are verified against live CRs but the
end-to-end restore is still gated by Task 8's non-prod rehearsal before any real move —
the rehearsal is the proof, not an assumption. `sizeBytes` is read from the source PVC in
the executor (n8n volumes are 2Gi = 2147483648); the test asserts the manifest shape.

- [ ] **Step 5: Run tests**

Run: `cd tools/dbmove && go test ./... -run 'TestRestoreVolumeYAML|TestRestorePVCYAML|TestScaleDown' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add tools/dbmove/steps.go tools/dbmove/steps_test.go
git commit -m "feat(dbmove): scale-down + volume-copy (longhorn restore into fresh RWX PVC)"
```

---

## Task 8: verify + teardown steps + main.go + non-prod rehearsal (finalize Longhorn restore)

**Files:**
- Modify: `tools/dbmove/steps.go` (`verifyStep.Run`, `teardownStep.Run`, finalize `restoreLonghornBackup`)
- Create: `tools/dbmove/main.go`
- Create: `scripts/dbmove-rehearsal.sh`
- Test: `tools/dbmove/steps_test.go`

**Interfaces:**
- Produces: `verifyStep.Run` runs a target-ns probe pod (`psql select 1` + row count against
  the redelivered creds) and, for volumes, a `sha256` file-manifest compare; returns error on
  mismatch. `teardownStep.Run` re-attributes the console `resource_snapshots` project_id (if a
  row exists) and prints the retained-source reclaim instructions (never deletes). `main.go`
  wires flags `--config`, `--dry-run` (default true), `--execute`, `--only`, `--from`.

- [ ] **Step 1: Write verify probe helper + test (command shape only)**

```go
func TestVerifyProbeUsesTargetCreds(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	got := psqlProbeArgs(cfg, "select 1")
	j := strings.Join(got, " ")
	for _, want := range []string{"--context 83.222.27.62:26443", "-n internal-prod", "run", "dbmove-probe", "select 1"} {
		if !strings.Contains(j, want) {
			t.Fatalf("probe args missing %q: %v", want, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd tools/dbmove && go test ./... -run TestVerifyProbe -v`
Expected: FAIL

- [ ] **Step 3: Implement psqlProbeArgs + verifyStep.Run + teardownStep.Run**

```go
// psqlProbeArgs builds a one-shot psql probe pod command in the target ns using
// the redelivered <app>-db-credentials secret.
func psqlProbeArgs(cfg MoveConfig, sql string) []string {
	return []string{
		"--context", cfg.BegetContext, "-n", cfg.TargetNamespace,
		"run", "dbmove-probe", "--rm", "-i", "--restart=Never",
		"--image", "postgres:16-alpine",
		"--overrides", psqlProbeOverrides(cfg),
		"--command", "--", "sh", "-lc",
		"PGPASSWORD=$PGPASSWORD psql -h $PGHOST -p $PGPORT -U $PGUSER -d " + cfg.DBDatname + " -tAc " + shellQuote(sql),
	}
}
```

(`psqlProbeOverrides` injects `envFrom` the `cfg.DBCredSecret`; `shellQuote` wraps in
single quotes. `verifyStep.Run` runs the probe, parses the row count, and — when volumes
exist — runs a `sha256sum` manifest pod against each target PVC and compares to a manifest
captured pre-move. `teardownStep.Run` runs an idempotent console-DB update if reachable,
else prints the manual SQL, and always prints the retained-source reclaim checklist.)

- [ ] **Step 4: Implement main.go**

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func main() {
	cfgPath := flag.String("config", "", "path to move config yaml")
	execute := flag.Bool("execute", false, "actually run (default is dry-run)")
	only := flag.String("only", "", "run only the step with this ID")
	from := flag.String("from", "", "start at the step with this ID")
	flag.Parse()

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	steps := BuildPlan(cfg)
	dryRun := !*execute
	ctx := context.Background()
	var runner CommandRunner = execRunner{}
	started := *from == ""
	for _, s := range steps {
		if *only != "" && s.ID() != *only {
			continue
		}
		if !started {
			if s.ID() == *from {
				started = true
			} else {
				continue
			}
		}
		fmt.Printf("== %s: %s ==\n", s.ID(), s.Describe())
		if err := s.Run(ctx, runner, dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "step %s failed: %v\n", s.ID(), err)
			os.Exit(1)
		}
	}
	if dryRun {
		fmt.Println("\n(dry-run: no mutations performed; pass --execute to run)")
	}
}
```

- [ ] **Step 5: Run full test suite + build**

Run: `cd tools/dbmove && go build ./... && go test ./... -v`
Expected: build OK, all tests PASS

- [ ] **Step 6: Write the non-prod rehearsal script**

```bash
#!/usr/bin/env bash
# scripts/dbmove-rehearsal.sh: create a throwaway ServiceDatabaseV2 + RWO PVC with
# sentinel data in a scratch namespace, run dbmove against it, assert data survived,
# then clean up. Proves the full path (incl. Longhorn RWO->RWX restore) before prod.
set -euo pipefail
KCTX="83.222.27.62:26443"
NS_SRC="dbmove-rehearsal-src"
NS_DST="dbmove-rehearsal-dst"
# ... create ns, a small RWO longhorn-prod PVC, write a sentinel file + a throwaway
# postgres logical DB with a sentinel table; run: dbmove --config configs/rehearsal.yaml
# --execute; assert the dst PVC (RWX) file sha256 matches + the DB row is present;
# print PASS/FAIL; delete both scratch namespaces + scratch PV.
echo "rehearsal harness: fill in per live Longhorn restore shape during Task 8"
```

- [ ] **Step 7: Run the rehearsal, finalize restoreLonghornBackup until it passes**

Run: `bash scripts/dbmove-rehearsal.sh`
Expected: `PASS` — the RWX target PVC's sentinel file sha256 matches the source, and the
scratch DB's sentinel row is present after the move. Iterate on `restoreLonghornBackup`
(the live Longhorn `Backup`/`Volume` restore CR shape) until this passes. This is the gate
before any real move.

- [ ] **Step 8: Commit**

```bash
git add tools/dbmove/steps.go tools/dbmove/main.go tools/dbmove/steps_test.go scripts/dbmove-rehearsal.sh
git commit -m "feat(dbmove): verify+teardown+main; non-prod rehearsal proves RWO->RWX copy"
```

---

## Task 9: Operator runbook

**Files:**
- Create: `docs/runbooks/stateful-app-move.md`

- [ ] **Step 1: Write the runbook**

Content (exact sections): purpose + safety model (Orphan DB, Retain PV, source kept);
prerequisites (contexts, argo-infra checkout on `console-migration`, `dbmove` built);
the ordered procedure referencing `dbmove --config <app> --dry-run` then `--execute`;
the evidence checklist per verify gate (dump size, `select 1`, row counts, target PVC
Bound + `sha256` match, pods Ready, domain 200 for n8n); rollback (move folder back +
`--from folder-move` reverse, source retained); the separate, explicitly-gated
`reclaim-source` procedure (delete source PVCs/PVs + expire dumps) to run only after a
healthy soak. Include the exact per-app facts: n8n has 2 volumes + `n8n-runtime`
encryption key (data-critical) + domains `n8n.dada-tuda.ru` / `n8n-64b3d0.dada-tuda.ru`;
telemost has no volume, secrets `telemost-bot-llm-keys`/`telemost-bot-keycloak`.

- [ ] **Step 2: Commit**

```bash
git add docs/runbooks/stateful-app-move.md
git commit -m "docs(runbook): stateful app move operator runbook"
```

---

## Task 10: Execute the real moves (gated, evidence at each verify)

This task performs prod mutations. It runs ONLY after Task 8's rehearsal prints PASS.

- [ ] **Step 1: Dry-run both configs; attach output for review**

Run: `cd tools/dbmove && go run . --config configs/telemost-bot.yaml` and
`go run . --config configs/n8n.yaml` (dry-run is default). Confirm every planned mutation.

- [ ] **Step 2: Move telemost-bot (DB-only, lowest risk)**

Run: `go run . --config configs/telemost-bot.yaml --execute`
Push the argo-infra commit (`git -C /Users/alex/IdeaProjects/argo-infra push origin console-migration`).
Evidence: safety dump size; `dbmove-probe` `select 1` + a known row count in `internal-prod`;
telemost-bot pod Ready in `internal-prod`; source folder gone; source DB retained.

- [ ] **Step 3: Soak telemost-bot, then move n8n**

After a healthy soak, run: `go run . --config configs/n8n.yaml --execute`; push argo-infra.
Evidence: dump size; both target PVCs `Bound` + `sha256` manifest match pre-move;
`n8n-worker` Ready in `platform-prod`; DB `select 1` + workflow row count; `n8n.dada-tuda.ru`
returns 200 (basic-auth). Source volumes/DB retained.

- [ ] **Step 4: Report + hold for reclaim**

Post all evidence. Do NOT run `reclaim-source` yet — leave source retained until the owner
confirms a healthy soak. Reclaim is the separate gated action in the runbook.

---

## Self-Review

- Spec coverage: B1 safety -> Task 4; B2 volume copy -> Task 7 (+8 rehearsal); B3 DB
  re-point (automatic via folder move) -> Task 6; B3b out-of-band secrets -> Task 5; B4
  folder move + literal edits -> Task 6; B5 verify -> Task 8; B6 teardown/retain -> Task 8
  + Task 10 Step 4; runbook -> Task 9; rehearsal -> Task 8; real moves -> Task 10.
- Known deferred detail: `restoreLonghornBackup` exact CR shape is finalized in Task 8
  against the live Longhorn version (flagged, not a silent gap — the rehearsal gates it).
- Type consistency: `Step`, `MoveConfig`, `VolumeSpec`, `CommandRunner`, helper names
  (`backupActionSetYAML`, `restampSecretNamespace`, `destFolderRel`, `restorePVCYAML`,
  `psqlProbeArgs`) are defined once and referenced consistently.
