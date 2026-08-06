package worker

import (
	"context"
	"fmt"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
	"github.com/dada-tuda/console/portainer-agent/internal/portainer"
	"github.com/rs/zerolog/log"
)

// deployStackPayload.AppName carries the Portainer STACK name — under the
// aggregated-per-VM model that is the per-environment stack ("{projectSlug}-{envSlug}"),
// not an individual app. gitops-agent's renderEnvAggregate enqueues it.
type deployStackPayload struct {
	AppName string `json:"app_name"`
	// Volumes are the named Docker volumes the rendered compose file pins
	// `external: true`. gitops-agent knows them because it rendered them; this
	// worker creates the missing ones before deploying, because an external
	// volume that does not exist yet fails the deploy outright.
	Volumes []string `json:"volumes,omitempty"`
}

// envComposeGitPath builds the in-repo path to an environment's AGGREGATE
// compose.yaml. It must match gitops-agent's renderer.EnvComposeGitPath (cluster
// prefix is fixed to beget-prod, as elsewhere in the platform).
func envComposeGitPath(projectSlug, envSlug string) string {
	return fmt.Sprintf("clusters/beget-prod/projects/%s/environments/%s/compose.yaml",
		projectSlug, envSlug)
}

// doDeployStack deploys (or redeploys) a compose app as a Portainer stack on the
// environment's AppServer endpoint. Portainer pulls compose.yaml from git; the
// sibling .env is auto-loaded by docker compose from the same directory.
func (w *VMWatcher) doDeployStack(ctx context.Context, op db.Operation) error {
	var p deployStackPayload
	if err := unmarshalPayload(op.Payload, &p); err != nil {
		return err
	}
	if p.AppName == "" {
		return fmt.Errorf("deploy stack: app_name is required")
	}

	target, err := db.GetComposeDeployTarget(ctx, w.pool, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("resolve deploy target: %w", err)
	}

	// Create the stack's named volumes before deploying. Idempotent: Docker
	// returns an existing volume untouched, so this never disturbs live data —
	// which is why it can run on every deploy rather than only on the first.
	// Failing here is fatal on purpose: deploying a stack whose external volume
	// is missing produces a compose error about an unknown volume, which reads
	// as a broken platform rather than as the missing volume it is.
	for _, vol := range p.Volumes {
		if vol == "" {
			continue
		}
		if err := w.portainer.EnsureVolume(ctx, target.EndpointID, vol); err != nil {
			return fmt.Errorf("ensure volume %q: %w", vol, err)
		}
	}

	composePath := envComposeGitPath(target.ProjectSlug, target.EnvSlug)
	branchRef := fmt.Sprintf("refs/heads/%s", w.cfg.GitopsBranch)
	useAuth := w.cfg.GitopsToken != ""

	// Redeploy if a stack with this name already exists on the endpoint.
	stacks, err := w.portainer.ListStacks(ctx, target.EndpointID)
	if err != nil {
		return fmt.Errorf("list stacks: %w", err)
	}
	for _, st := range stacks {
		if st.Name == p.AppName {
			log.Info().Str("app", p.AppName).Int("stack", st.ID).Msg("redeploying existing stack")
			if err := w.portainer.RedeployStack(ctx, st.ID, target.EndpointID, portainer.RedeployStackRequest{
				PullImage:                true,
				Prune:                    false,
				RepositoryReferenceName:  branchRef,
				RepositoryAuthentication: useAuth,
				RepositoryUsername:       w.cfg.GitopsUsername,
				RepositoryPassword:       w.cfg.GitopsToken,
			}); err != nil {
				return fmt.Errorf("redeploy stack: %w", err)
			}
			w.syncStackSnapshots(ctx, op, target.EndpointID, p.AppName)
			return db.MarkReady(ctx, w.pool, op.ID)
		}
	}

	log.Info().Str("app", p.AppName).Int("endpoint", target.EndpointID).Str("compose", composePath).Msg("creating stack from git")
	if _, err := w.portainer.CreateStackFromGit(ctx, target.EndpointID, portainer.CreateStackRequest{
		Name:                     p.AppName,
		RepositoryURL:            w.cfg.GitopsRepoURL,
		RepositoryReferenceName:  branchRef,
		ComposeFile:              composePath,
		RepositoryAuthentication: useAuth,
		RepositoryUsername:       w.cfg.GitopsUsername,
		RepositoryPassword:       w.cfg.GitopsToken,
		Env:                      []any{},
	}); err != nil {
		return fmt.Errorf("create stack from git: %w", err)
	}
	w.syncStackSnapshots(ctx, op, target.EndpointID, p.AppName)
	return db.MarkReady(ctx, w.pool, op.ID)
}
