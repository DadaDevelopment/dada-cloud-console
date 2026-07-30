package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/box"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// The box credential.
//
// Shape copied from app_deploy_hooks (migration 039, deploy_hooks.go) line for
// line, and the copy is deliberate rather than lazy: that shape is the one place
// in this repository where a bearer credential is minted correctly — sha256 only
// at rest, a short plaintext prefix so a caller can tell two tokens apart, the
// plaintext returned exactly once, and withdrawal by revoked_at rather than
// DELETE. Inventing a second shape would mean auditing two.
//
// The contrast worth keeping in mind is CreateAppServerPayload.SSHPrivateKey,
// which has to be SCRUBBED from operations.payload once the operation goes
// terminal, precisely because it carries a live secret into a table that is
// long-lived, replicated and readable by every agent polling it. Box sessions
// were designed the other way round from the start, so there is no scrub step
// that can be forgotten.

// boxTokenPrefix marks a box session token's plaintext and lets one string
// pattern-match it apart from any other credential the platform mints.
const boxTokenPrefix = "dadabox_"

// boxTokenPrefixLen is how many leading plaintext characters are persisted as
// token_prefix: the marker plus 6 hex, enough to identify and useless to
// authenticate with.
const boxTokenPrefixLen = len(boxTokenPrefix) + 6

// Session lifetime. 12h by default because a box's own hard TTL is 8h, so a
// default session comfortably outlives the body it opens without becoming a
// standing credential; 168h is the ceiling for a caller that deliberately asks.
const (
	defaultBoxSessionHours = 12
	maxBoxSessionHours     = 168
)

// generateBoxToken mints a plaintext box session token plus its hash and prefix.
// The plaintext is returned to the caller exactly once and never persisted.
func generateBoxToken() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, 20)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	plaintext = boxTokenPrefix + hex.EncodeToString(buf)
	hash = hashBoxToken(plaintext)
	prefix = plaintext[:boxTokenPrefixLen]
	return plaintext, hash, prefix, nil
}

// hashBoxToken returns the hex sha256 of a box token plaintext — the only form
// ever persisted or compared against.
func hashBoxToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// boxSessionTTL clamps a requested session lifetime.
func boxSessionTTL(hours int) time.Duration {
	if hours <= 0 {
		hours = defaultBoxSessionHours
	}
	if hours > maxBoxSessionHours {
		hours = maxBoxSessionHours
	}
	return time.Duration(hours) * time.Hour
}

// mintBoxSession creates one session for a box and returns the plaintext.
func (h *Handler) mintBoxSession(ctx context.Context, boxID, projectID uuid.UUID, actor *uuid.UUID, ttl time.Duration) (plaintext string, prefix string, expiresAt time.Time, err error) {
	plaintext, hash, prefix, err := generateBoxToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	err = h.pool.QueryRow(ctx,
		`INSERT INTO box_sessions (box_id, project_id, token_hash, token_prefix, created_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, now() + ($6::int * INTERVAL '1 second'))
		 RETURNING expires_at`,
		boxID, projectID, hash, prefix, actor, int(ttl.Seconds()),
	).Scan(&expiresAt)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return plaintext, prefix, expiresAt, nil
}

// revokeBoxSessions marks every live session of a box revoked and returns how
// many it withdrew.
//
// CALLED BEFORE THE ENQUEUE in DeleteBox and SuspendBox, and the order is the
// whole point: an operation sits in a queue for as long as a worker takes to poll
// it, and a live credential that outlives the enqueue is a credential that opens
// a body the customer has already been told is gone. Revoking after the enqueue
// would leave exactly that window, and it would be invisible because both steps
// succeeded.
//
// SHUTTING THE BOX'S OWN DOOR IS PART OF REVOKING, not a follow-up. Since D6 the
// box runs its own endpoint and authenticates against a digest file inside itself,
// so a revocation that only updated this table would leave that file — and
// therefore that door — exactly as it was. The credential would keep working on the
// one path the control plane is deliberately not on, which is the worst possible
// place for a revocation to be incomplete.
//
// A failure to clear the file fails the revocation, for the same reason the
// ordering above is strict: a teardown that could not withdraw the credential must
// not report that it did.
func (h *Handler) revokeBoxSessions(ctx context.Context, boxID uuid.UUID) (int64, error) {
	tag, err := h.pool.Exec(ctx,
		`UPDATE box_sessions SET revoked_at = now()
		  WHERE box_id = $1 AND revoked_at IS NULL`, boxID)
	if err != nil {
		return 0, err
	}
	if err := h.clearBoxDoor(ctx, boxID); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// clearBoxDoor empties the digest file inside the box, if this host runs the box
// and gave it a door at all.
func (h *Handler) clearBoxDoor(ctx context.Context, boxID uuid.UUID) error {
	stack := h.boxStack
	if stack == nil || !stack.door.BrokerConfigured() {
		return nil
	}
	var instanceRef string
	if err := h.pool.QueryRow(ctx,
		`SELECT instance_ref FROM boxes WHERE id = $1`, boxID).Scan(&instanceRef); err != nil {
		return err
	}
	if instanceRef == "" {
		return nil
	}
	return stack.door.RevokeAllSessionDigests(ctx, &box.Instance{ID: boxID.String(), InstanceRef: instanceRef})
}

// resolvedBoxSession is the tenancy a box session token resolves to.
type resolvedBoxSession struct {
	SessionID   uuid.UUID
	BoxID       uuid.UUID
	ProjectID   uuid.UUID
	BoxName     string
	InstanceRef string
	Status      string
}

// extractBoxToken reads the caller's box token, preferring the dedicated header
// and falling back to a bearer Authorization. Mirrors extractDeployToken: a
// non-"Bearer "-prefixed Authorization header is treated as absent rather than as
// a literal token.
func extractBoxToken(c *gin.Context) string {
	if tok := c.GetHeader("X-Dada-Box-Token"); tok != "" {
		return tok
	}
	header := c.GetHeader("Authorization")
	raw := strings.TrimPrefix(header, "Bearer ")
	if raw == "" || raw == header {
		return ""
	}
	return raw
}

// boxSessionFromToken authenticates a box-session request. On failure it has
// already written the response and returns ok=false.
//
// Expiry is enforced in the WHERE clause rather than in Go: the sweeper runs once
// a minute, so between sweeps an expired row still exists, and a check that lived
// only in the sweeper would accept a credential for up to a minute past its own
// deadline.
func (h *Handler) boxSessionFromToken(c *gin.Context) (resolvedBoxSession, bool) {
	raw := extractBoxToken(c)
	if raw == "" {
		respondUnauthorized(c)
		return resolvedBoxSession{}, false
	}
	var s resolvedBoxSession
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT bs.id, bs.box_id, bs.project_id, b.name, b.instance_ref, b.status
		   FROM box_sessions bs
		   JOIN boxes b ON b.id = bs.box_id
		  WHERE bs.token_hash = $1
		    AND bs.revoked_at IS NULL
		    AND bs.expires_at > now()
		    AND b.status <> 'Deleted'`,
		hashBoxToken(raw),
	).Scan(&s.SessionID, &s.BoxID, &s.ProjectID, &s.BoxName, &s.InstanceRef, &s.Status)
	if err == pgx.ErrNoRows {
		respondUnauthorized(c)
		return resolvedBoxSession{}, false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve box session")
		return resolvedBoxSession{}, false
	}
	// Best-effort: a failed touch must not fail the caller's command.
	_, _ = h.pool.Exec(c.Request.Context(),
		`UPDATE box_sessions SET last_used_at = now() WHERE id = $1`, s.SessionID)
	return s, true
}

// StartBoxSessionSweeper revokes expired sessions once a minute.
//
// The sweeper is housekeeping, NOT the enforcement point — boxSessionFromToken
// filters on expires_at itself. Keeping both is deliberate: without the query
// filter an expired token would work until the next tick, and without the sweeper
// box_sessions would keep rows that look live forever, so "which credentials are
// live" would stop being answerable by a WHERE clause.
func (h *Handler) StartBoxSessionSweeper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tag, err := h.pool.Exec(ctx,
					`UPDATE box_sessions SET revoked_at = now()
					  WHERE revoked_at IS NULL AND expires_at <= now()`)
				if err != nil {
					log.Warn().Err(err).Msg("box: session sweep failed")
					continue
				}
				if n := tag.RowsAffected(); n > 0 {
					log.Info().Int64("revoked", n).Msg("box: swept expired sessions")
				}
			}
		}
	}()
}

// mcpServersSnippet renders the ready-to-paste mcpServers block for the box.
//
// It points at the BOX's own endpoint, never at ours, and that is a product
// decision written into the code: the customer's agent keeps its brain local and
// talks to the box directly, so their source and their model credentials never
// traverse our API. A control-plane boxExec tool would route every keystroke of
// their work through us — the exact opposite of the promise — which is why no such
// tool exists on this surface and no keep-list entry can create one.
func mcpServersSnippet(boxName, mcpURL, token string) map[string]any {
	return map[string]any{
		"mcpServers": map[string]any{
			"dada-box-" + boxName: map[string]any{
				"type": "http",
				"url":  mcpURL,
				"headers": map[string]string{
					"Authorization": "Bearer " + token,
				},
			},
		},
	}
}

// boxSSHCommand renders the copy-paste ssh command for a box.
func boxSSHCommand(host string, port *int) string {
	if host == "" {
		return ""
	}
	p := 22
	if port != nil {
		p = *port
	}
	return fmt.Sprintf("ssh -p %d root@%s", p, host)
}
