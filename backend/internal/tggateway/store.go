package tggateway

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by Store.Get when no row exists for the agent name.
var ErrNotFound = errors.New("tggateway: no binding for that agent")

// Store is the tg_bindings persistence boundary. An interface so Reconcile can
// be unit-tested against an in-memory fake instead of a real Postgres (see
// manager_test.go) -- the reconcile loop is exactly the "row added/removed ->
// poller started/stopped" behaviour the design doc asks to be tested without
// a real database.
type Store interface {
	List(ctx context.Context) ([]Binding, error)
	Get(ctx context.Context, agentName string) (Binding, error)
	Upsert(ctx context.Context, b Binding) error
	Delete(ctx context.Context, agentName string) error
}

type pgStore struct{ pool *pgxpool.Pool }

// NewPGStore builds the production Store over tg-gateway's Postgres pool.
func NewPGStore(pool *pgxpool.Pool) Store { return pgStore{pool: pool} }

func (s pgStore) List(ctx context.Context) ([]Binding, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT agent_name, project_id, bot_token, bot_username, status, created_at FROM tg_bindings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Binding
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s pgStore) Get(ctx context.Context, agentName string) (Binding, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT agent_name, project_id, bot_token, bot_username, status, created_at
		   FROM tg_bindings WHERE agent_name = $1`, agentName)
	b, err := scanBinding(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Binding{}, ErrNotFound
	}
	return b, err
}

func (s pgStore) Upsert(ctx context.Context, b Binding) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tg_bindings (agent_name, project_id, bot_token, bot_username, status)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (agent_name) DO UPDATE
		   SET project_id   = EXCLUDED.project_id,
		       bot_token    = EXCLUDED.bot_token,
		       bot_username = EXCLUDED.bot_username,
		       status       = EXCLUDED.status`,
		b.AgentName, b.ProjectID, b.BotToken, b.BotUsername, string(b.Status),
	)
	return err
}

func (s pgStore) Delete(ctx context.Context, agentName string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM tg_bindings WHERE agent_name = $1`, agentName)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBinding(row rowScanner) (Binding, error) {
	var b Binding
	var status string
	if err := row.Scan(&b.AgentName, &b.ProjectID, &b.BotToken, &b.BotUsername, &status, &b.CreatedAt); err != nil {
		return Binding{}, err
	}
	b.Status = Status(status)
	return b, nil
}
