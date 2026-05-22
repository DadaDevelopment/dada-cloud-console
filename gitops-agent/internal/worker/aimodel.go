package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

// Profile catalog mirrored from backend/internal/profiles. Kept inline because
// the agent must not import the backend module. Update both when adding profiles.
type aiProfile struct {
	cpu, memory, gpu string
}

var aiProfiles = map[string]aiProfile{
	"cpu-small":  {cpu: "1", memory: "2Gi"},
	"cpu-medium": {cpu: "2", memory: "4Gi"},
	"gpu-t4":     {cpu: "4", memory: "16Gi", gpu: "1"},
	"gpu-a100":   {cpu: "8", memory: "32Gi", gpu: "1"},
}

func resolveProfile(name string) aiProfile {
	if p, ok := aiProfiles[name]; ok {
		return p
	}
	return aiProfiles["cpu-small"]
}

// stageFromEnv mirrors D15: env=dev -> development, anything else -> production.
func stageFromEnv(envName string) string {
	if strings.EqualFold(envName, "dev") || strings.EqualFold(envName, "development") {
		return "development"
	}
	return "production"
}

// payloadCreateAIModel mirrors backend/internal/models.CreateAIModelPayload.
type payloadCreateAIModel struct {
	Name            string `json:"name"`
	ModelType       string `json:"model_type"`
	Source          string `json:"source"`
	MLflowName      string `json:"mlflow_name,omitempty"`
	MLflowVersion   string `json:"mlflow_version,omitempty"`
	ArtifactURI     string `json:"artifact_uri,omitempty"`
	ContainerImage  string `json:"container_image,omitempty"`
	Profile         string `json:"profile"`
	AuthMode        string `json:"auth_mode"`
	AttachedAppName string `json:"attached_app_name,omitempty"`
	Version         string `json:"version,omitempty"`
}

func (w *DBWatcher) doCreateAIModel(ctx context.Context, op db.Operation) error {
	var p payloadCreateAIModel
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	projectName, envName, envNamespace, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	// Resolve artifactURI when source=mlflow. Worker is the only place that
	// touches MLflow at render time so the operation row can fail loudly if
	// MLflow is unreachable or the version is gone.
	artifactURI := p.ArtifactURI
	if p.Source == "mlflow" {
		uri, err := w.resolveMLflowArtifact(ctx, p.MLflowName, p.MLflowVersion)
		if err != nil {
			return fmt.Errorf("resolve mlflow artifact: %w", err)
		}
		artifactURI = uri
	}

	prof := resolveProfile(p.Profile)
	apiKeySecretRef := fmt.Sprintf("aimodel-%s-apikey", p.Name)

	yaml, err := renderer.RenderAIModel(renderer.AIModelSpec{
		Name:            p.Name,
		Namespace:       envNamespace,
		ProjectSlug:     projectName,
		EnvSlug:         envName,
		OperationID:     op.ID.String(),
		ModelType:       p.ModelType,
		Version:         defaultIfEmpty(p.Version, "v1"),
		Stage:           stageFromEnv(envName),
		ArtifactURI:     artifactURI,
		ContainerImage:  p.ContainerImage,
		ProfileCPU:      prof.cpu,
		ProfileMemory:   prof.memory,
		ProfileGPU:      prof.gpu,
		APIKeySecretRef: apiKeySecretRef,
		AttachedAppName: p.AttachedAppName,
	})
	if err != nil {
		return err
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	gitPath := renderer.AIModelGitPath(projectName, envName, p.Name)
	commitMsg := fmt.Sprintf(
		"[DADA Console] Create AIModel %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\nProfile: %s\n",
		p.Name, op.ID, projectName, envName, p.Profile,
	)
	if err := w.commitAndRecord(ctx, op, mgr, gitPath, yaml, commitMsg); err != nil {
		return err
	}

	// Issue API key when auth_mode=apikey, park plaintext for one-shot reveal.
	if p.AuthMode == "apikey" {
		if err := w.issueAIModelAPIKey(ctx, op, p.Name); err != nil {
			return fmt.Errorf("issue api key: %w", err)
		}
	}

	// Snapshot row so the read APIs / quotas counter pick it up immediately.
	summary, _ := json.Marshal(map[string]any{
		"profile":         p.Profile,
		"model_type":      p.ModelType,
		"version":         p.Version,
		"stage":           stageFromEnv(envName),
		"artifact_uri":    artifactURI,
		"auth_mode":       p.AuthMode,
		"attached_app":    p.AttachedAppName,
		"status":          "Pending",
	})
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID, "AIModel", p.Name, "Pending", summary, time.Now(),
	)
}

func (w *DBWatcher) doUpdateAIModelArtifact(ctx context.Context, op db.Operation) error {
	var p struct {
		Name          string `json:"name"`
		ArtifactURI   string `json:"artifact_uri,omitempty"`
		MLflowName    string `json:"mlflow_name,omitempty"`
		MLflowVersion string `json:"mlflow_version,omitempty"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	uri := p.ArtifactURI
	if uri == "" {
		resolved, err := w.resolveMLflowArtifact(ctx, p.MLflowName, p.MLflowVersion)
		if err != nil {
			return fmt.Errorf("resolve mlflow artifact: %w", err)
		}
		uri = resolved
	}
	return w.rerenderAIModel(ctx, op, p.Name, func(s map[string]any) {
		s["artifact_uri"] = uri
		if p.MLflowName != "" {
			s["mlflow_name"] = p.MLflowName
			s["mlflow_version"] = p.MLflowVersion
		}
	})
}

func (w *DBWatcher) doSetCanaryTraffic(ctx context.Context, op db.Operation) error {
	var p struct {
		Name           string `json:"name"`
		TrafficPercent int    `json:"traffic_percent"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	return w.rerenderAIModel(ctx, op, p.Name, func(s map[string]any) {
		s["canary_percent"] = p.TrafficPercent
	})
}

func (w *DBWatcher) doPromoteAIModel(ctx context.Context, op db.Operation) error {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	return w.rerenderAIModel(ctx, op, p.Name, func(s map[string]any) {
		delete(s, "canary_percent")
	})
}

func (w *DBWatcher) doPinAIModelMlflowVersion(ctx context.Context, op db.Operation) error {
	var p struct {
		Name          string `json:"name"`
		MLflowName    string `json:"mlflow_name"`
		MLflowVersion string `json:"mlflow_version"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	uri, err := w.resolveMLflowArtifact(ctx, p.MLflowName, p.MLflowVersion)
	if err != nil {
		return fmt.Errorf("resolve mlflow artifact: %w", err)
	}
	return w.rerenderAIModel(ctx, op, p.Name, func(s map[string]any) {
		s["artifact_uri"] = uri
		s["mlflow_name"] = p.MLflowName
		s["mlflow_version"] = p.MLflowVersion
	})
}

func (w *DBWatcher) doDeleteAIModel(ctx context.Context, op db.Operation) error {
	var p struct {
		Name  string `json:"name"`
		Force bool   `json:"force,omitempty"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	projectName, envName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}
	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	paths := []string{
		renderer.AIModelGitPath(projectName, envName, p.Name),
		renderer.AIModelPublicApiGitPath(projectName, envName, p.Name),
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Delete AIModel %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, op.ID, projectName, envName,
	)
	sha, err := mgr.RemoveAndPush(paths, commitMsg, w.cfg.BotName, w.cfg.BotEmail)
	if err != nil {
		return fmt.Errorf("git remove: %w", err)
	}
	if sha != "" {
		opID := op.ID
		_ = db.InsertCommit(ctx, w.pool, sha, mgr.RepoURL(), mgr.Branch(),
			paths[0], commitMsg, w.cfg.BotName, w.cfg.BotEmail, &opID, "agent")
	}
	if err := db.MarkCommitted(ctx, w.pool, op.ID, sha, paths[0]); err != nil {
		return err
	}
	// Revoke active API key (D17).
	_, _ = w.pool.Exec(ctx,
		`UPDATE aimodel_api_keys SET revoked_at = NOW()
		 WHERE project_id = $1 AND environment_id = $2 AND aimodel_name = $3 AND revoked_at IS NULL`,
		op.ProjectID, op.EnvironmentID, p.Name,
	)
	// Drop snapshot so quota recalculation reflects deletion.
	_, _ = w.pool.Exec(ctx,
		`DELETE FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'AIModel' AND name = $3`,
		op.ProjectID, op.EnvironmentID, p.Name,
	)
	return nil
}

// rerenderAIModel pulls the current snapshot, applies a mutation, re-renders
// the AIModel CR with the merged inputs, and commits. This is the single path
// for any in-place AIModel update so we never lose fields between operations.
func (w *DBWatcher) rerenderAIModel(ctx context.Context, op db.Operation, name string, mutate func(map[string]any)) error {
	projectName, envName, envNamespace, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}
	var summaryRaw []byte
	if err := w.pool.QueryRow(ctx, `
		SELECT summary_json FROM resource_snapshots
		WHERE project_id=$1 AND environment_id=$2 AND kind='AIModel' AND name=$3
	`, op.ProjectID, op.EnvironmentID, name).Scan(&summaryRaw); err != nil {
		return fmt.Errorf("loading aimodel snapshot: %w", err)
	}
	var summary map[string]any
	if err := json.Unmarshal(summaryRaw, &summary); err != nil {
		return fmt.Errorf("parse summary: %w", err)
	}
	mutate(summary)

	prof := resolveProfile(asString(summary["profile"]))
	var canary *int
	if v, ok := summary["canary_percent"].(float64); ok {
		c := int(v)
		canary = &c
	}
	yaml, err := renderer.RenderAIModel(renderer.AIModelSpec{
		Name:            name,
		Namespace:       envNamespace,
		ProjectSlug:     projectName,
		EnvSlug:         envName,
		OperationID:     op.ID.String(),
		ModelType:       asString(summary["model_type"]),
		Version:         defaultIfEmpty(asString(summary["version"]), "v1"),
		Stage:           defaultIfEmpty(asString(summary["stage"]), stageFromEnv(envName)),
		ArtifactURI:     asString(summary["artifact_uri"]),
		ContainerImage:  asString(summary["container_image"]),
		ProfileCPU:      prof.cpu,
		ProfileMemory:   prof.memory,
		ProfileGPU:      prof.gpu,
		CanaryPercent:   canary,
		APIKeySecretRef: fmt.Sprintf("aimodel-%s-apikey", name),
		AttachedAppName: asString(summary["attached_app"]),
	})
	if err != nil {
		return err
	}
	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	gitPath := renderer.AIModelGitPath(projectName, envName, name)
	commitMsg := fmt.Sprintf(
		"[DADA Console] %s AIModel %s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		op.Action, name, op.ID, projectName, envName,
	)
	if err := w.commitAndRecord(ctx, op, mgr, gitPath, yaml, commitMsg); err != nil {
		return err
	}
	updated, _ := json.Marshal(summary)
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID, "AIModel", name, "Pending", updated, time.Now(),
	)
}

// issueAIModelAPIKey generates a fresh key, parks the plaintext in
// aimodel_api_key_reveals (15-min TTL, S6), and stores the argon2id hash.
func (w *DBWatcher) issueAIModelAPIKey(ctx context.Context, op db.Operation, modelName string) error {
	plain, prefix, err := generateAPIKey()
	if err != nil {
		return err
	}
	hash := argon2.IDKey(plain, []byte(modelName), 1, 64*1024, 4, 32)

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var apiKeyID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO aimodel_api_keys (project_id, environment_id, aimodel_name, key_prefix, key_hash)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, op.ProjectID, op.EnvironmentID, modelName, prefix, hash).Scan(&apiKeyID); err != nil {
		return fmt.Errorf("insert api key: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO aimodel_api_key_reveals (operation_id, api_key_id, plaintext, expires_at)
		VALUES ($1, $2, $3, NOW() + INTERVAL '15 minutes')
	`, op.ID, apiKeyID, plain); err != nil {
		return fmt.Errorf("insert reveal: %w", err)
	}
	return tx.Commit(ctx)
}

func generateAPIKey() ([]byte, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", err
	}
	encoded := hex.EncodeToString(raw)
	plain := []byte("aim_" + encoded)
	prefix := string(plain[:12]) // "aim_" + 8 hex chars
	return plain, prefix, nil
}

// resolveMLflowArtifact is a placeholder: the agent does not yet have an
// MLflow client. Backend phase 2 prepared the proxy; once phase 6 wires the
// inference proxy we will reuse the same client here. Until then, MLflow-
// sourced operations fail loudly so they can be retried after the client lands.
func (w *DBWatcher) resolveMLflowArtifact(ctx context.Context, name, version string) (string, error) {
	if name == "" || version == "" {
		return "", fmt.Errorf("mlflow name+version required")
	}
	return "", fmt.Errorf("mlflow resolution not yet wired in agent (phase 6)")
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
