package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// godClaims returns a platform-admin claim for the given user, which effectiveRole
// resolves to Owner on every project, so a create handler passes its membership +
// write-permission gates without seeding org/project group rows. The user id must
// reference a real users row because operations.actor_id has a NOT NULL FK to it.
func godClaims(userID uuid.UUID) *auth.Claims {
	return &auth.Claims{UserID: userID, Groups: []string{"/platform-admins"}}
}

// seedUser inserts a throwaway user (the create operation's actor) and returns its
// id, cleaning it up when the test ends.
func seedUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	suffix := uuid.NewString()[:8]
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, email, password_hash, display_name)
		 VALUES ($1, $2, 'x', $1) RETURNING id`,
		"tester-"+suffix, "tester-"+suffix+"@example.com",
	).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
	return id
}

// seedApp inserts an App snapshot so an endpoint create has an app to bind to.
func seedApp(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, phase)
		 VALUES ($1, $2, 'App', $3, 'Ready')`,
		projectID, envID, name,
	); err != nil {
		t.Fatalf("seed app: %v", err)
	}
}

// newCreateCtx builds a gin context carrying the given JSON body, path params and
// claims, plus a recorder to read the handler's response.
func newCreateCtx(t *testing.T, body string, params gin.Params, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	if claims != nil {
		auth.SetClaims(c, claims)
	}
	return c, rec
}

// assertOptimisticSeeded asserts the handler returned 202 and left exactly the
// optimistic snapshot the read surfaces depend on: a Pending row of the given kind
// stamped live_source=create-optimistic.
func assertOptimisticSeeded(t *testing.T, pool *pgxpool.Pool, rec *httptest.ResponseRecorder, projectID, envID uuid.UUID, kind, name string) {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var phase, src string
	err := pool.QueryRow(context.Background(),
		`SELECT phase, summary_json->>'live_source' FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = $3 AND name = $4`,
		projectID, envID, kind, name,
	).Scan(&phase, &src)
	if err != nil {
		t.Fatalf("optimistic %s row missing after create: %v", kind, err)
	}
	if phase != "Pending" {
		t.Fatalf("%s phase = %q, want Pending", kind, phase)
	}
	if src != "create-optimistic" {
		t.Fatalf("%s live_source = %q, want create-optimistic", kind, src)
	}
}

func params(projectID, envID uuid.UUID) gin.Params {
	return gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
	}
}

func TestCreateServiceDatabase_SeedsOptimisticSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "db-" + uuid.NewString()[:8]

	c, rec := newCreateCtx(t, `{"name":"`+name+`","database":"appdb"}`, params(projectID, envID), claims)
	h.CreateServiceDatabase(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "ServiceDatabaseV2", name)
}

func TestCreateApp_SeedsOptimisticSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "app-" + uuid.NewString()[:8]

	c, rec := newCreateCtx(t, `{"name":"`+name+`","image":"nginx:latest","port":8080}`, params(projectID, envID), claims)
	h.CreateApp(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "App", name)
}

func TestCreateApp_SeedsDefaultHostname(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{DefaultDomainEnabled: true, DefaultDomainBase: "example.test"}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "app-" + uuid.NewString()[:8]

	c, rec := newCreateCtx(t, `{"name":"`+name+`","image":"nginx:latest","port":8080}`, params(projectID, envID), claims)
	h.CreateApp(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "App", name)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM domain_hostnames WHERE environment_id = $1 AND app_name = $2`,
		envID, name,
	).Scan(&count); err != nil {
		t.Fatalf("query domain_hostnames: %v", err)
	}
	if count != 1 {
		t.Fatalf("domain_hostnames rows for app = %d, want 1", count)
	}
}

// TestCreateApp_WorkerSkipsDefaultHostname proves the worker flag durably blocks
// the auto public domain a long-poll bot (no HTTP server) would otherwise get:
// no domain_hostnames row is inserted, and the flag is stamped into the seeded
// optimistic snapshot summary so BackfillMissingDefaultDomains also stays away
// from it once gitops-agent replaces that snapshot (see appNeedsDefaultDomain).
func TestCreateApp_WorkerSkipsDefaultHostname(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{DefaultDomainEnabled: true, DefaultDomainBase: "example.test"}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "app-" + uuid.NewString()[:8]

	c, rec := newCreateCtx(t, `{"name":"`+name+`","image":"nginx:latest","port":8080,"worker":true}`, params(projectID, envID), claims)
	h.CreateApp(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "App", name)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM domain_hostnames WHERE environment_id = $1 AND app_name = $2`,
		envID, name,
	).Scan(&count); err != nil {
		t.Fatalf("query domain_hostnames: %v", err)
	}
	if count != 0 {
		t.Fatalf("domain_hostnames rows for worker app = %d, want 0", count)
	}

	var worker bool
	if err := pool.QueryRow(context.Background(),
		`SELECT (summary_json->>'worker')::boolean FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, name,
	).Scan(&worker); err != nil {
		t.Fatalf("query snapshot summary: %v", err)
	}
	if !worker {
		t.Fatalf("snapshot summary_json.worker = false, want true")
	}
}

func TestCreateS3Bucket_SeedsOptimisticSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "s3-" + uuid.NewString()[:8]

	c, rec := newCreateCtx(t, `{"name":"`+name+`","bucket_name":"bucket-`+name+`"}`, params(projectID, envID), claims)
	h.CreateS3Bucket(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "S3Bucket", name)
}

// TestCreateS3Bucket_RejectsDisallowedDescriptionCharset uses the exact
// description string from the live artemmendeleev incident (43 runes, well
// under the 45-rune cap) that Beget silently rejected on its colon, stranding
// the bucket create for 72 minutes. The API must now reject it up front with
// a message naming the offending character instead of letting it reach
// Beget mute.
func TestCreateS3Bucket_RejectsDisallowedDescriptionCharset(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "s3-" + uuid.NewString()[:8]

	body := `{"name":"` + name + `","bucket_name":"bucket-` + name + `","description":"Cold storage: Fonbet raw bodies offloaded"}`
	c, rec := newCreateCtx(t, body, params(projectID, envID), claims)
	h.CreateS3Bucket(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `":"`) {
		t.Fatalf("error body = %s, want it to name the rejected colon", rec.Body.String())
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM resource_snapshots WHERE project_id = $1 AND environment_id = $2 AND kind = 'S3Bucket' AND name = $3`,
		projectID, envID, name,
	).Scan(&count); err != nil {
		t.Fatalf("query resource_snapshots: %v", err)
	}
	if count != 0 {
		t.Fatalf("resource_snapshots rows for rejected bucket = %d, want 0", count)
	}
}

// TestCreateS3Bucket_AllowsValidDescriptionCharset proves Cyrillic letters plus
// the four allowed punctuation marks (. , _ -) pass the charset check.
func TestCreateS3Bucket_AllowsValidDescriptionCharset(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "s3-" + uuid.NewString()[:8]

	body := `{"name":"` + name + `","bucket_name":"bucket-` + name + `","description":"Холодное хранилище, бэкапы - раз_в.месяц"}`
	c, rec := newCreateCtx(t, body, params(projectID, envID), claims)
	h.CreateS3Bucket(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "S3Bucket", name)
}

// TestCreateS3Bucket_AllowsEmptyDescription proves the charset check does not
// treat the optional, empty description as a violation.
func TestCreateS3Bucket_AllowsEmptyDescription(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "s3-" + uuid.NewString()[:8]

	body := `{"name":"` + name + `","bucket_name":"bucket-` + name + `","description":""}`
	c, rec := newCreateCtx(t, body, params(projectID, envID), claims)
	h.CreateS3Bucket(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "S3Bucket", name)
}

func TestCreatePublicApi_SeedsOptimisticSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	appName := "app-" + uuid.NewString()[:8]
	seedApp(t, pool, projectID, envID, appName)
	sub := "e2e-" + uuid.NewString()[:8]
	fqdn := sub + ".example.com"
	publicApiName := sub + "-example-com"

	epParams := append(params(projectID, envID), gin.Param{Key: "appName", Value: appName})
	c, rec := newCreateCtx(t, `{"fqdn":"`+fqdn+`"}`, epParams, claims)
	h.CreateEndpoint(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "PublicApi", publicApiName)
}

func TestCreateAIModel_SeedsOptimisticSnapshot(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	projectID, envID := seedOptimisticFixture(t, pool)
	claims := godClaims(seedUser(t, pool))
	name := "ai-" + uuid.NewString()[:8]

	body := `{"name":"` + name + `","model_type":"custom","source":"custom","container_image":"img:latest","profile":"cpu-small","auth_mode":"apikey"}`
	c, rec := newCreateCtx(t, body, params(projectID, envID), claims)
	h.CreateAIModel(c)
	assertOptimisticSeeded(t, pool, rec, projectID, envID, "AIModel", name)
}
