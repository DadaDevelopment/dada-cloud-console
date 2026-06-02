package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dada-tuda/console/portainer-agent/internal/config"
	"github.com/dada-tuda/console/portainer-agent/internal/db"
	"github.com/dada-tuda/console/portainer-agent/internal/portainer"
	dadash "github.com/dada-tuda/console/portainer-agent/internal/ssh"
	tfexecutor "github.com/dada-tuda/console/portainer-agent/internal/terraform"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// VMWatcher polls the operations table for VM-track operations and dispatches them.
type VMWatcher struct {
	pool      *pgxpool.Pool
	cfg       *config.Config
	portainer *portainer.Client
	tf        *tfexecutor.Executor
}

// NewVMWatcher constructs a VMWatcher with its dependencies.
func NewVMWatcher(pool *pgxpool.Pool, cfg *config.Config) *VMWatcher {
	return &VMWatcher{
		pool:      pool,
		cfg:       cfg,
		portainer: portainer.New(cfg.PortainerURL, cfg.PortainerAPIToken),
		tf:        tfexecutor.NewExecutor(cfg.TFBinPath, cfg.TFStateConnStr, cfg.TFWorkspaceBase),
	}
}

// Start begins the polling loop. Blocks until ctx is cancelled.
func (w *VMWatcher) Start(ctx context.Context) {
	log.Info().Dur("interval", w.cfg.PollIntervalDB).Msg("vm-watcher started")
	ticker := time.NewTicker(w.cfg.PollIntervalDB)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *VMWatcher) poll(ctx context.Context) {
	ops, err := db.ClaimPending(ctx, w.pool)
	if err != nil {
		log.Error().Err(err).Msg("vm-watcher: claim pending")
		return
	}
	for _, op := range ops {
		if err := w.dispatch(ctx, op); err != nil {
			log.Error().Err(err).
				Str("op", op.ID.String()).
				Str("action", op.Action).
				Msg("operation failed")
			_ = db.MarkFailed(ctx, w.pool, op.ID, "PROCESSING_ERROR", err.Error())
		}
	}
}

func (w *VMWatcher) dispatch(ctx context.Context, op db.Operation) error {
	log.Info().Str("op", op.ID.String()).Str("action", op.Action).Msg("dispatching")
	switch op.Action {
	case "CreateAppServer":
		return w.doCreateAppServer(ctx, op)
	case "DeleteAppServer":
		return w.doDeleteAppServer(ctx, op)
	case "DeployStack":
		return w.doDeployStack(ctx, op)
	default:
		return fmt.Errorf("unknown vm action: %s", op.Action)
	}
}

// bootstrapParams assembles SSH bootstrap template params from config.
func (w *VMWatcher) bootstrapParams(serverName, edgeKey, edgeID string) dadash.BootstrapParams {
	return dadash.BootstrapParams{
		ServerName:               serverName,
		EdgeKey:                  edgeKey,
		EdgeID:                   edgeID,
		PrometheusRemoteWriteURL: w.cfg.PrometheusRemoteWriteURL,
		PrometheusUser:           w.cfg.PrometheusRemoteWriteUser,
		PrometheusPass:           w.cfg.PrometheusRemoteWritePass,
		ElasticsearchURL:         w.cfg.ElasticsearchURL,
		ElasticsearchAPIKey:      w.cfg.ElasticsearchAPIKey,
	}
}

// tfVars assembles Terraform variable map from config + AppServer params.
func (w *VMWatcher) tfVars(serverName, region string) map[string]string {
	return map[string]string{
		"beget_token":    w.cfg.BegetToken,
		"server_name":    serverName,
		"region":         region,
		"software_slug":  w.cfg.BegetSoftwareSlug,
		"ssh_public_key": w.cfg.AgentSSHPublicKey,
	}
}

// unmarshalPayload decodes op.Payload into a typed struct.
func unmarshalPayload(raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	return nil
}
