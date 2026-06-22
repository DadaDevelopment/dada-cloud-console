package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	internalmcp "github.com/dada-tuda/console/backend/internal/mcp"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// authMiddleware selects the request-auth middleware by cfg.AuthMode. Default
// ("local" or unset) returns the existing HS256 GinMiddleware so behavior is
// unchanged. "keycloak" builds a JWKS verifier from KEYCLOAK_ISSUER and resolves
// each token to a local users.id via auth.ResolveUser, populating the same
// *auth.Claims the handlers already read.
func authMiddleware(pool *pgxpool.Pool, cfg *config.Config) gin.HandlerFunc {
	if cfg.AuthMode != "keycloak" {
		return auth.GinMiddleware(cfg.JWTSecret)
	}

	verifier, err := auth.NewKeycloakVerifier(
		context.Background(),
		cfg.KeycloakIssuer,
		cfg.KeycloakVerifyAud,
		cfg.KeycloakAudience,
		cfg.KeycloakRolesClient,
	)
	if err != nil {
		// AUTH_MODE=keycloak is an explicit operator choice; a misconfigured
		// issuer must fail loudly at startup rather than silently fall back to a
		// path that can't validate any token.
		panic(fmt.Sprintf("auth: build keycloak verifier: %v", err))
	}

	resolver := func(c *gin.Context, kc *auth.KeycloakClaims) (*auth.Claims, error) {
		id, err := auth.ResolveUser(c.Request.Context(), pool, kc)
		if err != nil {
			return nil, err
		}
		// Authorization is decoded from the native Keycloak claims (group paths +
		// scope). The /platform-admins staff god-mode is handled inside the claim
		// decode, not here (ADR-009 §4).
		return &auth.Claims{
			UserID:      id,
			Username:    kc.PreferredUsername,
			Email:       kc.Email,
			DisplayName: kc.Name,
			Groups:      kc.Groups,
			Roles:       kc.Roles,
			Scope:       kc.Scope,
		}, nil
	}

	return auth.KeycloakMiddleware(verifier, resolver)
}

// SetupRouter configures and returns the Gin engine with all API routes registered.
func SetupRouter(pool *pgxpool.Pool, cfg *config.Config) *gin.Engine {
	if !cfg.DevMode {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	// CORS middleware
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	h := NewHandler(pool, cfg)

	keycloakMode := cfg.AuthMode == "keycloak"

	// Public routes. In keycloak mode local login is disabled (auth is via
	// Keycloak); the route stays registered but returns 410 Gone so the OpenAPI
	// coverage gate and clients still see a defined endpoint.
	if keycloakMode {
		r.POST("/api/v1/auth/login", func(c *gin.Context) {
			c.JSON(http.StatusGone, gin.H{"error": "local login disabled; authenticate via Keycloak"})
		})
	} else {
		r.POST("/api/v1/auth/login", h.Login)
	}

	// Embedded OpenAPI spec (public — feeds the reflective MCP server).
	r.GET("/openapi.json", ServeOpenAPISpec)

	// Embedded MCP server at /mcp (Streamable HTTP transport).
	// Each tool call self-proxies to cfg.MCPSelfURL/api/v1/... so auth and all
	// middleware apply unchanged. Disabled via MCP_ENABLED=false.
	if cfg.MCPEnabled {
		selfURL := cfg.MCPSelfURL
		if selfURL == "" {
			selfURL = "http://127.0.0.1:" + cfg.Port
		}
		mcpHandler, err := internalmcp.NewHandler(EmbeddedSpec(), internalmcp.Config{
			BackendURL:     selfURL,
			OverridesPath:  cfg.MCPOverridesPath,
			ResourceURL:    cfg.MCPResourceURL,
			KeycloakIssuer: cfg.KeycloakIssuer,
		})
		if err != nil {
			log.Fatalf("mcp: failed to initialize: %v", err)
		}
		r.Any("/mcp", gin.WrapH(http.StripPrefix("/mcp", mcpHandler)))
		r.Any("/mcp/*path", gin.WrapH(http.StripPrefix("/mcp", mcpHandler)))
		log.Printf("mcp: serving at /mcp (self-proxy -> %s)", selfURL)
	}

	// Internal server-to-server API (ADR-009). user-service calls this when it
	// mints a project. Guarded by a shared secret, NOT the user JWT middleware.
	// Disabled when INTERNAL_AUTH_TOKEN is unset.
	if cfg.InternalAuthToken != "" {
		internal := r.Group("/internal", requireInternalToken(cfg.InternalAuthToken))
		internal.POST("/projects", h.ProvisionProject)
		log.Printf("internal: provisioning API enabled at /internal")
	}

	// Authenticated routes — pick the auth middleware by configured mode.
	// Built once and shared: the monitoring ingest group reuses it as its JWT
	// fallback, so we don't construct a second Keycloak verifier/JWKS client.
	authMW := authMiddleware(pool, cfg)
	api := r.Group("/api/v1", authMW)
	{
		// Auth
		api.GET("/auth/me", h.Me)

		// Projects
		api.GET("/projects", h.ListProjects)
		api.GET("/projects/:projectId", h.GetProject)

		// Namespace policies (project settings — LimitRange + ResourceQuota)
		api.PUT("/projects/:projectId/environments/:envId/namespace-policy", h.SetNamespacePolicy)

		// Databases (ServiceDatabase CRD)
		api.GET("/projects/:projectId/environments/:envId/databases", h.ListDatabases)
		api.POST("/projects/:projectId/environments/:envId/databases", h.CreateServiceDatabase)

		// Object Storage (S3Bucket XR)
		api.GET("/projects/:projectId/environments/:envId/s3buckets", h.ListS3Buckets)
		api.POST("/projects/:projectId/environments/:envId/s3buckets", h.CreateS3Bucket)

		// AppServers (VM track)
		api.GET("/projects/:projectId/app-servers", h.ListAppServers)
		api.POST("/projects/:projectId/app-servers", h.CreateAppServer)
		api.GET("/projects/:projectId/app-servers/:serverName", h.GetAppServer)
		api.GET("/projects/:projectId/app-servers/:serverName/state", h.GetAppServerState)
		api.GET("/projects/:projectId/app-servers/:serverName/metrics", h.GetAppServerMetrics)
		api.DELETE("/projects/:projectId/app-servers/:serverName", h.DeleteAppServer)

		// Apps
		api.GET("/projects/:projectId/environments/:envId/apps", h.ListApps)
		api.POST("/projects/:projectId/environments/:envId/apps", h.CreateApp)
		api.PATCH("/projects/:projectId/environments/:envId/apps/:appName/image", h.UpdateAppImage)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/values-token", h.GetValuesToken)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/state", h.GetAppState)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/logs", h.GetAppLogs)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/metrics", h.GetAppMetrics)

		// Aggregated log search (Elasticsearch/filebeat proxy, read-only).
		api.GET("/projects/:projectId/logs", h.SearchLogs)

		// Endpoints (PublicApi CRD)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/endpoints", h.ListEndpoints)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/endpoints", h.CreateEndpoint)

		// Custom domains (user-owned domains + auto TLS, Vercel-style two-level model).
		// Level 1: apex authorization (project-scoped). Level 2: hostname attachment (app-scoped).
		api.GET("/projects/:projectId/domain-authorizations", h.ListDomainAuthorizations)
		api.POST("/projects/:projectId/domain-authorizations", h.AddDomainAuthorization)
		api.POST("/projects/:projectId/domain-authorizations/:id/verify", h.VerifyDomainAuthorization)
		api.DELETE("/projects/:projectId/domain-authorizations/:id", h.DeleteDomainAuthorization)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/hostnames", h.ListHostnames)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/hostnames", h.AttachHostname)
		api.DELETE("/projects/:projectId/environments/:envId/apps/:appName/hostnames/:id", h.DetachHostname)

		// Vercel-flow: git repos, builds, deployments, env vars.
		// Git provider installations + remote repo listing (build-agent proxy).
		api.GET("/projects/:projectId/git/installations", h.ListGitInstallations)
		api.GET("/projects/:projectId/git/install-url", h.GetGitInstallURL)
		api.GET("/projects/:projectId/git/installations/:installationId/repos", h.ListInstallationRepos)
		api.GET("/projects/:projectId/git/installations/:installationId/detect", h.DetectFramework)
		// Git repos linked per environment.
		api.GET("/projects/:projectId/environments/:envId/repos", h.ListGitRepos)
		api.POST("/projects/:projectId/environments/:envId/repos", h.ConnectGitRepo)
		api.DELETE("/projects/:projectId/environments/:envId/repos/:repoId", h.DisconnectGitRepo)
		// Builds (imperative — no operations). Scope-gated per ADR-009 vocabulary.
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/builds", auth.RequireScope("builds:read"), h.ListBuilds)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/builds", auth.RequireScope("builds:write"), h.TriggerBuild)
		api.GET("/projects/:projectId/builds/:buildId", auth.RequireScope("builds:read"), h.GetBuild)
		api.POST("/projects/:projectId/builds/:buildId/cancel", auth.RequireScope("builds:write"), h.CancelBuild)
		api.POST("/projects/:projectId/builds/:buildId/logs-token", auth.RequireScope("builds:read"), h.GetBuildLogsToken)
		// Mobile artifacts (APK/AAB) — list + scope-gated proxied download (ADR-010).
		api.GET("/projects/:projectId/builds/:buildId/artifacts", auth.RequireScope("builds:read"), h.ListBuildArtifacts)
		api.GET("/projects/:projectId/builds/:buildId/artifacts/:artifactId/download", auth.RequireScope("builds:read"), h.DownloadBuildArtifact)
		// Deployments (rollback/promote enqueue DeployImageVersion operations).
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/deployments", h.ListDeployments)
		api.POST("/projects/:projectId/deployments/:deploymentId/rollback", auth.RequireScope("deploy:write"), h.RollbackDeployment)
		api.POST("/projects/:projectId/deployments/:deploymentId/promote", auth.RequireScope("deploy:write"), h.PromoteDeployment)
		// Env vars (always encrypted at rest; reveal is write-gated).
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/env", h.ListEnvVars)
		api.PUT("/projects/:projectId/environments/:envId/apps/:appName/env/:key", h.SetEnvVar)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/env/:key", h.RevealEnvVar)
		api.DELETE("/projects/:projectId/environments/:envId/apps/:appName/env/:key", h.DeleteEnvVar)

		// Monitoring write path (PRD-monitoring / ADR-011).
		// Management (user-authenticated): list + create monitoring resources.
		api.GET("/projects/:projectId/monitoring", h.ListMonitoringApps)
		api.POST("/projects/:projectId/environments/:envId/monitoring", h.CreateMonitoringApp)
		// Device-facing ingest routes are registered on a separate group below
		// (h.IngestAuthMiddleware) so a scoped dmon_ key authenticates directly,
		// bypassing the JWT-only group middleware. See PRD-monitoring contract
		// POST /projects/{projectId}/monitoring/{appId}/{metrics,logs}.

		// Monitoring read/alert/health layer (ADR-011): read-back, health,
		// dashboards, channels, alert rules and resource teardown. Handlers
		// gracefully 503 when Grafana/Prometheus/ES are unconfigured.
		mon := "/projects/:projectId/environments/:envId/monitoring"
		api.GET(mon+"/channels", h.ListChannels)
		api.POST(mon+"/channels", h.CreateChannel)
		api.DELETE(mon+"/channels/:id", h.DeleteChannel)
		api.GET(mon+"/:appId", h.GetMonitoringApp)
		api.DELETE(mon+"/:appId", h.DeleteMonitoringApp)
		api.GET(mon+"/:appId/health", h.GetMonitoringHealth)
		api.GET(mon+"/:appId/metrics", h.GetMonitoringMetrics)
		api.GET(mon+"/:appId/logs", h.GetMonitoringLogs)
		api.GET(mon+"/:appId/grafana-link", h.GetMonitoringGrafanaLink)
		api.GET(mon+"/:appId/alert-rules", h.ListAlertRules)
		api.POST(mon+"/:appId/alert-rules", h.CreateAlertRule)
		api.DELETE(mon+"/:appId/alert-rules/:ruleId", h.DeleteAlertRule)

		// Operations
		api.GET("/projects/:projectId/operations", h.GetProjectOperations)
		api.GET("/projects/:projectId/operations/:operationId", h.GetOperation)
		api.POST("/projects/:projectId/operations/:operationId/retry", h.RetryOperation)

		// AI Studio (v2). Routes are registered always; handlers gracefully
		// degrade when AI_STUDIO_ENABLED is false / MLflow is not configured.
		if cfg.AIStudioEnabled {
			// Quotas
			api.GET("/projects/:projectId/quotas", h.GetProjectQuotas)

			// AIModel CRUD
			api.GET("/projects/:projectId/environments/:envId/models", h.ListAIModels)
			api.POST("/projects/:projectId/environments/:envId/models", h.CreateAIModel)
			api.GET("/projects/:projectId/environments/:envId/models/:name", h.GetAIModel)
			api.DELETE("/projects/:projectId/environments/:envId/models/:name", h.DeleteAIModel)
			api.PATCH("/projects/:projectId/environments/:envId/models/:name/artifact", h.UpdateAIModelArtifact)
			api.PATCH("/projects/:projectId/environments/:envId/models/:name/canary", h.SetCanaryTraffic)
			api.POST("/projects/:projectId/environments/:envId/models/:name/promote", h.PromoteAIModel)
			api.PATCH("/projects/:projectId/environments/:envId/models/:name/mlflow-pin", h.PinAIModelMlflowVersion)
			api.GET("/projects/:projectId/environments/:envId/models/:name/api-key", h.RevealAIModelAPIKey)

			// MLflow proxy (read-only)
			api.GET("/mlflow/registered-models", h.ListMLflowRegisteredModels)
			api.GET("/mlflow/registered-models/:name/versions", h.ListMLflowModelVersions)
			api.GET("/mlflow/registered-models/:name/versions/:version", h.GetMLflowModelVersion)

			// Admin approvals (generic — first consumer is the GPU gate above).
			api.GET("/admin/operations", h.ListAdminApprovals)
			api.POST("/admin/operations/:opId/approve", h.ApproveOperation)
			api.POST("/admin/operations/:opId/reject", h.RejectOperation)

			// Inference proxy (playground only — production traffic goes via PublicApi ingress).
			api.POST("/projects/:projectId/environments/:envId/models/:name/infer", h.ProxyInference)
		}
	}

	// Device-facing monitoring ingest. Separate group so a scoped dmon_ key
	// authenticates directly (IngestAuthMiddleware), with the standard JWT/
	// Keycloak middleware as fallback for console/testing callers. RequireScope
	// still gates metrics:write / logs:write from the synthesized claims.
	ingest := r.Group("/api/v1", h.IngestAuthMiddleware(authMW))
	{
		ingest.POST("/projects/:projectId/monitoring/:appId/metrics", auth.RequireScope("metrics:write"), h.IngestMetrics)
		ingest.POST("/projects/:projectId/monitoring/:appId/logs", auth.RequireScope("logs:write"), h.IngestLogs)
	}

	// Liveness — process is up. Cheap, no dependencies. K8s restarts the pod
	// only on a hard hang (server can't even answer this).
	liveness := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) }
	r.GET("/health", liveness)
	r.GET("/healthz", liveness)

	// Readiness — process is up AND can talk to Postgres. K8s removes the pod
	// from the Service endpoints when this fails, so a pod that's lost its DB
	// stops receiving traffic instead of returning 500s. 1s timeout keeps the
	// probe path fast even when the DB is degraded.
	r.GET("/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 1*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db_unreachable", "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	// Self-heal Grafana-provisioned alert rules after a Grafana restart wipes
	// them (shared Grafana runs on emptyDir). No-op when Grafana is unconfigured.
	h.StartGrafanaReconciler(context.Background(), defaultReconcileInterval)

	return r
}
