package api

import (
	"context"
	"log"

	"github.com/dada-tuda/console/backend/internal/beget"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/fincore"
)

// startFinCoreSync starts the hourly push of this platform's own economics --
// users as clients, succeeded payments as incoming money, the Beget bill as an
// expense -- into FinCore, the analytical CRM this team also builds.
//
// Nothing starts unless FINCORE_BASE_URL, FINCORE_TOKEN and FINCORE_TENANT_SLUG
// are all set: fincore.New returns nil when the integration is unconfigured, so
// every deployment that has not opted in is untouched.
//
// The token must be a FinCore service token (fcs_...). A personal JWT reaches
// the ingest endpoints and is refused with 401, which is worth saying at
// startup rather than once an hour in the logs.
func (h *Handler) startFinCoreSync(cfg *config.Config) {
	client := fincore.New(cfg.FinCoreBaseURL, cfg.FinCoreToken, cfg.FinCoreTenantSlug)
	if client == nil {
		return
	}
	if !client.LooksLikeServiceToken() {
		log.Printf("fincore: FINCORE_TOKEN is not a service token (fcs_...); the ingest seam will answer 401 -- sync not started")
		return
	}
	syncer := fincore.NewSyncer(
		h.pool,
		client,
		beget.New(cfg.BegetK8SToken),
		cfg.BegetK8SClusterSlug,
		cfg.HardwareMonthlyCostRUB,
		cfg.FinCoreProjectID,
		cfg.FinCoreIncludeInternalOrgs,
	)
	if syncer == nil {
		return
	}
	log.Printf("fincore: syncing clients, payments and hosting cost into tenant %s at %s", cfg.FinCoreTenantSlug, cfg.FinCoreBaseURL)
	syncer.Start(context.Background())
}
