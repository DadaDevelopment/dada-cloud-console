package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// redeployConfirmWindow bounds how long we keep asking Portainer whether a
// redeploy we lost the answer to actually landed.
const redeployConfirmWindow = 5 * time.Minute

// redeployLanded answers the only question a failed redeploy call leaves open:
// did the deploy happen anyway? A transport failure is the absence of a verdict,
// not a verdict of failure — Portainer can finish the work long after our client
// hangs up. So for transport failures only, re-read the stack and look for the
// server's own evidence that it redeployed: UpdateDate advancing past what we
// saw before the call, or the git ConfigHash moving. An HTTP error status is a
// real verdict and is never second-guessed here.
//
// Recorded because it bit us: the fin-core/findata redeploy of 2026-08-12 timed
// out client-side and was written down as Failed while all four containers were
// already running the new images from the new commit.
func (w *VMWatcher) redeployLanded(ctx context.Context, before portainer.Stack, cause error) bool {
	var transport *portainer.TransportError
	if !errors.As(cause, &transport) {
		return false
	}
	deadline := time.Now().Add(redeployConfirmWindow)
	for {
		after, err := w.portainer.GetStack(ctx, before.ID)
		if err == nil && stackAdvanced(before, *after) {
			log.Warn().Int("stack", before.ID).Err(cause).
				Msg("redeploy call failed but the stack advanced — treating as delivered")
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(10 * time.Second):
		}
	}
}

// stackAdvanced reports whether Portainer redeployed the stack since the
// snapshot taken before the call.
func stackAdvanced(before, after portainer.Stack) bool {
	if after.UpdateDate > before.UpdateDate {
		return true
	}
	if before.GitConfig != nil && after.GitConfig != nil {
		return after.GitConfig.ConfigHash != "" && after.GitConfig.ConfigHash != before.GitConfig.ConfigHash
	}
	return false
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
				if !w.redeployLanded(ctx, st, err) {
					return fmt.Errorf("redeploy stack: %w", err)
				}
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
