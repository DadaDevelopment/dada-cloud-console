package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/crypto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// installTestKey is a throwaway 32-byte hex key: the install path encrypts the
// seeded database credentials, so a handler without one cannot run at all.
const installTestKey = "ab000000000000000000000000000000000000000000000000000000000000cd"

func TestAppNameForInstall(t *testing.T) {
	cases := []struct {
		name string
		req  installSolutionRequest
		repo string
		want string
	}{
		{"explicit wins", installSolutionRequest{AppName: "my-app", Slug: "excalidraw"}, "excalidraw/excalidraw", "my-app"},
		{"slug when no app name", installSolutionRequest{Slug: "it-tools"}, "CorentinTh/it-tools", "it-tools"},
		{"repo short name lowercased", installSolutionRequest{}, "freeCodeCamp/devdocs", "devdocs"},
		{"dots and underscores become hyphens", installSolutionRequest{}, "acme/My_Cool.App", "my-cool-app"},
		{"leading and trailing junk trimmed", installSolutionRequest{}, "acme/.hidden.", "hidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := appNameForInstall(tc.req, tc.repo); got != tc.want {
				t.Fatalf("appNameForInstall = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestManagedDatabaseNameForIsAlwaysValid guards the derived names against the
// validators the database core applies: a name the customer never typed is
// still a name that has to pass validateKubeName and validatePgName, and a
// derivation that fails them turns one click into a 400 nobody can act on.
func TestManagedDatabaseNameForIsAlwaysValid(t *testing.T) {
	for _, app := range []string{"n8n", "excalidraw", "9gag", "a", "my-long-app-name"} {
		resource, database := managedDatabaseNameFor(app)
		if err := validateKubeName(resource); err != nil {
			t.Fatalf("resource name %q for app %q: %v", resource, app, err)
		}
		if err := validatePgName(database); err != nil {
			t.Fatalf("database name %q for app %q: %v", database, app, err)
		}
	}
	if resource, _ := managedDatabaseNameFor("n8n"); resource != "n8n-db" {
		t.Fatalf("resource = %q, want n8n-db", resource)
	}
	if _, database := managedDatabaseNameFor("9gag"); database != "db-9gag" {
		t.Fatalf("database = %q, want db-9gag", database)
	}
}

func testInstallPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping solution-install DB integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedInstallProject(t *testing.T, pool *pgxpool.Pool, orgID, runtime string) (projectID, envID, userID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]

	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, display_name, keycloak_sub)
		 VALUES ($1, $2, '', 'install test', $3) RETURNING id`,
		"install-"+suffix, "install-"+suffix+"@example.test", uuid.NewString(),
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { dropSeededUser(pool, userID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (name, display_name, org_id) VALUES ($1, $1, $2) RETURNING id`,
		"solution-install-test-"+suffix, orgID,
	).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() { dropSeededProject(pool, projectID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, name, namespace, type, runtime) VALUES ($1, 'prod', $2, 'prod', $3) RETURNING id`,
		projectID, "ns-"+suffix, runtime,
	).Scan(&envID); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return projectID, envID, userID
}

func newInstallCtx(projectID, envID uuid.UUID, body any, claims *auth.Claims) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	raw, _ := json.Marshal(body)
	path := "/api/v1/projects/" + projectID.String() + "/environments/" + envID.String() + "/solutions/install"
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{
		{Key: "projectId", Value: projectID.String()},
		{Key: "envId", Value: envID.String()},
	}
	auth.SetClaims(c, claims)
	return c, rec
}

func newInstallHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}}
}

// TestInstallSolution_CatalogEntryLinksAndBuilds is the newcomer's scenario:
// one call, and the project has a repository linked with the verified spec and
// a build already queued -- no second call the console could forget to make.
func TestInstallSolution_CatalogEntryLinksAndBuilds(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")
	t.Cleanup(func() { dropSeededAudit(pool, "Solution", "excalidraw") })

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/platform-admins"}}
	c, rec := newInstallCtx(projectID, envID, installSolutionRequest{Slug: "excalidraw"}, claims)
	h.InstallSolution(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	var repoFullName, branch, rootDir string
	var port int
	if err := pool.QueryRow(ctx,
		`SELECT repo_full_name, production_branch, root_dir, port FROM git_repos
		 WHERE project_id = $1 AND environment_id = $2 AND app_name = 'excalidraw'`,
		projectID, envID,
	).Scan(&repoFullName, &branch, &rootDir, &port); err != nil {
		t.Fatalf("linked repo not found: %v", err)
	}
	if repoFullName != "excalidraw/excalidraw" {
		t.Fatalf("repo_full_name = %q", repoFullName)
	}
	if branch != "master" {
		t.Fatalf("branch = %q, want the catalog's verified branch master", branch)
	}
	if port != 80 {
		t.Fatalf("port = %d, want the catalog's verified port 80", port)
	}

	var buildStatus, buildBranch string
	if err := pool.QueryRow(ctx,
		`SELECT status, branch FROM builds WHERE environment_id = $1 AND app_name = 'excalidraw'`,
		envID,
	).Scan(&buildStatus, &buildBranch); err != nil {
		t.Fatalf("build not queued: %v", err)
	}
	if buildStatus != "queued" {
		t.Fatalf("build status = %q, want queued", buildStatus)
	}
	if buildBranch != "master" {
		t.Fatalf("build branch = %q, want master", buildBranch)
	}

	var dbCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM operations WHERE environment_id = $1 AND action = 'CreateServiceDatabase'`,
		envID,
	).Scan(&dbCount); err != nil {
		t.Fatalf("count database operations: %v", err)
	}
	if dbCount != 0 {
		t.Fatalf("ordered %d databases for a project that declares no needs", dbCount)
	}
}

// TestInstallSolution_WithDatabaseOnVMSeedsDSN is the whole point of item 4 on
// the VM track: the app comes up already able to reach its database, because
// the install seeded DATABASE_URL on it rather than telling the customer to go
// and wire one up.
func TestInstallSolution_WithDatabaseOnVMSeedsDSN(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "vm")
	t.Cleanup(func() { dropSeededAudit(pool, "Solution", "devdocs") })
	t.Cleanup(func() { dropSeededAudit(pool, "ServiceDatabaseV2", "devdocs-db") })

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/platform-admins"}}
	withDB := true
	c, rec := newInstallCtx(projectID, envID, installSolutionRequest{Slug: "devdocs", WithDatabase: &withDB}, claims)
	h.InstallSolution(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	var appRef, database string
	if err := pool.QueryRow(ctx,
		`SELECT payload->>'app_ref', payload->>'database' FROM operations
		 WHERE environment_id = $1 AND action = 'CreateServiceDatabase'`,
		envID,
	).Scan(&appRef, &database); err != nil {
		t.Fatalf("database operation not queued: %v", err)
	}
	if appRef != "devdocs" {
		t.Fatalf("app_ref = %q, want the installed app so the chart binds them", appRef)
	}
	if database != "devdocs" {
		t.Fatalf("database = %q", database)
	}

	var encrypted []byte
	if err := pool.QueryRow(ctx,
		`SELECT value_encrypted FROM env_vars WHERE environment_id = $1 AND app_name = 'devdocs' AND key = 'DATABASE_URL'`,
		envID,
	).Scan(&encrypted); err != nil {
		t.Fatalf("DATABASE_URL not seeded on the app: %v", err)
	}
	dsn, err := crypto.DecryptToken(installTestKey, encrypted)
	if err != nil {
		t.Fatalf("decrypt DATABASE_URL: %v", err)
	}
	if want := "@devdocs-db:5432/devdocs"; !bytes.Contains(dsn, []byte(want)) {
		t.Fatalf("DSN %q does not point at the database it just ordered (%q)", string(dsn), want)
	}

	var pgPassword []byte
	if err := pool.QueryRow(ctx,
		`SELECT value_encrypted FROM env_vars WHERE environment_id = $1 AND app_name = 'devdocs-db' AND key = 'POSTGRES_PASSWORD'`,
		envID,
	).Scan(&pgPassword); err != nil {
		t.Fatalf("POSTGRES_PASSWORD not seeded on the database app: %v", err)
	}
}

func TestInstallSolution_UnknownSlugIsNotFound(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")
	t.Cleanup(func() { dropSeededAudit(pool, "Solution", "") })

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/platform-admins"}}
	c, rec := newInstallCtx(projectID, envID, installSolutionRequest{Slug: "no-such-project"}, claims)
	h.InstallSolution(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d body=%s want 404", rec.Code, rec.Body.String())
	}
}

func TestInstallSolution_ReadOnlyRoleIsForbidden(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/orgs/acme/projects/" + projectID.String() + "/ReadOnly"}}
	c, rec := newInstallCtx(projectID, envID, installSolutionRequest{Slug: "excalidraw"}, claims)
	h.InstallSolution(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s want 403", rec.Code, rec.Body.String())
	}
}

// TestInstallSolution_PastedRepoInstalls covers the third audience: a link,
// no catalog entry, and the server-side defaults doing the rest.
func TestInstallSolution_PastedRepoInstalls(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")
	t.Cleanup(func() { dropSeededAudit(pool, "Solution", "hello-world") })

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/platform-admins"}}
	c, rec := newInstallCtx(projectID, envID, installSolutionRequest{Repo: "https://github.com/octocat/hello-world"}, claims)
	h.InstallSolution(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}

	var repoFullName, branch string
	var port int
	if err := pool.QueryRow(context.Background(),
		`SELECT repo_full_name, production_branch, port FROM git_repos
		 WHERE project_id = $1 AND environment_id = $2 AND app_name = 'hello-world'`,
		projectID, envID,
	).Scan(&repoFullName, &branch, &port); err != nil {
		t.Fatalf("linked repo not found: %v", err)
	}
	if repoFullName != "octocat/hello-world" {
		t.Fatalf("repo_full_name = %q", repoFullName)
	}
	if branch != "main" || port != 8080 {
		t.Fatalf("branch=%q port=%d, want the connect-repo defaults main/8080", branch, port)
	}
}

// A game server is reachable only because the VM publishes its port. A
// Kubernetes environment publishes HTTP through the shared ingress and nothing
// else, so installing one there would deploy green and never accept a player.
// The gate says so at install time instead, and names the substrate that works.
func TestInstallSolution_GameServerIsRejectedOnKubernetes(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")
	t.Cleanup(func() { dropSeededAudit(pool, "Solution", "") })

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/platform-admins"}}
	c, rec := newInstallCtx(projectID, envID, installSolutionRequest{Slug: "minecraft"}, claims)
	h.InstallSolution(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "VM") {
		t.Fatalf("the refusal must name the substrate that works: %s", rec.Body.String())
	}

	var appCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM operations WHERE environment_id = $1`, envID,
	).Scan(&appCount); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if appCount != 0 {
		t.Fatalf("a refused install queued %d operations", appCount)
	}
}

// A stateful catalog entry on a VM is what the whole VM track is for: the same
// card the cloud offers, running as a compose service with its data on the
// machine's own disk. It used to be impossible — the app core rejected any
// volume outside Kubernetes with storage_not_supported, which made every
// stateful ready-made project undeployable on a VM.
func TestInstallSolution_StatefulImageInstallsOnVM(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "vm")
	attachReadyAppServer(t, pool, projectID, envID)
	t.Cleanup(func() { dropSeededAudit(pool, "Solution", "minecraft") })
	t.Cleanup(func() { dropSeededAudit(pool, "App", "minecraft") })

	h := newInstallHandler(pool)
	claims := &auth.Claims{UserID: userID, Groups: []string{"/platform-admins"}}
	body := installSolutionRequest{Slug: "minecraft", Params: map[string]string{"eula": "TRUE"}}
	c, rec := newInstallCtx(projectID, envID, body, claims)
	h.InstallSolution(c)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s want 202", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	var image, volumePath, size string
	if err := pool.QueryRow(ctx,
		`SELECT payload->>'image', payload->'volume'->>'path', COALESCE(payload->'volume'->>'size', '')
		 FROM operations WHERE environment_id = $1 AND action = 'CreateApp' AND resource_name = 'minecraft'`,
		envID,
	).Scan(&image, &volumePath, &size); err != nil {
		t.Fatalf("CreateApp not queued: %v", err)
	}
	if image != "itzg/minecraft-server:java21" {
		t.Fatalf("image = %q", image)
	}
	if volumePath != "/data" {
		t.Fatalf("volume path = %q, want the catalog's data directory", volumePath)
	}
	// A VM volume is a Docker named volume on the machine's own disk: a Longhorn
	// size would be a number nothing on the VM can honour.
	if size != "" {
		t.Fatalf("volume size = %q, want it dropped for a compose app", size)
	}

	var encrypted []byte
	if err := pool.QueryRow(ctx,
		`SELECT value_encrypted FROM env_vars WHERE environment_id = $1 AND app_name = 'minecraft' AND key = 'EULA'`,
		envID,
	).Scan(&encrypted); err != nil {
		t.Fatalf("EULA not stored: %v", err)
	}
	eula, err := crypto.DecryptToken(installTestKey, encrypted)
	if err != nil {
		t.Fatalf("decrypt EULA: %v", err)
	}
	if string(eula) != "TRUE" {
		t.Fatalf("EULA = %q; the server refuses to start without it", string(eula))
	}
}

// attachReadyAppServer gives a VM environment the AppServer the app core
// insists on before it will queue anything for compose.
func attachReadyAppServer(t *testing.T, pool *pgxpool.Pool, projectID, envID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var serverID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO app_servers (project_id, name, status, source) VALUES ($1, $2, 'Ready', 'manual') RETURNING id`,
		projectID, "vm-"+uuid.NewString()[:8],
	).Scan(&serverID); err != nil {
		t.Fatalf("seed app server: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE environments SET app_server_id = $1 WHERE id = $2`, serverID, envID); err != nil {
		t.Fatalf("attach app server: %v", err)
	}
}
