package api_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/api"
	"github.com/dada-tuda/console/backend/internal/config"
)

// swaggerDoc is the minimal shape of the generated swagger.json we assert on:
// a basePath plus the set of paths, each mapping HTTP methods to operations.
type swaggerDoc struct {
	BasePath string                            `json:"basePath"`
	Paths    map[string]map[string]interface{} `json:"paths"`
}

// ginParam matches gin-style :param path segments so we can normalize them to
// the OpenAPI {param} form before comparing against the spec.
var ginParam = regexp.MustCompile(`:([^/]+)`)

func normalizePath(p string) string {
	return ginParam.ReplaceAllString(p, "{$1}")
}

// routesSkippedFromSpec are infrastructure routes that intentionally carry no
// OpenAPI annotation (liveness/readiness probes and the spec endpoint itself).
var routesSkippedFromSpec = map[string]bool{
	"/health":       true,
	"/healthz":      true,
	"/ready":        true,
	"/openapi.json": true,
}

// TestOpenAPICoverage enumerates the real gin routes registered by SetupRouter
// and asserts every /api/v1/... route appears as a path+method in the generated
// swagger.json. This is the golden gate that fails the build whenever a new
// handler is wired without a matching @Router annotation — keeping the spec
// (and therefore the reflective MCP toolset) complete.
//
// SetupRouter/NewHandler do not dereference the pool at setup time (see
// handler.go: NewHandler only stores it; SetupRouter only registers routes and
// reads pool inside the /ready closure, which we never invoke). The
// background loops NewHandler starts guard against a nil pool (see
// advisory_lock.go: runWithAdvisoryLock no-ops on nil), so a nil
// *pgxpool.Pool is sufficient to build the engine without a live database. A
// zero-value &pgxpool.Pool{} is NOT: it is a non-nil pointer wrapping a
// never-constructed internal pool, and pool.Acquire on it segfaults instead
// of erroring.
func TestOpenAPICoverage(t *testing.T) {
	raw, err := os.ReadFile("docs/swagger.json")
	if err != nil {
		t.Fatalf("read generated swagger.json (run `swag init ... -o internal/api/docs`): %v", err)
	}
	var doc swaggerDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse swagger.json: %v", err)
	}
	if doc.BasePath == "" {
		t.Fatal("swagger.json has no basePath")
	}

	// Build a set of "<METHOD> <path>" present in the spec, with basePath
	// prefixed so it matches the full gin route.
	specOps := map[string]bool{}
	for path, methods := range doc.Paths {
		for method := range methods {
			specOps[strings.ToUpper(method)+" "+doc.BasePath+path] = true
		}
	}

	// AIStudioEnabled=true so the v2 (AI Studio) routes are registered and
	// must therefore be covered by the spec too.
	cfg := &config.Config{
		JWTSecret:       "test-secret",
		DevMode:         true,
		AIStudioEnabled: true,
	}
	engine := api.SetupRouter(nil, cfg)

	var missing []string
	for _, rt := range engine.Routes() {
		path := normalizePath(rt.Path)
		if routesSkippedFromSpec[path] {
			continue
		}
		if !strings.HasPrefix(path, "/api/v1/") {
			continue
		}
		key := rt.Method + " " + path
		if !specOps[key] {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		t.Fatalf("the following routes are registered but missing OpenAPI annotations "+
			"(add @Router/@ID etc. and regenerate docs):\n  %s",
			strings.Join(missing, "\n  "))
	}
}
