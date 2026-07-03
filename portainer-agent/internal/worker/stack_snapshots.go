package worker

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/dada-tuda/console/portainer-agent/internal/db"
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

// syncStackSnapshots reads the just-deployed stack's containers via the Portainer
// docker proxy and upserts one resource_snapshot per service into the operation's
// environment: application services as kind=App (named after the image), infra
// services as kind=Infra with a subtype. This is the decomposition that lets the
// console show a VM compose stack as first-class apps + infra instead of one
// opaque compose app. Reproducible: runs on every deploy, keyed off discovery.
func (w *VMWatcher) syncStackSnapshots(ctx context.Context, op db.Operation, endpointID int, stackName string) {
	containers, err := w.portainer.ListContainers(ctx, endpointID, "")
	if err != nil {
		log.Warn().Err(err).Str("stack", stackName).Msg("stack snapshot sync: list containers failed (non-fatal)")
		return
	}
	now := time.Now()
	count := 0
	for _, c := range containers {
		if c.Labels["com.docker.compose.project"] != stackName {
			continue
		}
		kind, subtype, name := classifyService(c.Image)
		summary := map[string]any{
			"image":       c.Image,
			"status":      "Ready",
			"runtime":     "compose",
			"live_source": "vm",
			"service":     c.Labels["com.docker.compose.service"],
			"stack":       stackName,
			"endpoint_id": endpointID,
		}
		if subtype != "" {
			summary["subtype"] = subtype
		}
		summaryJSON, _ := json.Marshal(summary)
		if err := db.UpsertSnapshot(ctx, w.pool, op.ProjectID, op.EnvironmentID, kind, name, "Ready", summaryJSON, now); err != nil {
			log.Warn().Err(err).Str("name", name).Str("kind", kind).Msg("stack snapshot upsert failed (non-fatal)")
			continue
		}
		count++
	}
	log.Info().Str("stack", stackName).Int("services", count).Msg("stack decomposed into app/infra snapshots")
}
