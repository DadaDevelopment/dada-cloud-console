package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
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
		Name           string   `json:"name"`
		URL            string   `json:"url"`
		Description    string   `json:"description"`
		Timeout        string   `json:"timeout"`
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
		spec.Tools = append(spec.Tools, renderer.ManagedAgentToolRef{
			Name:           t.Name,
			URL:            t.URL,
			Description:    t.Description,
			Timeout:        t.Timeout,
			AllowedHeaders: t.AllowedHeaders,
		})
	}
	for _, e := range p.Env {
		spec.Env = append(spec.Env, renderer.ManagedAgentEnvVar{Name: e.Name, Value: e.Value})
	}

	yaml, err := renderer.RenderManagedAgent(spec)
	if err != nil {
		return err
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
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
		"display_name":   p.DisplayName,
		"namespace":      spec.Namespace,
		"prompt_version": p.PromptVersion,
		"model_config":   p.ModelConfig,
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
