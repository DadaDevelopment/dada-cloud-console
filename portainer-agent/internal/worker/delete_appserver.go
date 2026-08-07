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

// vpsRemover is the provider-level escape hatch used when Terraform cannot run.
type vpsRemover interface {
	RemoveVPS(ctx context.Context, id string) error
}

// destroyVM runs `terraform destroy` for one AppServer. The workspace is
// re-materialised first: it holds only the embedded .tf templates and lives on
// the agent pod's ephemeral disk, while the real state lives in Postgres. A pod
// restart between create and delete therefore wipes the directory, and an init
// against the missing directory used to abort the destroy (le-probe, 08-07 —
// the VM stayed billed while its console row was marked deleted).
func (w *VMWatcher) destroyVM(ctx context.Context, appServerUUID, serverName string) error {
	if err := tf.PrepareWorkspace(w.tf.WorkspaceDir(appServerUUID)); err != nil {
		return fmt.Errorf("prepare workspace: %w", err)
	}
	if err := w.tf.Init(ctx, appServerUUID); err != nil {
		return fmt.Errorf("terraform init: %w", err)
	}
	if err := w.tf.Destroy(ctx, appServerUUID, w.tfVars(serverName, w.cfg.BegetRegion)); err != nil {
		return fmt.Errorf("terraform destroy: %w", err)
	}
	return nil
}

// removeVMViaProvider deletes the machine straight through the Beget API.
func (w *VMWatcher) removeVMViaProvider(ctx context.Context, vmProviderID *string) error {
	if vmProviderID == nil || *vmProviderID == "" {
		return fmt.Errorf("no vm_provider_id recorded")
	}
	if w.beget == nil {
		return fmt.Errorf("beget client not configured")
	}
	return w.beget.RemoveVPS(ctx, *vmProviderID)
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
		if err := w.destroyVM(ctx, appServerUUID, p.AppServerName); err != nil {
			log.Warn().Err(err).Msg("terraform destroy unavailable — falling back to the Beget API")
			if fallbackErr := w.removeVMViaProvider(ctx, server.VMProviderID); fallbackErr != nil {
				return fmt.Errorf("destroy VM: %w (provider fallback: %v)", err, fallbackErr)
			}
			log.Info().Msg("VM removed via the Beget API")
		}
		if err := tf.CleanWorkspace(w.tf.WorkspaceDir(appServerUUID)); err != nil {
			log.Warn().Err(err).Msg("clean workspace failed")
		}
	}

	// ── 4. Mark deleted ──────────────────────────────────────────────────────
	// Only reached once the machine is provably gone: a silent "deleted" on a
	// live VM drops the console's only handle on a billed resource.
	if err := db.SetAppServerDeleted(ctx, w.pool, server.ID); err != nil {
		return fmt.Errorf("set deleted: %w", err)
	}
	return db.MarkReady(ctx, w.pool, op.ID)
}
