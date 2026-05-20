package worker

import (
	"context"
	"fmt"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
	tf "github.com/dada-tuda/console/portainer-agent/internal/terraform"
	"github.com/rs/zerolog/log"
)

type deleteAppServerPayload struct {
	AppServerName string `json:"app_server_name"`
}

func (w *VMWatcher) doDeleteAppServer(ctx context.Context, op db.Operation) error {
	var p deleteAppServerPayload
	if err := unmarshalPayload(op.Payload, &p); err != nil {
		return err
	}

	// ── 1. Fetch app_server record ──────────────────────────────────────────
	server, err := db.GetAppServerByName(ctx, w.pool, op.ProjectID, p.AppServerName)
	if err != nil {
		return fmt.Errorf("get app_server: %w", err)
	}

	if err := db.SetAppServerDeleting(ctx, w.pool, server.ID); err != nil {
		return fmt.Errorf("set deleting: %w", err)
	}
	_ = db.UpdateStatus(ctx, w.pool, op.ID, "DeletingStacks")

	// ── 2. Delete all Portainer stacks on this endpoint ─────────────────────
	if server.PortainerEndpointID != nil {
		endpointID := *server.PortainerEndpointID
		stacks, err := w.portainer.ListStacks(ctx, endpointID)
		if err != nil {
			log.Warn().Err(err).Int("endpoint", endpointID).Msg("list stacks failed — skipping stack deletion")
		} else {
			for _, stack := range stacks {
				log.Info().Int("stack", stack.ID).Str("name", stack.Name).Msg("deleting stack")
				if err := w.portainer.DeleteStack(ctx, stack.ID, endpointID); err != nil {
					log.Warn().Err(err).Int("stack", stack.ID).Msg("delete stack failed — continuing")
				}
			}
		}
		if err := w.portainer.DeleteEndpoint(ctx, endpointID); err != nil {
			log.Warn().Err(err).Int("endpoint", endpointID).Msg("delete endpoint failed — continuing")
		}
	}

	// ── 3. Terraform destroy ─────────────────────────────────────────────────
	_ = db.UpdateStatus(ctx, w.pool, op.ID, "DeletingVM")

	if server.TerraformWorkspace != nil {
		appServerUUID := server.ID.String()
		if err := w.tf.Init(ctx, appServerUUID); err != nil {
			log.Warn().Err(err).Msg("terraform init before destroy failed")
		} else {
			region := w.cfg.BegetRegion
			if err := w.tf.Destroy(ctx, appServerUUID, w.tfVars(p.AppServerName, region)); err != nil {
				log.Warn().Err(err).Msg("terraform destroy failed — marking deleted anyway")
			}
		}
		if err := tf.CleanWorkspace(w.tf.WorkspaceDir(appServerUUID)); err != nil {
			log.Warn().Err(err).Msg("clean workspace failed")
		}
	}

	// ── 4. Mark deleted ──────────────────────────────────────────────────────
	if err := db.SetAppServerDeleted(ctx, w.pool, server.ID); err != nil {
		return fmt.Errorf("set deleted: %w", err)
	}
	return db.MarkReady(ctx, w.pool, op.ID)
}
