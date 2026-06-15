package worker

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/dada-tuda/console/portainer-agent/internal/beget"
	"github.com/dada-tuda/console/portainer-agent/internal/config"
	"github.com/dada-tuda/console/portainer-agent/internal/db"
	tf "github.com/dada-tuda/console/portainer-agent/internal/terraform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// BegetReader is the reverse-sync loop: it discovers Beget VMs created outside
// the console and adopts them into Terraform state + the app_servers table so
// they show up and are managed like console-created VMs.
//
// Dedup is by Beget provider id (skip VMs the console already owns), with a
// name match and a creation grace window as guards against importing a VM the
// console is still in the middle of creating.
type BegetReader struct {
	pool  *pgxpool.Pool
	cfg   *config.Config
	beget *beget.Client
	tf    *tf.Executor
}

// NewBegetReader constructs a BegetReader.
func NewBegetReader(pool *pgxpool.Pool, cfg *config.Config) *BegetReader {
	return &BegetReader{
		pool:  pool,
		cfg:   cfg,
		beget: beget.New(cfg.BegetAPIBaseURL, cfg.BegetToken),
		tf:    tf.NewExecutor(cfg.TFBinPath, cfg.TFStateConnStr, cfg.TFWorkspaceBase),
	}
}

// Start runs an initial reconcile, then repeats on the reader interval. Blocks
// until ctx is cancelled.
func (r *BegetReader) Start(ctx context.Context) {
	log.Info().
		Dur("interval", r.cfg.PollIntervalReader).
		Str("project", r.cfg.BegetReaderProject).
		Msg("beget-reader started")

	r.reconcile(ctx)

	ticker := time.NewTicker(r.cfg.PollIntervalReader)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *BegetReader) reconcile(ctx context.Context) {
	projectID, err := db.GetProjectIDByName(ctx, r.pool, r.cfg.BegetReaderProject)
	if err != nil {
		log.Error().Err(err).Str("project", r.cfg.BegetReaderProject).
			Msg("beget-reader: target project not found, skipping cycle")
		return
	}

	vms, err := r.beget.ListVPS(ctx)
	if err != nil {
		log.Error().Err(err).Msg("beget-reader: list vps")
		return
	}

	knownIDs, err := db.ListKnownVMProviderIDs(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("beget-reader: list known provider ids")
		return
	}
	knownNames, err := db.ListActiveAppServerNames(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("beget-reader: list known names")
		return
	}

	adopted := 0
	for _, vm := range vms {
		if _, ok := knownIDs[vm.ID]; ok {
			continue // console already owns this VM (created or adopted) — dedup
		}
		if _, ok := knownNames[vm.Slug]; ok {
			log.Debug().Str("vm", vm.Slug).
				Msg("beget-reader: name matches an existing app_server, skipping (race guard)")
			continue
		}
		if age := time.Since(vm.DateCreate); age < r.cfg.BegetReaderGrace {
			log.Debug().Str("vm", vm.Slug).Dur("age", age).
				Msg("beget-reader: within grace window, skipping")
			continue
		}

		if err := r.adopt(ctx, projectID, vm); err != nil {
			log.Error().Err(err).Str("vm", vm.Slug).Str("id", vm.ID).
				Msg("beget-reader: adopt failed")
			continue
		}
		adopted++
		log.Info().Str("vm", vm.Slug).Str("id", vm.ID).Str("ip", vm.IPAddress).
			Msg("beget-reader: adopted external VM")
	}

	if adopted > 0 {
		log.Info().Int("adopted", adopted).Int("scanned", len(vms)).Msg("beget-reader: cycle complete")
	}
}

// adopt creates the app_servers row and imports the VM into per-server Terraform
// state. SSH bootstrap and Portainer enrollment are intentionally skipped — an
// externally created VM has no agent credentials we control.
func (r *BegetReader) adopt(ctx context.Context, projectID uuid.UUID, vm beget.VPS) error {
	serverID, err := db.CreateImportedAppServer(ctx, r.pool, projectID, vm.Slug, vm.ID, vm.IPAddress)
	if err != nil {
		return fmt.Errorf("create imported row: %w", err)
	}
	uuidStr := serverID.String()

	workspaceDir := r.tf.WorkspaceDir(uuidStr)
	if err := tf.PrepareAdoptWorkspace(workspaceDir, vm.ID); err != nil {
		_ = db.SetAppServerFailed(ctx, r.pool, serverID, err.Error())
		return fmt.Errorf("prepare adopt workspace: %w", err)
	}
	if err := db.SetAppServerWorkspace(ctx, r.pool, serverID, workspaceDir); err != nil {
		return fmt.Errorf("set workspace: %w", err)
	}

	if err := r.tf.Init(ctx, uuidStr); err != nil {
		_ = db.SetAppServerFailed(ctx, r.pool, serverID, err.Error())
		return fmt.Errorf("terraform init: %w", err)
	}

	region := vm.Configuration.Region
	if region == "" {
		region = r.cfg.BegetRegion
	}
	outputs, err := r.tf.Apply(ctx, uuidStr, r.adoptVars(vm, region))
	if err != nil {
		_ = db.SetAppServerFailed(ctx, r.pool, serverID, err.Error())
		return fmt.Errorf("terraform apply (import): %w", err)
	}

	vmIP := outputs["vm_ip"]
	if vmIP == "" {
		vmIP = vm.IPAddress
	}
	vmID := outputs["vm_id"]
	if vmID == "" {
		vmID = vm.ID
	}
	if err := db.SetAppServerImported(ctx, r.pool, serverID, vmIP, vmID); err != nil {
		return fmt.Errorf("set imported: %w", err)
	}
	return nil
}

// adoptVars builds the Terraform variables for the adopt workspace. cpu/ram/disk
// reflect discovered values; with ignore_changes = all they are cosmetic but
// keep state honest. ssh_public_key is declared-but-unused in adopt main.tf and
// only satisfies the shared variables.tf.
func (r *BegetReader) adoptVars(vm beget.VPS, region string) map[string]string {
	return map[string]string{
		"beget_token":    r.cfg.BegetToken,
		"server_name":    vm.Slug,
		"region":         region,
		"software_slug":  r.cfg.BegetSoftwareSlug,
		"ssh_public_key": r.cfg.AgentSSHPublicKey,
		"cpu":            strconv.Itoa(vm.Configuration.CPUCount),
		"ram_mb":         strconv.Itoa(vm.Configuration.Memory),
		"disk_mb":        strconv.Itoa(vm.Configuration.DiskSize),
	}
}
