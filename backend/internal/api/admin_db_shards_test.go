package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestGetAdminDBShardsHidesDatabaseAbsentFromLatestShardSample(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	shard := "sample-" + suffix
	current := time.Now().UTC().Truncate(time.Second)
	previous := current.Add(-time.Minute)

	if _, err := pool.Exec(ctx,
		`INSERT INTO db_shards (name, state, is_platform, metrics_selector, note)
		 VALUES ($1, 'open', FALSE, '', '')`, shard,
	); err != nil {
		t.Fatalf("seed shard: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM db_stat_databases WHERE shard = $1`, shard)
		_, _ = pool.Exec(ctx, `DELETE FROM db_shards WHERE name = $1`, shard)
	})

	for _, sample := range []struct {
		datname string
		at      time.Time
		size    int64
	}{
		{"live-" + suffix, previous, 10},
		{"live-" + suffix, current, 20},
		{"deleted-" + suffix, previous, 30},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO db_stat_databases (shard, datname, collected_at, size_bytes)
			 VALUES ($1, $2, $3, $4)`, shard, sample.datname, sample.at, sample.size,
		); err != nil {
			t.Fatalf("seed sample %s: %v", sample.datname, err)
		}
	}

	h := &Handler{pool: pool}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/db-shards", nil)
	auth.SetClaims(c, &auth.Claims{Groups: []string{"/platform-admins"}})
	h.GetAdminDBShards(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response struct {
		Shards []adminShardView `json:"shards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, view := range response.Shards {
		if view.Name != shard {
			continue
		}
		if view.Databases != 1 || len(view.Top) != 1 || view.Top[0].Datname != "live-"+suffix {
			t.Fatalf("latest shard sample must contain only the live database, got %+v", view)
		}
		return
	}
	t.Fatalf("response omitted shard %s", shard)
}
