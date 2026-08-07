package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	internalmcp "github.com/dada-tuda/console/backend/internal/mcp"
	"github.com/dada-tuda/console/backend/internal/metrics"
	"github.com/dada-tuda/console/backend/internal/notify"
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

	signupNotifier := notify.New(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)

	resolver := func(c *gin.Context, kc *auth.KeycloakClaims) (*auth.Claims, error) {
		id, created, err := auth.ResolveUser(c.Request.Context(), pool, kc)
		if err != nil {
			return nil, err
		}
		if created {
			notifySignup(pool, signupNotifier, cfg.SignupNotifyEmail, kc)
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
			SessionID:   kc.SessionID,
		}, nil
	}

	return auth.KeycloakMiddleware(verifier, resolver)
}

func optionalAuthResolver(pool *pgxpool.Pool, cfg *config.Config) func(c *gin.Context) (*auth.Claims, bool) {
	extractBearer := func(c *gin.Context) string {
		h := c.GetHeader("Authorization")
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			return ""
		}
		return parts[1]
	}

	if cfg.AuthMode != "keycloak" {
		return func(c *gin.Context) (*auth.Claims, bool) {
			token := extractBearer(c)
			if token == "" {
				return nil, false
			}
			claims, err := auth.ValidateToken(token, cfg.JWTSecret)
			if err != nil {
				return nil, false
			}
			return claims, true
		}
	}

	verifier, err := auth.NewKeycloakVerifier(
		context.Background(),
		cfg.KeycloakIssuer,
		cfg.KeycloakVerifyAud,
		cfg.KeycloakAudience,
		cfg.KeycloakRolesClient,
	)
	if err != nil {
		return func(c *gin.Context) (*auth.Claims, bool) { return nil, false }
	}

	return func(c *gin.Context) (*auth.Claims, bool) {
		token := extractBearer(c)
		if token == "" {
			return nil, false
		}
		kc, verr := verifier.Verify(c.Request.Context(), token)
		if verr != nil {
			return nil, false
		}
		id, _, rerr := auth.ResolveUser(c.Request.Context(), pool, kc)
		if rerr != nil {
			return nil, false
		}
		return &auth.Claims{UserID: id, Groups: kc.Groups}, true
	}
}

// SetupRouter configures and returns the Gin engine with all API routes registered.
func SetupRouter(pool *pgxpool.Pool, cfg *config.Config) *gin.Engine {
	if !cfg.DevMode {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(metrics.HTTPMiddleware())

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
	h.optionalAuth = optionalAuthResolver(pool, cfg)

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

	// Embedded OpenAPI spec (public -- feeds the reflective MCP server).
	r.GET("/openapi.json", ServeOpenAPISpec)

	// Status Radar (RU-vantage competitor availability probe, powers the
	// /status acquisition landing). Public on purpose -- measured data about
	// third-party public homepages, not tenant data; registered outside
	// /api/v1 like /health and /metrics, so it is also outside the auth
	// middleware and the OpenAPI coverage gate.
	r.GET("/api/public/status", h.PublicStatusRadar)

	// Prometheus scrape endpoint. Public on purpose: aggregate state gauges
	// (operations/domain health), no per-tenant data. Scraped in-cluster by the
	// kube-prometheus-stack ServiceMonitor; the public ingress does not route it.
	r.GET("/metrics", gin.WrapH(metrics.Handler()))

	// GitHub App install callback (Setup URL). Public — GitHub redirects an
	// anonymous browser here after install; trust is the HMAC-signed state, not
	// a session. Lives outside the JWT group on purpose.
	r.GET("/api/v1/git/install/callback", h.GitInstallCallback)
	r.GET("/api/v1/git/github/oauth/callback", h.GitHubOAuthCallback)
	r.GET("/api/v1/payments/oauth/callback", h.PaymentsOAuthCallback)

	// DadaAgent cloud-task webhook callback (status/artifacts). Public route on
	// purpose — it is bearer-gated by a Keycloak JWKS verifier inside the handler
	// (only the agent's own client, azp=dada-agent, is accepted), not by the user
	// JWT middleware. Disabled when KEYCLOAK_ISSUER is unset / verifier build fails.
	if v, err := auth.NewKeycloakVerifier(context.Background(), cfg.KeycloakIssuer, false, "", "dada-agent"); err == nil {
		h.agentVerifier = v
		r.POST("/api/v1/webhooks/dadagent", h.DadaAgentWebhook)
		r.POST("/api/v1/webhooks/dadagent/usage", h.DadaAgentUsageWebhook)
		log.Printf("cloud-task: dadagent webhook enabled at /api/v1/webhooks/dadagent")

		// Agent-facing read-only git discovery (repo/branch picker in
		// agent_sync_hub). Same azp=dada-agent gate as the webhook above.
		r.GET("/api/v1/agent/git/installations", h.AgentListGitInstallations)
		r.GET("/api/v1/agent/git/repos", h.AgentListInstallationRepos)
		r.GET("/api/v1/agent/git/branches", h.AgentListInstallationBranches)
	} else {
		log.Printf("cloud-task: dadagent webhook disabled (keycloak verifier: %v)", err)
	}

	// box-agent ingest webhooks (status transitions + out-of-guest activity
	// samples). Same shape and same reasons as the dadagent webhook above:
	// public route, bearer-gated by a JWKS verifier inside the handler
	// (azp=box-agent only), NOT the user JWT middleware, and registered only when
	// the verifier builds.
	//
	// The guard is load-bearing beyond configuration. These two handlers carry no
	// swaggo annotation, and the guard is what makes that safe: under the
	// coverage test's config no verifier builds, so the routes are not registered
	// and openapi_coverage_test.go has nothing to demand a spec entry for. It also
	// keeps them out of swagger.json, and therefore out of the reflected MCP
	// toolset — whose standalone server curates by denylist, so a spec entry would
	// become an agent-callable "write a box's billing sample" tool by default.
	if v, err := auth.NewKeycloakVerifier(context.Background(), cfg.KeycloakIssuer, false, "", "box-agent"); err == nil {
		h.boxAgentVerifier = v
		r.POST("/api/v1/webhooks/boxagent/status", h.BoxAgentStatusWebhook)
		r.POST("/api/v1/webhooks/boxagent/sample", h.BoxAgentSampleWebhook)
		log.Printf("box: box-agent webhooks enabled at /api/v1/webhooks/boxagent/{status,sample}")
	} else {
		log.Printf("box: box-agent webhooks disabled (keycloak verifier: %v)", err)
	}

	// Deploy-hook consumption routes. Public on purpose: authenticated by a
	// revocable per-app bearer token (app_deploy_hooks.token_hash) resolved
	// inside the handler, not the Keycloak user JWT middleware -- this is how
	// external CI (e.g. GitHub Actions) deploys a prebuilt image without a
	// console session. Management (mint/list/revoke) stays inside the JWT
	// group above, under .../apps/:appName/deploy-hooks.
	r.POST("/api/v1/deploy", h.DeployTrigger)
	r.GET("/api/v1/deploy/operations/:operationId", h.GetDeployOperation)

	r.POST("/api/v1/webhooks/yookassa", h.YooKassaWebhook)

	r.POST("/api/v1/client-errors", h.ReportClientError)

	r.POST("/api/v1/feedback", h.SubmitFeedback)

	r.POST("/api/v1/telemetry/events", h.RecordUXEvents)

	r.POST("/api/v1/promo/click", h.RecordPromoClick)

	// Dada Box fake-door funnel ingest. Public on purpose: the /box landing is a
	// marketing page with no session, and its route handler forwards events
	// server-to-server. Guarded by a per-IP + global token bucket inside the
	// handler, a closed event-name set and a capped body — not by the user JWT
	// middleware. Deliberately NOT in the MCP keep-list: it is an ingest written
	// by a landing page, not something an agent calls.
	r.POST("/api/v1/box/leads", h.RecordBoxFunnelEvent)

	// The box's OWN door: run a command inside the box, authenticated by the box's
	// "dadabox_" session token rather than by a console session.
	//
	// Registered only when the box runtime is configured, and carrying no swaggo
	// annotation — the same construction as the box-agent webhooks above, and the
	// guard is what makes the missing annotation safe: under the coverage test's
	// config no runtime is configured, so these routes are not registered and the
	// gate has nothing to demand a spec entry for. It also keeps them out of
	// swagger.json and therefore out of the REFLECTED MCP toolset, whose standalone
	// server curates by denylist — so an annotated exec endpoint would become an
	// agent-callable "run a command in a box" tool on our control plane by default.
	//
	// That is a product decision (D6), not a security nicety: the customer's agent
	// keeps its brain local and talks to the box, so their code and model
	// credentials never traverse our API. In production this surface is
	// cmd/box-broker at the box's own hostname (backlog phase 4).
	if h.boxStack != nil {
		r.POST("/api/v1/box/session/exec", h.BoxSessionExec)
		r.GET("/api/v1/box/session/info", h.BoxSessionInfo)
		log.Printf("box: session surface enabled at /api/v1/box/session/{exec,info} (LocalRuntime stand-in for cmd/box-broker)")
	}

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
			RequireBearer:  keycloakMode,
		})
		if err != nil {
			log.Fatalf("mcp: failed to initialize: %v", err)
		}
		mcpServe := func(c *gin.Context) {
			req := c.Request
			p := strings.TrimPrefix(req.URL.Path, "/mcp")
			if p == "" {
				p = "/"
			}
			req.URL.Path = p
			mcpHandler.ServeHTTP(c.Writer, req)
		}
		r.Any("/mcp", mcpServe)
		r.Any("/mcp/*path", mcpServe)

		metaHandler := gin.WrapH(internalmcp.MetadataHandler(internalmcp.Config{
			ResourceURL:    cfg.MCPResourceURL,
			KeycloakIssuer: cfg.KeycloakIssuer,
		}))
		r.GET("/.well-known/oauth-protected-resource", metaHandler)
		r.GET("/.well-known/oauth-protected-resource/mcp", metaHandler)

		log.Printf("mcp: serving at /mcp (self-proxy -> %s); RFC 9728 metadata at host root", selfURL)
	}

	// Internal server-to-server API (ADR-009). user-service calls this when it
	// mints a project. Guarded by a shared secret, NOT the user JWT middleware.
	// Disabled when INTERNAL_AUTH_TOKEN is unset.
	if cfg.InternalAuthToken != "" {
		internal := r.Group("/internal", requireInternalToken(cfg.InternalAuthToken))
		internal.POST("/projects", h.ProvisionProject)
		internal.POST("/backfill/project-groups", h.BackfillProjectGroups)
		internal.POST("/ai/credential/set", h.AISetProviderCredential)
		internal.POST("/ai/credential/get", h.AIGetProviderCredential)
		internal.POST("/ai/usage/record", h.AIRecordUsage)
		internal.POST("/ai/failure/record", h.AIRecordFailure)
		internal.POST("/ai/key/introspect", h.AIIntrospectKey)
		internal.POST("/identity/introspect", h.IntrospectServiceIdentity)
		internal.GET("/db/routes.ini", h.DBRoutes)
		log.Printf("internal: provisioning API enabled at /internal")
	}

	// Authenticated routes — pick the auth middleware by configured mode.
	// Built once and shared: the monitoring ingest group reuses it as its JWT
	// fallback, so we don't construct a second Keycloak verifier/JWKS client.
	authMW := authMiddleware(pool, cfg)
	api := r.Group("/api/v1", authMW, h.auditSessionMiddleware())
	{
		// Auth
		api.GET("/auth/me", h.Me)

		// Projects
		api.GET("/projects", h.ListProjects)
		api.POST("/projects", h.CreateProject)
		api.POST("/projects/default", h.EnsureDefaultProject)
		api.GET("/projects/:projectId", h.GetProject)
		api.GET("/projects/:projectId/delete-impact", h.DeleteProjectImpact)
		api.DELETE("/projects/:projectId", h.DeleteProject)

		// Namespace policies (project settings — LimitRange + ResourceQuota)
		api.PUT("/projects/:projectId/environments/:envId/namespace-policy", h.SetNamespacePolicy)

		// Databases (ServiceDatabase CRD)
		api.GET("/projects/:projectId/environments/:envId/databases", h.ListDatabases)
		api.POST("/projects/:projectId/environments/:envId/databases", h.CreateServiceDatabase)
		api.DELETE("/projects/:projectId/environments/:envId/databases/:name", h.DeleteServiceDatabase)
		api.GET("/projects/:projectId/environments/:envId/databases/:name/backups", h.ListDBBackups)
		api.POST("/projects/:projectId/environments/:envId/databases/:name/backups", h.CreateDBBackup)
		api.GET("/projects/:projectId/environments/:envId/databases/:name/backups/:backupId/download", h.DownloadDBBackup)
		api.POST("/projects/:projectId/environments/:envId/databases/:name/restore", h.RestoreServiceDatabase)
		api.GET("/projects/:projectId/environments/:envId/databases/:name/credentials", h.GetDatabaseCredentials)
		api.GET("/projects/:projectId/environments/:envId/databases/:name/insights", h.GetDatabaseInsights)
		api.GET("/projects/:projectId/environments/:envId/databases/:name/tables", h.ListDatabaseTables)
		api.GET("/projects/:projectId/environments/:envId/databases/:name/queries", h.ListDatabaseQueries)
		api.GET("/projects/:projectId/environments/:envId/databases/:name/advisories", h.ListDatabaseAdvisories)
		api.POST("/projects/:projectId/environments/:envId/ingress", h.CreateIngress)

		// Object Storage (S3Bucket XR)
		api.GET("/projects/:projectId/environments/:envId/s3buckets", h.ListS3Buckets)
		api.POST("/projects/:projectId/environments/:envId/s3buckets", h.CreateS3Bucket)
		api.GET("/projects/:projectId/environments/:envId/s3buckets/:name/credentials", h.GetS3BucketCredentials)

		// AppServers (VM track)
		api.GET("/projects/:projectId/app-servers", h.ListAppServers)
		api.POST("/projects/:projectId/app-servers", h.CreateAppServer)
		api.GET("/projects/:projectId/app-servers/:serverName", h.GetAppServer)
		api.GET("/projects/:projectId/app-servers/:serverName/state", h.GetAppServerState)
		api.GET("/projects/:projectId/app-servers/:serverName/metrics", h.GetAppServerMetrics)
		api.DELETE("/projects/:projectId/app-servers/:serverName", h.DeleteAppServer)
		api.POST("/projects/:projectId/app-servers/:serverName/discover", h.DiscoverWorkload)
		api.POST("/projects/:projectId/app-servers/:serverName/import", h.ImportComposeStack)

		// Boxes (ephemeral root sandboxes). A box owns exactly one environment
		// with runtime='box'; crystallization later promotes that same row to
		// runtime='vm', which is how its attachments and hostnames survive.
		api.GET("/projects/:projectId/boxes", h.ListBoxes)
		api.POST("/projects/:projectId/boxes", h.CreateBox)
		api.GET("/projects/:projectId/boxes/:boxName", h.GetBox)
		api.GET("/projects/:projectId/boxes/:boxName/state", h.GetBoxState)
		api.DELETE("/projects/:projectId/boxes/:boxName", h.DeleteBox)
		api.POST("/projects/:projectId/boxes/:boxName/suspend", h.SuspendBox)
		api.POST("/projects/:projectId/boxes/:boxName/resume", h.ResumeBox)
		api.POST("/projects/:projectId/boxes/:boxName/extend", h.ExtendBox)
		// Metered minutes and money for one box, read from the per-minute ledger
		// (migration 063). Read-only, member-visible: a customer must be able to see
		// what they are being billed for without asking us.
		api.GET("/projects/:projectId/boxes/:boxName/usage", h.GetBoxUsage)

		// The single-call door. The path is /box-up rather than /boxes/up because
		// gin's router refuses a static segment beside an existing wildcard at the
		// same position, and /boxes/:boxName already owns that slot. Worth stating so
		// nobody "tidies" it into a panic at startup.
		api.POST("/projects/:projectId/box-up", h.BoxUp)
		api.GET("/projects/:projectId/boxes/:boxName/connection", h.GetBoxConnection)
		api.GET("/box/catalog", h.GetBoxCatalog)

		// attach / expose / crystallize. These three drive the runtime seams
		// synchronously; see the file comments in boxes_attach.go and
		// boxes_crystallize.go for exactly where that diverges from the async
		// operations convention and why.
		api.POST("/projects/:projectId/boxes/:boxName/attach/database", h.AttachBoxDatabase)
		api.GET("/projects/:projectId/boxes/:boxName/attachments", h.ListBoxAttachments)
		api.POST("/projects/:projectId/boxes/:boxName/expose", h.ExposeBox)
		api.POST("/projects/:projectId/boxes/:boxName/crystallize", h.CrystallizeBox)
		api.GET("/projects/:projectId/boxes/:boxName/crystallizations", h.ListBoxCrystallizations)

		// Apps
		api.GET("/projects/:projectId/environments/:envId/apps", h.ListApps)
		api.GET("/projects/:projectId/environments/:envId/infra", h.ListInfra)
		api.POST("/projects/:projectId/environments/:envId/apps", h.CreateApp)
		api.PATCH("/projects/:projectId/environments/:envId/apps/:appName/image", h.UpdateAppImage)
		api.PATCH("/projects/:projectId/environments/:envId/apps/:appName/profile", h.UpdateAppProfile)
		api.PUT("/projects/:projectId/environments/:envId/apps/:appName/storage", h.UpdateAppStorage)
		api.PATCH("/projects/:projectId/environments/:envId/apps/:appName/compose-config", h.UpdateComposeConfig)
		api.PUT("/projects/:projectId/environments/:envId/apps/:appName/compose-volume", h.UpdateComposeVolume)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/rollback", h.RollbackApp)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/restart", h.RestartApp)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/identity", h.CreateAppServiceIdentity)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/identity", h.GetAppServiceIdentity)
		api.PATCH("/projects/:projectId/environments/:envId/apps/:appName/identity", h.UpdateAppServiceIdentity)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/adopt", h.AdoptApp)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/keep", h.KeepDemoApp)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/values-token", h.GetValuesToken)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/delete-impact", h.DeleteAppImpact)
		api.DELETE("/projects/:projectId/environments/:envId/apps/:appName", h.DeleteApp)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/move-impact", h.MoveAppImpact)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/move", h.MoveApp)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/state", h.GetAppState)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/logs", h.GetAppLogs)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/metrics", h.GetAppMetrics)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/volume/export", h.ExportAppVolume)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/net-probe", h.ProbeAppNetwork)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/volume/usage", h.GetAppVolumeUsage)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/volume/files", h.ListAppFiles)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/volume/files/content", h.ReadAppFile)
		api.PUT("/projects/:projectId/environments/:envId/apps/:appName/volume/files/content", h.WriteAppFile)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/volume/files/raw", h.DownloadAppFile)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/volume/files/archive", h.DownloadAppDirectory)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/volume/files/upload", h.UploadAppFile)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/volume/files/mkdir", h.CreateAppDirectory)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/volume/files/move", h.MoveAppFile)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/volume/files/delete", h.DeleteAppFile)

		// Deploy-hook tokens (revocable bearer credential for external CI --
		// see the token-authenticated /api/v1/deploy* routes registered outside
		// this JWT group, near the dadagent webhook, below).
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/deploy-hooks", h.CreateDeployHook)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/deploy-hooks", h.ListDeployHooks)
		api.DELETE("/projects/:projectId/environments/:envId/apps/:appName/deploy-hooks/:hookId", h.DeleteDeployHook)

		// AI Gateway (ADR-015 control plane): self-service keys, BYOK provider
		// credentials and the project's own usage. The inference data plane
		// itself is a separate origin (ai.dada-tuda.ru) and never routes here.
		api.GET("/ai/catalog", h.GetAIGatewayCatalog)
		api.POST("/projects/:projectId/ai/keys", h.CreateAIGatewayKey)
		api.GET("/projects/:projectId/ai/keys", h.ListAIGatewayKeys)
		api.DELETE("/projects/:projectId/ai/keys/:keyId", h.DeleteAIGatewayKey)
		api.GET("/projects/:projectId/ai/credentials", h.ListAIProviderCredentials)
		api.PUT("/projects/:projectId/ai/credentials/:provider", h.PutAIProviderCredential)
		api.DELETE("/projects/:projectId/ai/credentials/:provider", h.DeleteAIProviderCredential)
		api.GET("/projects/:projectId/ai/usage", h.GetProjectAIUsage)
		api.GET("/projects/:projectId/ai/routing", h.GetAIRoutingMode)
		api.PUT("/projects/:projectId/ai/routing", h.SetAIRoutingMode)

		// Cloud tasks (DadaAgent integration).
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/cloud-tasks", h.ListCloudTasks)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/cloud-tasks", h.CreateCloudTask)
		api.GET("/projects/:projectId/cloud-tasks/:taskId", h.GetCloudTask)
		api.GET("/projects/:projectId/cloud-tasks/:taskId/artifacts/:fileId", h.ProxyCloudTaskArtifact)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/autofix", h.TriggerAutofix)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/diagnose", h.DiagnoseApp)

		// Aggregated log search (Elasticsearch/filebeat proxy, read-only).
		api.GET("/projects/:projectId/logs", h.SearchLogs)

		// Per-project resource cost (OpenCost Allocation API, read-only).
		api.GET("/projects/:projectId/cost", h.GetProjectCost)

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

		api.POST("/projects/:projectId/domains/authorizations/:authId/delegate", h.DelegateAuthorization)
		api.GET("/projects/:projectId/domains/authorizations/:authId/zone", h.GetManagedZone)
		api.GET("/projects/:projectId/domains/authorizations/:authId/zone/records", h.ListManagedRecords)
		api.POST("/projects/:projectId/domains/authorizations/:authId/zone/records", h.UpsertManagedRecord)
		api.DELETE("/projects/:projectId/domains/authorizations/:authId/zone/records", h.DeleteManagedRecord)
		api.GET("/projects/:projectId/domains/authorizations/:authId/zone/import-preview", h.PreviewZoneImport)
		api.POST("/projects/:projectId/domains/authorizations/:authId/zone/import", h.ImportZone)

		// Vercel-flow: git repos, builds, deployments, env vars.
		// Git provider installations + remote repo listing (build-agent proxy).
		api.GET("/projects/:projectId/git/installations", h.ListGitInstallations)
		api.GET("/projects/:projectId/git/installations/available", h.ListAvailableInstallations)
		api.POST("/projects/:projectId/git/installations", h.BindInstallation)
		api.GET("/projects/:projectId/git/github/authorize", h.StartGitHubUserAuth)
		api.GET("/projects/:projectId/git/install-url", h.GetGitInstallURL)
		api.GET("/projects/:projectId/git/installations/:installationId/repos", h.ListInstallationRepos)
		api.GET("/projects/:projectId/git/installations/:installationId/detect", h.DetectFramework)
		api.GET("/projects/:projectId/git/detect", h.DetectPublicFramework)
		// Git repos linked per environment.
		api.GET("/projects/:projectId/environments/:envId/repos", h.ListGitRepos)
		api.POST("/projects/:projectId/environments/:envId/repos", h.ConnectGitRepo)
		api.DELETE("/projects/:projectId/environments/:envId/repos/:repoId", h.DisconnectGitRepo)
		// Builds (imperative — no operations). Scope-gated per ADR-009 vocabulary.
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/builds", auth.RequireScope("builds:read"), h.ListBuilds)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/builds", auth.RequireScope("builds:write"), h.TriggerBuild)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/source-archive", auth.RequireScope("builds:write"), h.UploadSourceArchive)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/source-archive/download", h.DownloadSourceArchive)
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
		api.DELETE("/projects/:projectId/environments/:envId/preview", auth.RequireScope("deploy:write"), h.DeletePreviewEnvironment)
		// Env vars (always encrypted at rest; reveal is write-gated).
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/env", h.ListEnvVars)
		api.PUT("/projects/:projectId/environments/:envId/apps/:appName/env/:key", h.SetEnvVar)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/env/bulk", h.BulkSetEnvVars)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/env/:key", h.RevealEnvVar)
		api.DELETE("/projects/:projectId/environments/:envId/apps/:appName/env/:key", h.DeleteEnvVar)

		api.POST("/projects/:projectId/environments/:envId/apps/:appName/payments/connect", h.PaymentsConnect)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/payments", h.PaymentsStatus)
		api.DELETE("/projects/:projectId/environments/:envId/apps/:appName/payments", h.PaymentsDisconnect)

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
		api.GET(mon+"/:appId/labels", h.GetMonitoringLabels)
		api.GET(mon+"/:appId/metrics", h.GetMonitoringMetrics)
		api.GET(mon+"/:appId/logs", h.GetMonitoringLogs)
		api.GET(mon+"/:appId/grafana-link", h.GetMonitoringGrafanaLink)
		api.GET(mon+"/:appId/dashboard", h.GetMonitoringDashboard)
		api.PUT(mon+"/:appId/dashboard", h.SaveMonitoringDashboard)
		api.GET(mon+"/:appId/events", h.GetMonitoringEvents)
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
			api.GET("/admin/audit", h.ListAuditEvents)
			api.GET("/admin/feedback", h.ListFeedback)
			api.POST("/admin/feedback/:id/resolve", h.ResolveFeedback)
			api.POST("/admin/feedback/:id/autofix", h.AutofixFeedback)
			api.GET("/admin/overview", h.GetAdminOverview)
			api.GET("/admin/costs", h.GetAdminCosts)
			api.GET("/admin/ai-gateway/usage", h.GetAIGatewayUsage)
			api.GET("/admin/growth/campaigns", h.GetGrowthCampaigns)
			api.GET("/admin/db-shards", h.GetAdminDBShards)

			// Concierge write-back for the Box private preview: which claim got
			// which box. Mandatory — it is the only source for the repeat-use
			// metric while provisioning is manual.
			api.POST("/admin/box/grants", h.GrantBox)

			// Inference proxy (playground only — production traffic goes via PublicApi ingress).
			api.POST("/projects/:projectId/environments/:envId/models/:name/infer", h.ProxyInference)
		}

		// Billing (plan catalog, account, usage, recommender, plan assignment).
		api.GET("/billing/plans", h.GetBillingPlans)
		api.POST("/billing/recommend-plan", h.RecommendPlan)
		api.GET("/projects/:projectId/billing/account", h.GetBillingAccount)
		api.GET("/projects/:projectId/billing/usage", h.GetBillingUsage)
		api.PUT("/projects/:projectId/billing/plan", h.AssignPlan)
		api.POST("/projects/:projectId/billing/checkout", h.BillingCheckout)
		api.PUT("/projects/:projectId/billing/autopay", h.SetBillingAutopay)
		api.GET("/projects/:projectId/billing/payments", h.GetBillingPayments)

		registerPayGatewayRoutes(r, api, h)

		// Informational real-consumption + money-equivalent (always on, viewer+).
		api.GET("/projects/:projectId/billing/consumption", h.GetProjectConsumption)
		api.GET("/billing/account/summary", h.GetAccountSummary)

		api.POST("/agent/chat", h.AgentChat)
		api.POST("/agent/chat/confirm", h.AgentChatConfirm)
		api.GET("/agent/chat/history", h.AgentChatGetHistory)
		api.POST("/agent/chat/context/clear", h.AgentChatClearContext)

		api.POST("/promo/redeem", h.RedeemPromo)
		api.GET("/onboarding", h.GetOnboarding)
		api.POST("/onboarding/:key", h.PostOnboarding)
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
