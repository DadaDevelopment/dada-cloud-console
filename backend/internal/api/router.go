package api

import (
	"net/http"

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

		// Databases (ServiceDatabase CRD)
		api.GET("/projects/:projectId/environments/:envId/databases", h.ListDatabases)
		api.POST("/projects/:projectId/environments/:envId/databases", h.CreateServiceDatabase)

		// AppServers (VM track)
		api.GET("/projects/:projectId/app-servers", h.ListAppServers)
		api.POST("/projects/:projectId/app-servers", h.CreateAppServer)
		api.GET("/projects/:projectId/app-servers/:serverName", h.GetAppServer)
		api.DELETE("/projects/:projectId/app-servers/:serverName", h.DeleteAppServer)

		// Apps
		api.GET("/projects/:projectId/environments/:envId/apps", h.ListApps)
		api.POST("/projects/:projectId/environments/:envId/apps", h.CreateApp)
		api.PATCH("/projects/:projectId/environments/:envId/apps/:appName/image", h.UpdateAppImage)

		// Endpoints (PublicApi CRD)
		api.GET("/projects/:projectId/environments/:envId/apps/:appName/endpoints", h.ListEndpoints)
		api.POST("/projects/:projectId/environments/:envId/apps/:appName/endpoints", h.CreateEndpoint)

		// Operations
		api.GET("/projects/:projectId/operations", h.GetProjectOperations)
		api.GET("/projects/:projectId/operations/:operationId", h.GetOperation)
		api.POST("/projects/:projectId/operations/:operationId/retry", h.RetryOperation)
	}

	// Health check (unauthenticated) — /health for Helm probes, /healthz for k8s convention
	healthHandler := func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) }
	r.GET("/health", healthHandler)
	r.GET("/healthz", healthHandler)

	return r
}
