package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"gopkg.in/yaml.v3"
)

// managedAgentPayload is the claim the console enqueues for both create and
// update: an agent is one whole CR, so a save re-states every field rather than
// patching one of them.
//
// The console never commits to argo-infra itself. It writes an operation row
// and this worker renders it, which is what keeps a prompt edit auditable
// (operation id lands in the CR labels and in the commit message) and what
// keeps a second writer from racing the first inside one repo clone.
type managedAgentPayload struct {
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	Prompt        string `json:"prompt"`
	PromptVersion string `json:"prompt_version"`
	ModelConfig   string `json:"model_config"`
	Runtime       string `json:"runtime"`
	Namespace     string `json:"namespace"`
	Tools         []struct {
		Name        string `json:"name"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Timeout     string `json:"timeout"`
		Protocol    string `json:"protocol"`
		Headers     []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
		AllowedHeaders []string `json:"allowed_headers"`
	} `json:"tools"`
	Env []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"env"`
}

// defaultAgentRuntimeNamespace is where the agent workloads live. It is a
// single shared namespace on purpose: kagent's controller watches it, and the
// agents of every project answer from it. The claim carries it so a future
// per-project runtime does not need a new operation action.
const defaultAgentRuntimeNamespace = "kagent"

// carriedOverAgentFields are the parts of the claim in git that a save may
// leave unsaid, and that the file must then keep.
type carriedOverAgentFields struct {
	DisplayName       string
	Description       string
	ModelConfig       string
	Runtime           string
	LangfuseProjectID string
	Memory            *renderer.ManagedAgentMemory
	Tools             []renderer.ManagedAgentToolRef
	Env               []renderer.ManagedAgentEnvVar
}

// carriedOverAgent returns what the claim in git already declares, so a save
// that says nothing about a field does not erase it.
//
// Two kinds of field need this. The ones the console does not know about at
// all: long-term memory -- an agent onboarded by hand can keep thirty days of
// notes, and dropping them because somebody fixed a typo in the prompt is a
// data loss nobody would connect to the edit -- and the Langfuse project, which
// when lost leaves the agent running while its "traces" link goes silent.
//
// And the ones the console knows but a caller may not restate. saveAgent over
// MCP with only a new prompt (2026-09-11, native.38) wrote a claim with no
// modelConfig, runtime or tools: the agent fell back to the default model
// config, whose tier was broken, every turn ran past the A2A timeout and the
// bot went silent for 38 hours. An edit to the prompt had unplugged the model.
// A field left empty on a save now means "keep what git has"; a caller that
// wants a field gone sets it to something else, not to nothing.
func carriedOverAgent(mgr *git.Manager, valuesPath, name string) (carriedOverAgentFields, error) {
	var out carriedOverAgentFields
	rv, err := loadResourcesValues(mgr, valuesPath)
	if err != nil {
		return out, err
	}
	existing, found, err := rv.ManifestOfKindNamed("ManagedAgent", name)
	if err != nil || !found {
		return out, err
	}
	var claim struct {
		Spec struct {
			DisplayName       string `yaml:"displayName"`
			Description       string `yaml:"description"`
			ModelConfig       string `yaml:"modelConfig"`
			Runtime           string `yaml:"runtime"`
			LangfuseProjectID string `yaml:"langfuseProjectId"`
			Memory            *struct {
				ModelConfig string `yaml:"modelConfig"`
				TTLDays     int    `yaml:"ttlDays"`
			} `yaml:"memory"`
			Tools []struct {
				Name        string `yaml:"name"`
				URL         string `yaml:"url"`
				Description string `yaml:"description"`
				Timeout     string `yaml:"timeout"`
				Protocol    string `yaml:"protocol"`
				Headers     []struct {
					Name  string `yaml:"name"`
					Value string `yaml:"value"`
				} `yaml:"headers"`
				AllowedHeaders []string `yaml:"allowedHeaders"`
			} `yaml:"tools"`
			Env []struct {
				Name  string `yaml:"name"`
				Value string `yaml:"value"`
			} `yaml:"env"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(existing), &claim); err != nil {
		return out, fmt.Errorf("parse existing agent %q: %w", name, err)
	}
	out.DisplayName = claim.Spec.DisplayName
	out.Description = claim.Spec.Description
	out.ModelConfig = claim.Spec.ModelConfig
	out.Runtime = claim.Spec.Runtime
	out.LangfuseProjectID = claim.Spec.LangfuseProjectID
	if claim.Spec.Memory != nil && claim.Spec.Memory.ModelConfig != "" {
		out.Memory = &renderer.ManagedAgentMemory{
			ModelConfig: claim.Spec.Memory.ModelConfig,
			TTLDays:     claim.Spec.Memory.TTLDays,
		}
	}
	for _, t := range claim.Spec.Tools {
		tool := renderer.ManagedAgentToolRef{
			Name:           t.Name,
			URL:            t.URL,
			Description:    t.Description,
			Timeout:        t.Timeout,
			Protocol:       t.Protocol,
			AllowedHeaders: t.AllowedHeaders,
		}
		for _, h := range t.Headers {
			tool.Headers = append(tool.Headers, renderer.ManagedAgentToolHeader{Name: h.Name, Value: h.Value})
		}
		out.Tools = append(out.Tools, tool)
	}
	for _, e := range claim.Spec.Env {
		out.Env = append(out.Env, renderer.ManagedAgentEnvVar{Name: e.Name, Value: e.Value})
	}
	return out, nil
}

// fillUnsaid completes a save with what git already holds for every field the
// save left empty. Memory and the Langfuse project are always taken from git,
// because no save can state them; the rest only when the save did not.
func fillUnsaid(spec *renderer.ManagedAgentSpec, carried carriedOverAgentFields) {
	spec.Memory = carried.Memory
	spec.LangfuseProjectID = carried.LangfuseProjectID
	if spec.DisplayName == "" {
		spec.DisplayName = carried.DisplayName
	}
	if spec.Description == "" {
		spec.Description = carried.Description
	}
	if spec.ModelConfig == "" {
		spec.ModelConfig = carried.ModelConfig
	}
	if spec.Runtime == "" {
		spec.Runtime = carried.Runtime
	}
	if len(spec.Tools) == 0 {
		spec.Tools = carried.Tools
	}
	if len(spec.Env) == 0 {
		spec.Env = carried.Env
	}
}

// doCreateAgent writes one ManagedAgent claim into the project's agent carrier
// app and commits it. Re-running it with the same name is an update: the CR is
// upserted by kind+name, so the console's "save" and "create" are the same
// operation and a retried operation cannot produce a second agent.
func (w *DBWatcher) doCreateAgent(ctx context.Context, op db.Operation) error {
	var p managedAgentPayload
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.Name == "" {
		return fmt.Errorf("create agent: name is required")
	}

	projectName, envName, envNamespace, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	spec := renderer.ManagedAgentSpec{
		Name:          p.Name,
		Namespace:     defaultIfEmpty(p.Namespace, defaultAgentRuntimeNamespace),
		ProjectSlug:   projectName,
		EnvSlug:       envName,
		OperationID:   op.ID.String(),
		DisplayName:   p.DisplayName,
		Description:   p.Description,
		Prompt:        p.Prompt,
		PromptVersion: p.PromptVersion,
		ModelConfig:   p.ModelConfig,
		Runtime:       p.Runtime,
	}
	for _, t := range p.Tools {
		tool := renderer.ManagedAgentToolRef{
			Name:           t.Name,
			URL:            t.URL,
			Description:    t.Description,
			Timeout:        t.Timeout,
			Protocol:       t.Protocol,
			AllowedHeaders: t.AllowedHeaders,
		}
		for _, h := range t.Headers {
			tool.Headers = append(tool.Headers, renderer.ManagedAgentToolHeader{Name: h.Name, Value: h.Value})
		}
		spec.Tools = append(spec.Tools, tool)
	}
	for _, e := range p.Env {
		spec.Env = append(spec.Env, renderer.ManagedAgentEnvVar{Name: e.Name, Value: e.Value})
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return err
	}
	carried, err := carriedOverAgent(mgr, renderer.ManagedAgentResourcesValuesGitPath(projectName, envName), p.Name)
	if err != nil {
		return err
	}
	fillUnsaid(&spec, carried)

	yaml, err := renderer.RenderManagedAgent(spec)
	if err != nil {
		return err
	}

	ownerApp := renderer.ManagedAgentOwnerApp(projectName)
	ownerFiles, err := w.ensureAppExists(mgr, projectName, envName, ownerApp, envNamespace, op.ID.String())
	if err != nil {
		return err
	}
	valuesPath := renderer.ManagedAgentResourcesValuesGitPath(projectName, envName)
	manifestFile, err := upsertManifestFile(mgr, valuesPath, yaml)
	if err != nil {
		return err
	}
	files := append(ownerFiles, manifestFile)

	commitMsg := fmt.Sprintf(
		"[DADA Console] Save agent %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\nOwner: %s\n",
		p.Name, op.ID, projectName, envName, ownerApp,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, valuesPath, files, commitMsg); err != nil {
		return err
	}

	summaryJSON, _ := json.Marshal(map[string]any{
		"name":           p.Name,
		"kind":           "ManagedAgent",
		"display_name":   spec.DisplayName,
		"namespace":      spec.Namespace,
		"prompt_version": p.PromptVersion,
		"model_config":   spec.ModelConfig,
		"status":         "Pending",
	})
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"ManagedAgent", p.Name, "Pending", summaryJSON, time.Now(),
	)
}

// doDeleteAgent removes an agent's claim from the carrier and drops its
// snapshot. Argo prunes the composed Agent, its prompt ConfigMap and its
// RemoteMCPServers once the claim leaves git.
//
// Two cases are deliberate, both learned on other resources:
//
//   - An agent that is not in the carrier fails the operation instead of
//     reporting a green delete. The agents that predate this path were written
//     into the runtime by hand, and this writer cannot remove them; answering
//     "deleted" would leave a live agent answering users behind a closed ticket.
//   - When the removed agent was the last one, the carrier app is removed whole,
//     because ArgoCD refuses to auto-sync a source that renders zero resources
//     and the claim would otherwise survive its own deletion forever.
func (w *DBWatcher) doDeleteAgent(ctx context.Context, op db.Operation) error {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.Name == "" {
		return fmt.Errorf("delete agent: name is required")
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

	valuesPath := renderer.ManagedAgentResourcesValuesGitPath(projectName, envName)
	manifestFile, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{
		{"ManagedAgent", p.Name},
	})
	if err != nil {
		return fmt.Errorf("remove manifests: %w", err)
	}
	if !changed {
		return fmt.Errorf(
			"delete agent %q: no ManagedAgent entry in %s — this agent was not created by the console and cannot be deleted from it",
			p.Name, valuesPath,
		)
	}

	commitMsg := fmt.Sprintf(
		"[DADA Console] Delete agent %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, op.ID, projectName, envName,
	)
	lastOne, err := manifestsFileIsEmpty(manifestFile)
	if err != nil {
		return err
	}
	var sha string
	if lastOne {
		sha, err = mgr.RemoveAndPush(
			standaloneOwnerAppPaths(projectName, envName, renderer.ManagedAgentOwnerApp(projectName)),
			commitMsg, w.cfg.BotName, w.cfg.BotEmail)
	} else {
		sha, err = mgr.CommitFilesAndPush([]git.FileChange{manifestFile}, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
	}
	if err != nil {
		return fmt.Errorf("git push (remove manifests): %w", err)
	}
	opID := op.ID
	_ = db.InsertCommit(ctx, w.pool, sha, mgr.RepoURL(), mgr.Branch(),
		valuesPath, commitMsg, w.cfg.BotName, w.cfg.BotEmail, &opID, "agent")

	if err := db.MarkCommitted(ctx, w.pool, op.ID, sha, valuesPath); err != nil {
		return err
	}
	_, _ = w.pool.Exec(ctx,
		`DELETE FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'ManagedAgent' AND name = $3`,
		op.ProjectID, op.EnvironmentID, p.Name,
	)
	return nil
}
