package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
	dadash "github.com/dada-tuda/console/portainer-agent/internal/ssh"
	tf "github.com/dada-tuda/console/portainer-agent/internal/terraform"
	"github.com/dada-tuda/console/portainer-agent/internal/portainer"
	"github.com/rs/zerolog/log"
)

type createAppServerPayload struct {
	Name       string `json:"name"`
	Mode       string `json:"mode"` // "terraform" (default) | "manual"
	Flavor     string `json:"flavor"`
	OSImage    string `json:"os_image"`
	Region     string `json:"region"`
	SSHKeyName string `json:"ssh_key_name"`

	// Manual-mode fields.
	VMIP          string `json:"vm_ip"`
	SSHUser       string `json:"ssh_user"`
	SSHPort       int    `json:"ssh_port"`
	SSHPrivateKey string `json:"ssh_private_key"`
}

func (w *VMWatcher) doCreateAppServer(ctx context.Context, op db.Operation) error {
	var p createAppServerPayload
	if err := unmarshalPayload(op.Payload, &p); err != nil {
		return err
	}

	if p.Mode == "manual" {
		return w.doCreateManualAppServer(ctx, op, p)
	}

	region := p.Region
	if region == "" {
		region = w.cfg.BegetRegion
	}

	// ── 1. Register Portainer edge endpoint ─────────────────────────────────
	_ = db.UpdateStatus(ctx, w.pool, op.ID, "ProvisioningVM")
	log.Info().Str("server", p.Name).Msg("creating Portainer edge endpoint")

	ep, err := w.portainer.CreateEdgeEndpoint(
		ctx, p.Name,
		w.cfg.PortainerEdgeURL,
		portainerTunnelAddr(w.cfg.PortainerEdgeURL),
	)
	if err != nil {
		return fmt.Errorf("create edge endpoint: %w", err)
	}
	log.Info().Int("endpoint_id", ep.ID).Str("edge_id", ep.EdgeID).Msg("edge endpoint created")

	// ── 2. Create app_servers DB row (gives us the stable UUID for workspace path) ──
	serverID, err := db.CreateAppServer(ctx, w.pool, op.ProjectID, p.Name, "")
	if err != nil {
		return fmt.Errorf("create app_server row: %w", err)
	}
	log.Info().Str("server_id", serverID.String()).Msg("app_servers row created")

	// ── 3. Prepare Terraform workspace (keyed by stable serverID) ────────────
	appServerUUID := serverID.String()
	workspaceDir := w.tf.WorkspaceDir(appServerUUID)
	if err := tf.PrepareWorkspace(workspaceDir); err != nil {
		_ = db.SetAppServerFailed(ctx, w.pool, serverID, err.Error())
		return fmt.Errorf("prepare workspace: %w", err)
	}
	if err := db.SetAppServerWorkspace(ctx, w.pool, serverID, workspaceDir); err != nil {
		return fmt.Errorf("set workspace path: %w", err)
	}

	// ── 4. Terraform init + apply ────────────────────────────────────────────
	if err := w.tf.Init(ctx, appServerUUID); err != nil {
		_ = db.SetAppServerFailed(ctx, w.pool, serverID, err.Error())
		return fmt.Errorf("terraform init: %w", err)
	}

	outputs, err := w.tf.Apply(ctx, appServerUUID, w.tfVars(p.Name, region))
	if err != nil {
		_ = db.SetAppServerFailed(ctx, w.pool, serverID, err.Error())
		return fmt.Errorf("terraform apply: %w", err)
	}
	vmIP := outputs["vm_ip"]
	vmID := outputs["vm_id"]
	log.Info().Str("vm_ip", vmIP).Str("vm_id", vmID).Msg("terraform apply complete")

	if err := db.SetAppServerProvisioned(ctx, w.pool, serverID, vmIP, vmID); err != nil {
		return fmt.Errorf("set app_server provisioned: %w", err)
	}

	// ── 5. SSH bootstrap ─────────────────────────────────────────────────────
	log.Info().Str("vm_ip", vmIP).Msg("running SSH bootstrap")
	bootstrapCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	params := w.bootstrapParams(p.Name, ep.EdgeKey, ep.EdgeID)
	if err := dadash.RunBootstrap(bootstrapCtx, vmIP, "root", w.cfg.AgentSSHPrivateKey, params); err != nil {
		_ = db.SetAppServerFailed(ctx, w.pool, serverID, err.Error())
		return fmt.Errorf("ssh bootstrap: %w", err)
	}
	log.Info().Msg("bootstrap complete — advancing to WaitingForAgent")

	// ── 6. Poll for Edge Agent connection ───────────────────────────────────
	_ = db.UpdateStatus(ctx, w.pool, op.ID, "WaitingForAgent")

	pollCtx, pollCancel := context.WithTimeout(ctx, w.cfg.AgentConnectTimeout)
	defer pollCancel()

	if err := w.waitForAgent(pollCtx, ep.ID); err != nil {
		_ = db.SetAppServerFailed(ctx, w.pool, serverID, "agent did not connect: "+err.Error())
		return fmt.Errorf("wait for agent: %w", err)
	}

	// ── 7. Mark Ready ───────────────────────────────────────────────────────
	if err := db.SetAppServerReady(ctx, w.pool, serverID, ep.ID); err != nil {
		return fmt.Errorf("set app_server ready: %w", err)
	}
	if err := db.MarkReady(ctx, w.pool, op.ID); err != nil {
		return fmt.Errorf("mark operation ready: %w", err)
	}

	log.Info().Str("server", p.Name).Int("portainer_id", ep.ID).Msg("AppServer ready")
	return nil
}

// doCreateManualAppServer connects a pre-existing (non-Terraform) VM: it registers
// a Portainer edge endpoint, then SSHes in to install Docker + the Edge Agent.
// Mirrors doCreateAppServer minus the Terraform provisioning steps.
//
// The SSH private key is one-shot — it is scrubbed from operations.payload once
// the operation reaches a terminal state (success or failure).
func (w *VMWatcher) doCreateManualAppServer(ctx context.Context, op db.Operation, p createAppServerPayload) error {
	defer func() {
		if err := db.ScrubOperationSecret(context.Background(), w.pool, op.ID, "ssh_private_key"); err != nil {
			log.Warn().Err(err).Str("op", op.ID.String()).Msg("failed to scrub ssh key from payload")
		}
	}()

	if p.VMIP == "" || p.SSHPrivateKey == "" {
		return fmt.Errorf("manual app server requires vm_ip and ssh_private_key")
	}
	sshUser := p.SSHUser
	if sshUser == "" {
		sshUser = "root"
	}
	host := p.VMIP
	if p.SSHPort != 0 && p.SSHPort != 22 {
		host = fmt.Sprintf("%s:%d", p.VMIP, p.SSHPort)
	}

	// ── 1. Register Portainer edge endpoint ─────────────────────────────────
	_ = db.UpdateStatus(ctx, w.pool, op.ID, "ProvisioningVM")
	log.Info().Str("server", p.Name).Str("vm_ip", p.VMIP).Msg("connecting manual VM: creating Portainer edge endpoint")

	ep, err := w.portainer.CreateEdgeEndpoint(
		ctx, p.Name,
		w.cfg.PortainerEdgeURL,
		portainerTunnelAddr(w.cfg.PortainerEdgeURL),
	)
	if err != nil {
		return fmt.Errorf("create edge endpoint: %w", err)
	}
	log.Info().Int("endpoint_id", ep.ID).Str("edge_id", ep.EdgeID).Msg("edge endpoint created")

	// ── 2. Create app_servers DB row (manual — no terraform workspace) ───────
	serverID, err := db.CreateManualAppServer(ctx, w.pool, op.ProjectID, p.Name)
	if err != nil {
		return fmt.Errorf("create app_server row: %w", err)
	}
	log.Info().Str("server_id", serverID.String()).Msg("manual app_servers row created")

	// ── 3. Record VM IP (status → WaitingForAgent) ───────────────────────────
	if err := db.SetAppServerProvisioned(ctx, w.pool, serverID, p.VMIP, "manual"); err != nil {
		return fmt.Errorf("set app_server provisioned: %w", err)
	}

	// ── 4. SSH bootstrap (install Docker + Edge Agent) ───────────────────────
	log.Info().Str("vm_ip", p.VMIP).Msg("running SSH bootstrap on manual VM")
	bootstrapCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	params := w.bootstrapParams(p.Name, ep.EdgeKey, ep.EdgeID)
	if err := dadash.RunBootstrap(bootstrapCtx, host, sshUser, p.SSHPrivateKey, params); err != nil {
		_ = db.SetAppServerFailed(ctx, w.pool, serverID, err.Error())
		return fmt.Errorf("ssh bootstrap: %w", err)
	}
	log.Info().Msg("bootstrap complete — advancing to WaitingForAgent")

	// ── 5. Poll for Edge Agent connection ───────────────────────────────────
	_ = db.UpdateStatus(ctx, w.pool, op.ID, "WaitingForAgent")

	pollCtx, pollCancel := context.WithTimeout(ctx, w.cfg.AgentConnectTimeout)
	defer pollCancel()

	if err := w.waitForAgent(pollCtx, ep.ID); err != nil {
		_ = db.SetAppServerFailed(ctx, w.pool, serverID, "agent did not connect: "+err.Error())
		return fmt.Errorf("wait for agent: %w", err)
	}

	// ── 6. Mark Ready ───────────────────────────────────────────────────────
	if err := db.SetAppServerReady(ctx, w.pool, serverID, ep.ID); err != nil {
		return fmt.Errorf("set app_server ready: %w", err)
	}
	if err := db.MarkReady(ctx, w.pool, op.ID); err != nil {
		return fmt.Errorf("mark operation ready: %w", err)
	}

	log.Info().Str("server", p.Name).Int("portainer_id", ep.ID).Msg("manual AppServer ready")
	return nil
}

// waitForAgent polls GET /api/endpoints/{id} until the edge agent has connected.
func (w *VMWatcher) waitForAgent(ctx context.Context, endpointID int) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			ep, err := w.portainer.GetEndpoint(ctx, endpointID)
			if err != nil {
				log.Warn().Err(err).Msg("poll endpoint error (retrying)")
				continue
			}
			if portainer.IsAgentConnected(ep) {
				return nil
			}
			log.Debug().Int("endpoint", endpointID).
				Bool("heartbeat", ep.Heartbeat).
				Int64("last_checkin", ep.LastCheckInDate).
				Msg("agent not yet connected")
		}
	}
}

// portainerTunnelAddr derives the Edge Agent tunnel address from the Portainer server URL.
// e.g. "https://portainer.dada.ru" → "portainer.dada.ru:8000"
func portainerTunnelAddr(portainerURL string) string {
	host := portainerURL
	for _, prefix := range []string{"https://", "http://"} {
		host = strings.TrimPrefix(host, prefix)
	}
	return host + ":8000"
}
