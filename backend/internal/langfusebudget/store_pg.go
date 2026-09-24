package langfusebudget

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore keeps readings in langfuse_budget_readings.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore returns nil for a nil pool, so the guard runs without a store.
func NewPGStore(pool *pgxpool.Pool) Store {
	if pool == nil {
		return nil
	}
	return &PGStore{pool: pool}
}

func (s *PGStore) Load(ctx context.Context, key string) (Reading, bool, error) {
	var r Reading
	err := s.pool.QueryRow(ctx, `SELECT day_units, month_units, read_at FROM langfuse_budget_readings WHERE project_key = $1`, key).Scan(&r.Day, &r.Month, &r.ReadAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reading{}, false, nil
	}
	if err != nil {
		return Reading{}, false, err
	}
	return r, true, nil
}

func (s *PGStore) Save(ctx context.Context, key string, r Reading) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO langfuse_budget_readings (project_key, day_units, month_units, read_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_key) DO UPDATE
		SET day_units = EXCLUDED.day_units, month_units = EXCLUDED.month_units, read_at = EXCLUDED.read_at
		WHERE langfuse_budget_readings.read_at <= EXCLUDED.read_at`, key, r.Day, r.Month, r.ReadAt)
	return err
}
