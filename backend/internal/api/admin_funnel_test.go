package api

import (
	"context"
	"testing"
)

func TestAdminFunnelQueriesMatchCurrentSchema(t *testing.T) {
	pool := overviewBrokenTestPool(t)
	ctx := context.Background()

	var counts [8]int
	err := pool.QueryRow(ctx, adminFunnelCountsQuery("TRUE"), nil).Scan(
		&counts[0], &counts[1], &counts[2], &counts[3],
		&counts[4], &counts[5], &counts[6], &counts[7],
	)
	if err != nil {
		t.Fatalf("admin funnel counts query must compile against current schema: %v", err)
	}

	rows, err := pool.Query(ctx, adminFunnelCohortsQuery("TRUE"))
	if err != nil {
		t.Fatalf("admin funnel cohorts query must compile against current schema: %v", err)
	}
	rows.Close()
}
