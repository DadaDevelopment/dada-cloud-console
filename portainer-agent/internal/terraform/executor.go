package terraform

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-exec/tfexec"
)

// Executor wraps terraform-exec for init / apply / destroy / output.
type Executor struct {
	terraformBin  string
	pgConnStr     string
	workspaceBase string
}

// NewExecutor creates an Executor.
func NewExecutor(terraformBin, pgConnStr, workspaceBase string) *Executor {
	return &Executor{
		terraformBin:  terraformBin,
		pgConnStr:     pgConnStr,
		workspaceBase: workspaceBase,
	}
}

// WorkspaceDir returns the filesystem path for a given appServerID workspace.
func (e *Executor) WorkspaceDir(appServerID string) string {
	return filepath.Join(e.workspaceBase, appServerID)
}

// schemaName derives the Postgres schema name for state isolation.
// Hyphens are replaced with underscores (Postgres schema names cannot contain hyphens).
func schemaName(appServerID string) string {
	return "tfstate_" + strings.ReplaceAll(appServerID, "-", "_")
}

func (e *Executor) newTF(workspaceDir string) (*tfexec.Terraform, error) {
	return tfexec.NewTerraform(workspaceDir, e.terraformBin)
}

// Init runs terraform init with the PG backend configured for this appServerID.
func (e *Executor) Init(ctx context.Context, appServerID string) error {
	dir := e.WorkspaceDir(appServerID)
	tf, err := e.newTF(dir)
	if err != nil {
		return err
	}
	return tf.Init(ctx,
		tfexec.Backend(true),
		tfexec.BackendConfig("conn_str="+e.pgConnStr),
		tfexec.BackendConfig("schema_name="+schemaName(appServerID)),
		tfexec.Upgrade(false),
	)
}

// Apply runs terraform apply and returns outputs as a string map.
// vars: map of variable name → value (e.g. "server_name" → "client-prod-1").
func (e *Executor) Apply(ctx context.Context, appServerID string, vars map[string]string) (map[string]string, error) {
	dir := e.WorkspaceDir(appServerID)
	tf, err := e.newTF(dir)
	if err != nil {
		return nil, err
	}
	applyOpts := []tfexec.ApplyOption{tfexec.Lock(true)}
	for k, v := range vars {
		applyOpts = append(applyOpts, tfexec.Var(fmt.Sprintf("%s=%s", k, v)))
	}
	if err := tf.Apply(ctx, applyOpts...); err != nil {
		return nil, fmt.Errorf("tf apply: %w", err)
	}
	return e.outputs(ctx, dir)
}

// Destroy runs terraform destroy.
func (e *Executor) Destroy(ctx context.Context, appServerID string, vars map[string]string) error {
	dir := e.WorkspaceDir(appServerID)
	tf, err := e.newTF(dir)
	if err != nil {
		return err
	}
	opts := []tfexec.DestroyOption{}
	for k, v := range vars {
		opts = append(opts, tfexec.Var(fmt.Sprintf("%s=%s", k, v)))
	}
	if err := tf.Destroy(ctx, opts...); err != nil {
		return fmt.Errorf("tf destroy: %w", err)
	}
	return nil
}

func (e *Executor) outputs(ctx context.Context, workspaceDir string) (map[string]string, error) {
	tf, err := e.newTF(workspaceDir)
	if err != nil {
		return nil, err
	}
	out, err := tf.Output(ctx)
	if err != nil {
		return nil, fmt.Errorf("tf output: %w", err)
	}
	result := make(map[string]string, len(out))
	for k, v := range out {
		result[k] = strings.Trim(string(v.Value), `"`)
	}
	return result, nil
}
