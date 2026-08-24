package worker

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// infraImages maps a well-known infrastructure image basename to its Infra
// subtype. Anything NOT in this map is treated as an application service. This is
// what lets a VM compose stack be decomposed into first-class apps (named after
// their image, 1:1 with the k8s apps) plus generic Infra (postgres, nginx, …).
var infraImages = map[string]string{
	"postgres":      "database",
	"postgresql":    "database",
	"mysql":         "database",
	"mariadb":       "database",
	"mongo":         "database",
	"mongodb":       "database",
	"redis":         "cache",
	"memcached":     "cache",
	"valkey":        "cache",
	"nginx":         "proxy",
	"traefik":       "proxy",
	"haproxy":       "proxy",
	"envoy":         "proxy",
	"caddy":         "proxy",
	"rabbitmq":      "queue",
	"kafka":         "queue",
	"nats":          "queue",
	"clickhouse":    "database",
	"elasticsearch": "search",
	"opensearch":    "search",
}

// imageBasename returns the lowercased final path segment of an image ref, with
// registry, tag and digest stripped: "nexus.dada-tuda.ru/dada/profi-backend:master-1.0.0-194"
// → "profi-backend"; "mirror.gcr.io/library/postgres:16-alpine" → "postgres".
func imageBasename(image string) string {
	repo := image
	if i := strings.LastIndex(repo, "@"); i >= 0 {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, ":"); i >= 0 {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	return strings.ToLower(repo)
}

// classifyService decides whether a stack service is an application or infra,
// and derives its console name from the image basename (so the VM's "profi" and
// "profi-backend" match the same-named k8s apps 1:1). Returns kind (App|Infra),
// an infra subtype (empty for App), and the name.
func classifyService(image string) (kind, subtype, name string) {
	base := imageBasename(image)
	if st, ok := infraImages[base]; ok {
		return "Infra", st, base
	}
	return "App", "", base
}

// applyPrimaryHostname writes the app's public address onto its live-status
// patch, mirroring what the k8s status reconciler puts on a cluster app so the
// console renders both runtimes the same way. The keys are always written, with
// url nil when there is no hostname: the patch is merged into summary_json, so
// leaving them out would keep showing the address of a domain that was detached.
// A lookup failure is non-fatal — live status is worth more than an address.
func applyPrimaryHostname(ctx context.Context, pool *pgxpool.Pool, envID uuid.UUID, appName string, patch map[string]any) {
	info, err := db.PrimaryHostname(ctx, pool, envID, appName)
	if err != nil {
		log.Warn().Err(err).Str("app", appName).Msg("primary hostname lookup failed (non-fatal)")
		return
	}
	for k, v := range hostnamePatchFields(info) {
		patch[k] = v
	}
}

// hostnamePatchFields renders one hostname lookup into the summary keys the
// console reads. Split out from applyPrimaryHostname so the shape is testable
// without a database.
func hostnamePatchFields(info db.PrimaryHostnameInfo) map[string]any {
	if info.Hostname == "" {
		return map[string]any{"url": nil, "url_status": nil, "url_reason": nil}
	}
	return map[string]any{
		"url":        "https://" + info.Hostname,
		"url_status": info.Status,
		"url_reason": info.Reason,
	}
}

// syncStackSnapshots mirrors the just-deployed per-environment stack's live
// container state onto the first-class Application snapshots. Each Application is
// one compose SERVICE (service label == app name), so live status is matched to
// its kind=App snapshot by service name and MERGED via UpdateLiveStatus — which
// only touches existing rows (never resurrecting a deleted app) and preserves
// summary_json.desired (the durable spec renderEnvAggregate reads). A running
// service with no managing app row is recorded as an Orphaned app so the console
// can surface drift (deletes are Prune:false, so a removed app lingers until
// cleaned up). Reproducible: runs on every deploy.
func (w *VMWatcher) syncStackSnapshots(ctx context.Context, op db.Operation, endpointID int, stackName string) {
	if op.EnvironmentID == nil {
		return
	}
	containers, err := w.portainer.ListContainers(ctx, endpointID, "")
	if err != nil {
		log.Warn().Err(err).Str("stack", stackName).Msg("stack snapshot sync: list containers failed (non-fatal)")
		return
	}
	count := 0
	for _, c := range containers {
		if c.Labels["com.docker.compose.project"] != stackName {
			continue
		}
		service := c.Labels["com.docker.compose.service"]
		if service == "" {
			continue
		}
		patch := map[string]any{
			"image":       c.Image,
			"status":      "Ready",
			"live_source": "vm",
			"stack":       stackName,
			"endpoint_id": endpointID,
		}
		applyPrimaryHostname(ctx, w.pool, *op.EnvironmentID, service, patch)
		patchJSON, _ := json.Marshal(patch)
		n, err := db.UpdateLiveStatus(ctx, w.pool, *op.EnvironmentID, "App", service, "Ready", patchJSON)
		if err != nil {
			log.Warn().Err(err).Str("service", service).Msg("stack live-status update failed (non-fatal)")
			continue
		}
		if n == 0 {
			orphan := map[string]any{
				"image":       c.Image,
				"status":      "Orphaned",
				"runtime":     "compose",
				"live_source": "vm",
				"stack":       stackName,
				"endpoint_id": endpointID,
				"orphaned":    true,
			}
			orphanJSON, _ := json.Marshal(orphan)
			_ = db.UpsertSnapshot(ctx, w.pool, op.ProjectID, op.EnvironmentID, "App", service, "Orphaned", orphanJSON, time.Now())
		}
		count++
	}
	log.Info().Str("stack", stackName).Int("services", count).Msg("stack live status synced onto app snapshots")
}
