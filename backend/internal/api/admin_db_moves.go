package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// adminDBMoveListLimit bounds the move history. The list answers "what is
// moving and what moved recently"; the archive of every move ever made belongs
// in a query, not on a page.
const adminDBMoveListLimit = 100

// adminDBMovePreflightTimeout bounds the live checks against both shards. The
// operator is waiting on the response, and a shard that cannot answer in this
// window cannot be trusted to carry a move either.
const adminDBMovePreflightTimeout = 5 * time.Second

// adminDBMove is one move as an operator sees it.
type adminDBMove struct {
	ID          string     `json:"id"`
	Datname     string     `json:"datname"`
	OwnerRole   string     `json:"owner_role"`
	SourceShard string     `json:"source_shard"`
	TargetShard string     `json:"target_shard"`
	Phase       string     `json:"phase"`
	LagBytes    int64      `json:"lag_bytes"`
	Error       string     `json:"error,omitempty"`
	RequestedBy string     `json:"requested_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CutoverAt   *time.Time `json:"cutover_at,omitempty"`
}

// startDBMoveRequest asks for one database to be moved to one shard.
//
// The source is never taken from the caller: it is where the database is right
// now, and a caller who names the wrong source would have the worker replicate
// from an instance that stopped receiving writes.
type startDBMoveRequest struct {
	Datname     string `json:"datname"`
	TargetShard string `json:"target_shard"`
}

// ListAdminDBMoves returns recent and in-flight moves.
//
// @ID          listAdminDBMoves
// @Summary     Database moves between shards (admin readers)
// @Description Returns the most recent moves of logical databases between PostgreSQL shards with their phase, replication lag, cutover time and failure reason. Platform-admin and platform-analyst readers; every other caller gets 403.
// @Tags        admin
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} map[string]interface{}
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Router      /admin/db-moves [get]
func (h *Handler) ListAdminDBMoves(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isAdminReader(claims) {
		respondForbidden(c)
		return
	}

	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT id::text, datname, owner_role, source_shard, target_shard, phase,
		       lag_bytes, error, requested_by, created_at, updated_at, cutover_at
		  FROM db_moves
		 ORDER BY created_at DESC
		 LIMIT $1`, adminDBMoveListLimit)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read moves")
		return
	}
	defer rows.Close()

	out := make([]adminDBMove, 0, adminDBMoveListLimit)
	for rows.Next() {
		var m adminDBMove
		if err := rows.Scan(&m.ID, &m.Datname, &m.OwnerRole, &m.SourceShard, &m.TargetShard,
			&m.Phase, &m.LagBytes, &m.Error, &m.RequestedBy, &m.CreatedAt, &m.UpdatedAt, &m.CutoverAt); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to read moves")
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read moves")
		return
	}
	c.JSON(http.StatusOK, gin.H{"moves": out})
}

// StartAdminDBMove enqueues a move of one database to one shard.
//
// Until this endpoint the only way to move a database was an INSERT into
// db_moves typed by hand against production, which is how a move was once
// started with the source shard the data had already left. Everything the
// operator would otherwise have to be right about is checked here against the
// live instances rather than against the control plane's opinion: where the
// database is, who owns it, that the target does not already hold a database
// under that name, and that the owner is not the superuser.
//
// The superuser refusal is not a formality. A move carries the owner role and
// its SCRAM verifier to the target; for a database owned by postgres that
// would overwrite the target shard's admin password with the source's.
//
// @ID          startAdminDBMove
// @Summary     Move a database to another shard (platform admins)
// @Description Enqueues a move of one logical database to another PostgreSQL shard. The source shard, the owner role and the checks are resolved live against the instances: the database must exist on the shard it is currently routed to, must not already exist on the target, and must not be owned by the superuser. Returns the created move row; the move driver advances it through schema copy, logical replication and cutover. Platform admins only.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       request body startDBMoveRequest true "Database and target shard"
// @Success     201 {object} adminDBMove
// @Failure     400 {object} map[string]string
// @Failure     401 {object} map[string]string
// @Failure     403 {object} map[string]string
// @Failure     409 {object} map[string]string
// @Router      /admin/db-moves [post]
func (h *Handler) StartAdminDBMove(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	if !isGod(claims) {
		respondForbidden(c)
		return
	}

	var req startDBMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid JSON body")
		return
	}
	datname := strings.TrimSpace(req.Datname)
	target := strings.TrimSpace(req.TargetShard)
	if datname == "" || target == "" {
		respondError(c, http.StatusBadRequest, "datname and target_shard are required")
		return
	}

	ctx := c.Request.Context()
	source, err := h.currentShardOf(ctx, datname)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if source == target {
		respondError(c, http.StatusBadRequest, fmt.Sprintf("%s is already on %s", datname, target))
		return
	}

	owner, err := h.preflightDBMove(ctx, datname, source, target)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	var m adminDBMove
	err = h.pool.QueryRow(ctx, `
		INSERT INTO db_moves (datname, owner_role, source_shard, target_shard, requested_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, datname, owner_role, source_shard, target_shard, phase,
		          lag_bytes, error, requested_by, created_at, updated_at, cutover_at`,
		datname, owner, source, target, claims.UserID.String(),
	).Scan(&m.ID, &m.Datname, &m.OwnerRole, &m.SourceShard, &m.TargetShard, &m.Phase,
		&m.LagBytes, &m.Error, &m.RequestedBy, &m.CreatedAt, &m.UpdatedAt, &m.CutoverAt)
	if isUniqueViolation(err) {
		respondError(c, http.StatusConflict, fmt.Sprintf("%s already has a move in flight", datname))
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to enqueue move")
		return
	}
	c.JSON(http.StatusCreated, m)
}

// currentShardOf reports where a database is routed right now: the newest move
// that reached cutover wins, otherwise the CR snapshot.
//
// This is the same precedence the router itself renders with, which is the
// point — a move must start from the instance clients are actually being sent
// to, not from the one the CR still names.
func (h *Handler) currentShardOf(ctx context.Context, datname string) (string, error) {
	overrides, err := h.routerMoveOverrides(ctx)
	if err != nil {
		return "", errors.New("failed to read move overrides")
	}
	if shard, ok := overrides[datname]; ok {
		return shard, nil
	}
	placements, err := h.routerPlacements(ctx)
	if err != nil {
		return "", errors.New("failed to read placements")
	}
	for _, p := range placements {
		if p.Datname == datname && p.Shard != "" {
			return p.Shard, nil
		}
	}
	return "", fmt.Errorf("%s has no known placement: no CR snapshot and no finished move", datname)
}

// preflightDBMove checks both instances and returns the owner role to carry.
//
// The owner is read from the source instance rather than from the CR because
// it is the source instance the move copies from: a CR naming a role the
// database does not actually belong to would hand the target's objects to a
// role no client authenticates as.
func (h *Handler) preflightDBMove(ctx context.Context, datname, source, target string) (string, error) {
	if h.cfg == nil {
		return "", errors.New("shard admin credentials are not configured")
	}
	dsns := parseShardAdminDSNs(h.cfg.DBShardAdminDSNs)
	srcDSN, ok := dsns[source]
	if !ok {
		return "", fmt.Errorf("shard %s has no admin credentials", source)
	}
	dstDSN, ok := dsns[target]
	if !ok {
		return "", fmt.Errorf("shard %s has no admin credentials", target)
	}

	ctx, cancel := context.WithTimeout(ctx, adminDBMovePreflightTimeout)
	defer cancel()

	owner, err := databaseOwnerOn(ctx, srcDSN, datname)
	if err != nil {
		return "", err
	}
	if owner == "" {
		return "", fmt.Errorf("%s does not exist on %s", datname, source)
	}
	if owner == "postgres" {
		return "", fmt.Errorf("%s is owned by postgres: moving it would copy the superuser verifier onto %s", datname, target)
	}
	existing, err := databaseOwnerOn(ctx, dstDSN, datname)
	if err != nil {
		return "", err
	}
	if existing != "" {
		return "", fmt.Errorf("%s already exists on %s, owned by %s", datname, target, existing)
	}
	return owner, nil
}

// databaseOwnerOn returns the owner role of a database on one instance, or an
// empty string when the instance has no such database.
func databaseOwnerOn(ctx context.Context, dsn, datname string) (string, error) {
	cfg, err := configForDatabase(dsn, "postgres")
	if err != nil {
		return "", err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("connect to shard: %w", err)
	}
	defer conn.Close(ctx)

	var owner string
	err = conn.QueryRow(ctx,
		`SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1`, datname).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read database owner: %w", err)
	}
	return owner, nil
}
