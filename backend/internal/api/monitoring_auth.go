package api

import (
	"net/http"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// monitoringKeyPrefix is the recognizable prefix every monitoring ingest key
// carries. A presented token starting with it is treated as a scoped device key
// rather than a JWT. Single source of truth: telemetry.KeyPrefix.
const monitoringKeyPrefix = telemetry.KeyPrefix

// ingestKeyFromRequest extracts a presented monitoring ingest key from either
// the X-API-Key header or an "Authorization: Bearer <key>" header. Returns ""
// when no dmon_ key is present so the caller can fall back to JWT auth.
// Delegates to telemetry so the console and gateway share one parser.
func ingestKeyFromRequest(c *gin.Context) string {
	return telemetry.KeyFromHeaders(c.GetHeader("X-API-Key"), c.GetHeader("Authorization"))
}

// verifyMonitoringKeyHash checks a presented key against the stored
// salt(16)||digest(32) argon2id hash, in constant time. Delegates to telemetry.
func verifyMonitoringKeyHash(full string, stored []byte) bool {
	return telemetry.VerifyKeyHash(full, stored)
}

// IngestAuthMiddleware authenticates the device-facing monitoring ingest
// endpoints. A device presents its scoped monitoring key (dmon_...) via
// X-API-Key or Authorization: Bearer; the middleware loads the key's
// monitoring_apps row, binds it to the :appId in the path, verifies the
// argon2id hash, and synthesizes a *auth.Claims carrying the app's scopes (and
// the owning project's user as the actor). The existing RequireScope gate and
// the IngestMetrics/IngestLogs handlers then run unchanged.
//
// When no dmon_ key is present the request falls back to jwtNext (the normal
// JWT/Keycloak middleware) so a console user's token also works — handy for
// testing and for the future gateway-exchange path described in PRD-monitoring.
func (h *Handler) IngestAuthMiddleware(jwtNext gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := ingestKeyFromRequest(c)
		if key == "" {
			jwtNext(c)
			return
		}

		appID, err := uuid.Parse(c.Param("appId"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		var (
			prefix string
			hash   []byte
			scopes []string
			owner  *uuid.UUID
		)
		err = h.pool.QueryRow(c.Request.Context(),
			`SELECT m.api_key_prefix, m.api_key_hash, m.scopes, p.owner_id
			 FROM monitoring_apps m
			 JOIN projects p ON p.id = m.project_id
			 WHERE m.id = $1`,
			appID,
		).Scan(&prefix, &hash, &scopes, &owner)
		if err != nil {
			// Unknown app id or DB error — do not leak which; the key is unusable.
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}

		// Cheap prefix gate, then authoritative constant-time hash check.
		if !strings.HasPrefix(key, prefix) || !verifyMonitoringKeyHash(key, hash) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}

		claims := &auth.Claims{Scope: strings.Join(scopes, " ")}
		if owner != nil {
			claims.UserID = *owner
		}
		auth.SetClaims(c, claims)
		c.Next()
	}
}
