package worker

import (
	"context"
	"fmt"

	"github.com/dada-tuda/console/portainer-agent/internal/portainer"
	"github.com/rs/zerolog/log"
)

// Fleet config delivery (see tasks/vm-fleet-config-plan.md). Every enrolled VM
// joins a single dynamic edge group; a git-sourced edge stack (observability
// sidecars) is reconciled onto the whole group by Portainer — so a config fix is
// one git edit + redeploy, not a per-VM SSH. These names match the live
// pre-provisioned group/tag (dada-managed / dada-vms, both id 1).
const (
	fleetTag         = "dada-managed"
	fleetGroup       = "dada-vms"
	fleetStackName   = "vm-observability"
	fleetComposePath = "clusters/beget-prod/fleet/vm-observability/docker-compose.yml"
)

// joinFleet tags an enrolled endpoint into the fleet edge group and ensures the
// fleet edge stack exists, so the VM receives the git-defined node config with no
// per-VM manual op. Best-effort: a failure here must NOT fail enrollment (the VM
// is already Ready); it only means the fleet config lags until the next reconcile.
func (w *VMWatcher) joinFleet(ctx context.Context, endpointID int) {
	tagID, err := w.portainer.EnsureTag(ctx, fleetTag)
	if err != nil {
		log.Warn().Err(err).Msg("fleet: ensure tag failed (non-fatal)")
		return
	}
	if err := w.portainer.TagEndpoint(ctx, endpointID, tagID); err != nil {
		log.Warn().Err(err).Int("endpoint", endpointID).Msg("fleet: tag endpoint failed (non-fatal)")
		return
	}
	grp, err := w.portainer.EnsureEdgeGroup(ctx, fleetGroup, tagID)
	if err != nil {
		log.Warn().Err(err).Msg("fleet: ensure edge group failed (non-fatal)")
		return
	}
	if _, err := w.portainer.EnsureEdgeStackFromGit(ctx, portainer.CreateEdgeStackGitRequest{
		Name:                     fleetStackName,
		RepositoryURL:            w.cfg.GitopsRepoURL,
		RepositoryReferenceName:  fmt.Sprintf("refs/heads/%s", w.cfg.GitopsBranch),
		FilePathInRepository:     fleetComposePath,
		EdgeGroups:               []int{grp.ID},
		DeploymentType:           0, // 0 = compose
		RepositoryAuthentication: w.cfg.GitopsToken != "",
		RepositoryUsername:       w.cfg.GitopsUsername,
		RepositoryPassword:       w.cfg.GitopsToken,
	}); err != nil {
		log.Warn().Err(err).Msg("fleet: ensure edge stack failed (non-fatal)")
		return
	}
	log.Info().Int("endpoint", endpointID).Str("group", fleetGroup).Str("stack", fleetStackName).
		Msg("fleet: endpoint joined group + edge stack ensured")
}
