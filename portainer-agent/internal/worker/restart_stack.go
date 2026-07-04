package worker

import (
	"context"
	"fmt"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
	"github.com/dada-tuda/console/portainer-agent/internal/portainer"
	"github.com/rs/zerolog/log"
)

// doRestartStack recreates a compose app's containers from the CURRENT git
// compose — the VM-runtime "Restart" action (ADR-013 §8.3). It is a redeploy with
// PullImage:false / Prune:false, so it bounces the workload without pulling new
// images or touching volumes (the external PG volume is never affected). The
// stack must already exist; restarting a never-deployed app is an error.
func (w *VMWatcher) doRestartStack(ctx context.Context, op db.Operation) error {
	var p deployStackPayload
	if err := unmarshalPayload(op.Payload, &p); err != nil {
		return err
	}
	if p.AppName == "" {
		return fmt.Errorf("restart stack: app_name is required")
	}

	target, err := db.GetComposeDeployTarget(ctx, w.pool, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("resolve deploy target: %w", err)
	}

	stacks, err := w.portainer.ListStacks(ctx, target.EndpointID)
	if err != nil {
		return fmt.Errorf("list stacks: %w", err)
	}
	branchRef := fmt.Sprintf("refs/heads/%s", w.cfg.GitopsBranch)
	useAuth := w.cfg.GitopsToken != ""
	for _, st := range stacks {
		if st.Name == p.AppName {
			log.Info().Str("app", p.AppName).Int("stack", st.ID).Msg("restarting stack (recreate, no pull)")
			if err := w.portainer.RedeployStack(ctx, st.ID, target.EndpointID, portainer.RedeployStackRequest{
				PullImage:                false,
				Prune:                    false,
				RepositoryReferenceName:  branchRef,
				RepositoryAuthentication: useAuth,
				RepositoryUsername:       w.cfg.GitopsUsername,
				RepositoryPassword:       w.cfg.GitopsToken,
			}); err != nil {
				return fmt.Errorf("restart stack: %w", err)
			}
			w.syncStackSnapshots(ctx, op, target.EndpointID, p.AppName)
			return db.MarkReady(ctx, w.pool, op.ID)
		}
	}
	return fmt.Errorf("restart stack: %q is not deployed on endpoint %d", p.AppName, target.EndpointID)
}
