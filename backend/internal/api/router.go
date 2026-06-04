package api

import (
	"context"
	"net/http"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

	// Public routes
	r.POST("/api/v1/auth/login", h.Login)

	// Authenticated routes
	api := r.Group("/api/v1", auth.GinMiddleware(cfg.JWTSecret))
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

	return r
}
