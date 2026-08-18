package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
)

// adminShardWindow is how far back the per-database growth on the shard view
// is measured. It matches the owner-facing insights window so support and the
// owner are looking at the same number.
const adminShardWindow = 7 * 24 * time.Hour

// adminShardTopDatabases bounds how many databases are returned per shard. The
// question this view answers is "who is eating this instance", and the tail
// below the twentieth database has never been the answer.
const adminShardTopDatabases = 20

// adminShardDatabase is one logical database as seen from the instance it
// lives on, with the owner attached.
type adminShardDatabase struct {
	Datname     string  `json:"datname"`
	SizeBytes   int64   `json:"size_bytes"`
	Share       float64 `json:"share"`
	GrowthBytes int64   `json:"growth_bytes_7d"`
	Connections int32   `json:"connections"`
	ProjectID   string  `json:"project_id,omitempty"`
	ProjectName string  `json:"project_name,omitempty"`
	Resource    string  `json:"resource,omitempty"`
	AppRef      string  `json:"app_ref,omitempty"`
	Tier        string  `json:"tier,omitempty"`
	Critical    int     `json:"critical_advisories"`
	Warnings    int     `json:"warning_advisories"`
	Orphan      bool    `json:"orphan"`
}

// adminShardView is one PostgreSQL instance: what the registry says about it
// and what is actually inside.
type adminShardView struct {
	Name            string               `json:"name"`
	State           string               `json:"state"`
	IsPlatform      bool                 `json:"is_platform"`
	CapacityBytes   int64                `json:"capacity_bytes"`
	SampledBytes    int64                `json:"sampled_bytes"`
	Databases       int                  `json:"databases"`
	CollectedAt     *time.Time           `json:"collected_at,omitempty"`
	InstanceStartAt *time.Time           `json:"instance_start_at,omitempty"`
	Top             []adminShardDatabase `json:"top"`
}

// dbOwner is the control plane's record of who a logical database belongs to.
type dbOwner struct {
	ProjectID   string
	ProjectName string
	Resource    string
	AppRef      string
	Tier        string
}

// GetAdminDBShards answers "who is eating this instance" for every managed
// PostgreSQL shard.
//
// The same samples the owner sees on their own database page are re-grouped by
// instance here, so support and the owner never argue about whose number is
// right. Databases with no snapshot behind them are returned marked orphan
// rather than dropped: a database on the instance that the control plane does
// not know about is the single most useful thing this view can surface, and
// hiding it is how the platform postgres ended up sharing a volume with a
// tenant nobody was watching.
//
// @ID          getAdminDBShards
// @Summary     Per-shard database consumption (admin readers)
// @Description Returns every managed PostgreSQL shard from the registry with what is actually inside it: total sampled size, database count, instance uptime, and the largest databases with their owning project, quota tier, seven-day growth and open advisory counts. Databases present in the samples but absent from the control plane's resource snapshots are returned with orphan=true. Reads collected samples only; no shard is queried live. Platform-admin and platform-analyst readers; every other caller gets 403.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{}
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Router      /admin/db-shards [get]
func (h *Handler) GetAdminDBShards(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isAdminReader(claims) {
		respondForbidden(c)
		return
	}
	ctx := c.Request.Context()

	shards, err := h.shardRegistry(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read shard registry")
		return
	}
	owners, err := h.databaseOwners(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read database owners")
		return
	}
	advisories, err := h.advisoryCounts(ctx)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read advisories")
		return
	}

	rows, err := h.pool.Query(ctx, `
		WITH latest_shard_sample AS (
			SELECT shard, MAX(collected_at) AS collected_at
			  FROM db_stat_databases
			 WHERE collected_at > $1
			 GROUP BY shard
		), b AS (
			SELECT d.shard, d.datname,
			       l.collected_at AS last_at,
			       MIN(d.collected_at) AS first_at
			  FROM db_stat_databases d
			  JOIN latest_shard_sample l
			    ON l.shard = d.shard
			 WHERE d.collected_at > $1
			   AND EXISTS (
			       SELECT 1 FROM db_stat_databases current_sample
			        WHERE current_sample.shard = l.shard
			          AND current_sample.datname = d.datname
			          AND current_sample.collected_at = l.collected_at
			   )
			 GROUP BY d.shard, d.datname, l.collected_at
		)
		SELECT b.shard, b.datname, l.size_bytes, l.numbackends, l.collected_at,
		       l.instance_start_at, f.size_bytes
		  FROM b
		  JOIN db_stat_databases l
		    ON l.shard = b.shard AND l.datname = b.datname AND l.collected_at = b.last_at
		  JOIN db_stat_databases f
		    ON f.shard = b.shard AND f.datname = b.datname AND f.collected_at = b.first_at`,
		time.Now().Add(-adminShardWindow))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read samples")
		return
	}
	defer rows.Close()

	byShard := map[string]*adminShardView{}
	for _, s := range shards {
		view := s
		byShard[s.Name] = &view
	}
	for rows.Next() {
		var (
			shard, datname  string
			size, firstSize int64
			backends        int32
			collectedAt     time.Time
			startAt         *time.Time
		)
		if err := rows.Scan(&shard, &datname, &size, &backends, &collectedAt, &startAt, &firstSize); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read samples")
			return
		}
		view, ok := byShard[shard]
		if !ok {
			view = &adminShardView{Name: shard, State: "unregistered"}
			byShard[shard] = view
		}
		if view.CollectedAt == nil || collectedAt.After(*view.CollectedAt) {
			at := collectedAt
			view.CollectedAt = &at
			view.InstanceStartAt = startAt
		}
		view.SampledBytes += size
		view.Databases++
		owner, known := owners[datname]
		counts := advisories[shard+"/"+datname]
		view.Top = append(view.Top, adminShardDatabase{
			Datname:     datname,
			SizeBytes:   size,
			GrowthBytes: size - firstSize,
			Connections: backends,
			ProjectID:   owner.ProjectID,
			ProjectName: owner.ProjectName,
			Resource:    owner.Resource,
			AppRef:      owner.AppRef,
			Tier:        owner.Tier,
			Critical:    counts["critical"],
			Warnings:    counts["warning"],
			Orphan:      !known,
		})
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read samples")
		return
	}

	out := make([]adminShardView, 0, len(byShard))
	for _, view := range byShard {
		sort.Slice(view.Top, func(i, j int) bool { return view.Top[i].SizeBytes > view.Top[j].SizeBytes })
		if view.SampledBytes > 0 {
			for i := range view.Top {
				view.Top[i].Share = float64(view.Top[i].SizeBytes) / float64(view.SampledBytes)
			}
		}
		if len(view.Top) > adminShardTopDatabases {
			view.Top = view.Top[:adminShardTopDatabases]
		}
		out = append(out, *view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	c.JSON(http.StatusOK, gin.H{"shards": out, "window_days": int(adminShardWindow.Hours() / 24)})
}

// shardRegistry lists every shard the platform knows about, including closed
// and platform-reserved ones. Placement filters those out; this view must not,
// because a closed shard is exactly where a runaway database is likely to be.
func (h *Handler) shardRegistry(ctx context.Context) ([]adminShardView, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT name, state, is_platform, capacity_bytes FROM db_shards ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []adminShardView
	for rows.Next() {
		var s adminShardView
		if err := rows.Scan(&s.Name, &s.State, &s.IsPlatform, &s.CapacityBytes); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// databaseOwners maps a logical database name onto the project that owns it.
//
// The key is the datname because that is all a sample carries. Names are
// unique per instance by construction, and a collision across shards would
// mean two projects sharing one name — worth seeing as a wrong owner on this
// page rather than as silence.
func (h *Handler) databaseOwners(ctx context.Context) (map[string]dbOwner, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT rs.project_id, COALESCE(p.display_name, ''), rs.name, rs.summary_json
		  FROM resource_snapshots rs
		  LEFT JOIN projects p ON p.id = rs.project_id
		 WHERE rs.kind = 'ServiceDatabaseV2'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]dbOwner{}
	for rows.Next() {
		var (
			projectID, projectName, name string
			summaryRaw                   []byte
		)
		if err := rows.Scan(&projectID, &projectName, &name, &summaryRaw); err != nil {
			return nil, err
		}
		datname := serviceDatabaseDatname(summaryRaw)
		if datname == "" {
			continue
		}
		out[datname] = dbOwner{
			ProjectID:   projectID,
			ProjectName: projectName,
			Resource:    name,
			AppRef:      serviceDatabaseAppRef(summaryRaw),
			Tier:        serviceDatabaseTier(summaryRaw),
		}
	}
	return out, rows.Err()
}

// advisoryCounts counts open advisories per database and severity, keyed by
// "shard/datname" so a name reused on two shards keeps its own counts.
func (h *Handler) advisoryCounts(ctx context.Context) (map[string]map[string]int, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT shard, datname, severity, COUNT(*) FROM db_advisories GROUP BY shard, datname, severity`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]map[string]int{}
	for rows.Next() {
		var shard, datname, severity string
		var n int
		if err := rows.Scan(&shard, &datname, &severity, &n); err != nil {
			return nil, err
		}
		key := shard + "/" + datname
		if out[key] == nil {
			out[key] = map[string]int{}
		}
		out[key][severity] = n
	}
	return out, rows.Err()
}
