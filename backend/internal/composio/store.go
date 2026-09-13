package composio

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoSession marks an end user who has no tool-router session yet.
var ErrNoSession = errors.New("composio: no session for this end user")

// SessionRow is the broker's mapping from one end user of one project to the
// Composio session that carries that user's connected accounts.
type SessionRow struct {
	ProjectID  uuid.UUID
	EndUserKey string
	UserID     string
	SessionID  string
	MCPURL     string
	Toolkits   []string
}

// Integration is one toolkit this end user authorized, with Composio's own
// status kept verbatim.
type Integration struct {
	Toolkit            string    `json:"toolkit"`
	ConnectedAccountID string    `json:"connected_account_id,omitempty"`
	Status             string    `json:"status"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Store is the broker's persistence. It owns three tables and nothing else
// reads them, the same posture tg-gateway has toward tg_bindings.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Session returns the session row of one end user, or ErrNoSession.
func (s *Store) Session(ctx context.Context, projectID uuid.UUID, endUserKey string) (SessionRow, error) {
	row := SessionRow{ProjectID: projectID, EndUserKey: endUserKey}
	err := s.pool.QueryRow(ctx,
		`SELECT user_id, session_id, mcp_url, toolkits
		   FROM composio_sessions
		  WHERE project_id = $1 AND end_user_key = $2`,
		projectID, endUserKey,
	).Scan(&row.UserID, &row.SessionID, &row.MCPURL, &row.Toolkits)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionRow{}, ErrNoSession
	}
	return row, err
}

// SaveSession upserts the session row of one end user.
func (s *Store) SaveSession(ctx context.Context, row SessionRow) error {
	toolkits := row.Toolkits
	if toolkits == nil {
		toolkits = []string{}
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO composio_sessions (project_id, end_user_key, user_id, session_id, mcp_url, toolkits)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (project_id, end_user_key) DO UPDATE
		    SET user_id = EXCLUDED.user_id,
		        session_id = EXCLUDED.session_id,
		        mcp_url = EXCLUDED.mcp_url,
		        toolkits = EXCLUDED.toolkits,
		        last_used_at = NOW()`,
		row.ProjectID, row.EndUserKey, row.UserID, row.SessionID, row.MCPURL, toolkits,
	)
	return err
}

// SetSessionToolkits records the allowlist the session now carries upstream.
func (s *Store) SetSessionToolkits(ctx context.Context, projectID uuid.UUID, endUserKey string, toolkits []string) error {
	if toolkits == nil {
		toolkits = []string{}
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE composio_sessions SET toolkits = $3, last_used_at = NOW()
		  WHERE project_id = $1 AND end_user_key = $2`,
		projectID, endUserKey, toolkits,
	)
	return err
}

// TouchSession records that this session served a call, so an unused session can
// be reaped later without guessing.
func (s *Store) TouchSession(ctx context.Context, projectID uuid.UUID, endUserKey string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE composio_sessions SET last_used_at = NOW()
		  WHERE project_id = $1 AND end_user_key = $2`,
		projectID, endUserKey,
	)
	return err
}

// UpsertIntegration records or updates one authorization of one end user.
func (s *Store) UpsertIntegration(ctx context.Context, projectID uuid.UUID, endUserKey, toolkit, connectedAccountID, status string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO composio_integrations (project_id, end_user_key, toolkit, connected_account_id, status)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (project_id, end_user_key, toolkit) DO UPDATE
		    SET connected_account_id = CASE WHEN EXCLUDED.connected_account_id = '' 
		                                   THEN composio_integrations.connected_account_id
		                                   ELSE EXCLUDED.connected_account_id END,
		        status = EXCLUDED.status,
		        updated_at = NOW()`,
		projectID, endUserKey, toolkit, connectedAccountID, status,
	)
	return err
}

// Integrations lists what this end user authorized, newest state first.
func (s *Store) Integrations(ctx context.Context, projectID uuid.UUID, endUserKey string) ([]Integration, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT toolkit, connected_account_id, status, updated_at
		   FROM composio_integrations
		  WHERE project_id = $1 AND end_user_key = $2
		  ORDER BY toolkit`,
		projectID, endUserKey,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Integration
	for rows.Next() {
		var item Integration
		if err := rows.Scan(&item.Toolkit, &item.ConnectedAccountID, &item.Status, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteIntegration forgets one authorization locally.
func (s *Store) DeleteIntegration(ctx context.Context, projectID uuid.UUID, endUserKey, toolkit string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM composio_integrations
		  WHERE project_id = $1 AND end_user_key = $2 AND toolkit = $3`,
		projectID, endUserKey, toolkit,
	)
	return err
}

// RecordToolCall writes the audit row the MCP transport would otherwise lose:
// over MCP the client executes against Composio directly, so the broker is the
// only place a call is observable at all.
func (s *Store) RecordToolCall(ctx context.Context, projectID uuid.UUID, endUserKey, agentName, toolName string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO composio_tool_calls (project_id, end_user_key, agent_name, tool_name)
		 VALUES ($1, $2, $3, $4)`,
		projectID, endUserKey, agentName, toolName,
	)
	return err
}
