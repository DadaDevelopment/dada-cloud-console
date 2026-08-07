package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
)

// seedDefaultDomainApp inserts an App resource_snapshot with the given
// summary and a managed domain_hostnames row pointing at it, mirroring what
// CreateApp + BackfillMissingDefaultDomains leave behind for an app that got
// (or once got) an auto public domain. Returns the domain_hostnames id.
func seedDefaultDomainApp(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, appName, summaryJSON string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase, summary_json)
		 VALUES ($1, $2, 'App', $3, 'Ready', $4::jsonb)`,
		projectID, envID, appName, summaryJSON,
	); err != nil {
		t.Fatalf("seed resource_snapshots: %v", err)
	}
	var hostnameID uuid.UUID
	hostname := appName + "-abcd.apps.test.dada-tuda.ru"
	if err := pool.QueryRow(ctx,
		`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, managed)
		 VALUES (NULL, $1, $2, $3, 'CNAME', 'active', 'active', true)
		 RETURNING id`,
		envID, appName, hostname,
	).Scan(&hostnameID); err != nil {
		t.Fatalf("seed domain_hostnames: %v", err)
	}
	return hostnameID
}

// TestDetachHostname_WorkerAppMayShedManagedDomain pins the retrofit-unstick
// path: an app that is now a worker (or has no configured port) must be able
// to shed the managed/default domain it was stuck with, since that route
// only ever 502s underneath it. An ordinary HTTP app must keep the same
// refusal as before.
func TestDetachHostname_WorkerAppMayShedManagedDomain(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	claims := godClaims(userID)
	projectID, envID := seedWorkerProjectEnv(t, pool)

	gin.SetMode(gin.TestMode)
	route := func(r *gin.Engine) {
		r.DELETE("/projects/:projectId/environments/:envId/apps/:appName/hostnames/:id", func(c *gin.Context) {
			auth.SetClaims(c, claims)
			h.DetachHostname(c)
		})
	}

	t.Run("worker app can detach its managed domain", func(t *testing.T) {
		appName := "wrk-" + uuid.NewString()[:8]
		hostnameID := seedDefaultDomainApp(t, pool, projectID, envID, appName, `{"port":8080,"worker":true}`)

		r := gin.New()
		route(r)
		rec := httptest.NewRecorder()
		path := "/projects/" + projectID.String() + "/environments/" + envID.String() +
			"/apps/" + appName + "/hostnames/" + hostnameID.String()
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM domain_hostnames WHERE id = $1`, hostnameID,
		).Scan(&count); err != nil {
			t.Fatalf("query domain_hostnames: %v", err)
		}
		if count != 0 {
			t.Errorf("domain_hostnames row still present after detach")
		}
	})

	t.Run("portless app can detach its managed domain", func(t *testing.T) {
		appName := "zero-" + uuid.NewString()[:8]
		hostnameID := seedDefaultDomainApp(t, pool, projectID, envID, appName, `{"port":0}`)

		r := gin.New()
		route(r)
		rec := httptest.NewRecorder()
		path := "/projects/" + projectID.String() + "/environments/" + envID.String() +
			"/apps/" + appName + "/hostnames/" + hostnameID.String()
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("ordinary http app keeps the managed-domain refusal", func(t *testing.T) {
		appName := "web-" + uuid.NewString()[:8]
		hostnameID := seedDefaultDomainApp(t, pool, projectID, envID, appName, `{"port":8080}`)

		r := gin.New()
		route(r)
		rec := httptest.NewRecorder()
		path := "/projects/" + projectID.String() + "/environments/" + envID.String() +
			"/apps/" + appName + "/hostnames/" + hostnameID.String()
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 managed_domain; body=%s", rec.Code, rec.Body.String())
		}
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM domain_hostnames WHERE id = $1`, hostnameID,
		).Scan(&count); err != nil {
			t.Fatalf("query domain_hostnames: %v", err)
		}
		if count != 1 {
			t.Errorf("domain_hostnames row must survive a refused detach")
		}
	})

	t.Run("missing app snapshot fails closed and keeps the refusal", func(t *testing.T) {
		appName := "ghost-" + uuid.NewString()[:8]
		hostname := appName + "-abcd.apps.test.dada-tuda.ru"
		var hostnameID uuid.UUID
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO domain_hostnames (authorization_id, environment_id, app_name, hostname, record_type, status, cert_status, managed)
			 VALUES (NULL, $1, $2, $3, 'CNAME', 'active', 'active', true)
			 RETURNING id`,
			envID, appName, hostname,
		).Scan(&hostnameID); err != nil {
			t.Fatalf("seed domain_hostnames: %v", err)
		}

		r := gin.New()
		route(r)
		rec := httptest.NewRecorder()
		path := "/projects/" + projectID.String() + "/environments/" + envID.String() +
			"/apps/" + appName + "/hostnames/" + hostnameID.String()
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (fail closed with no snapshot); body=%s", rec.Code, rec.Body.String())
		}
	})
}
