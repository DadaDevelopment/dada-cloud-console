package langfusebudget

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestPGStoreRoundTripKeepsNewestReading(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping langfusebudget store test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	st := NewPGStore(pool)
	key := "pk-lf-test-" + uuid.NewString()[:8]
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM langfuse_budget_readings WHERE project_key = $1`, key) })

	_, ok, err := st.Load(ctx, key)
	require.NoError(t, err)
	require.False(t, ok)

	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	require.NoError(t, st.Save(ctx, key, Reading{Day: 80, Month: 20845, ReadAt: at}))
	require.NoError(t, st.Save(ctx, key, Reading{Day: 1, Month: 1, ReadAt: at.Add(-time.Hour)}))
	got, ok, err := st.Load(ctx, key)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(80), got.Day)
	require.Equal(t, int64(20845), got.Month)
	require.True(t, got.ReadAt.Equal(at))
}
