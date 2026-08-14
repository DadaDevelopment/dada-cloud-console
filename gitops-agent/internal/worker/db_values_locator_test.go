package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

func locatorRepo(t *testing.T, files map[string]string) *git.Manager {
	t.Helper()
	mgr := git.New(git.RepoConfig{RepoURL: "https://example.invalid/argo-infra.git", LocalBase: t.TempDir()})
	for path, content := range files {
		full := filepath.Join(mgr.LocalPath(), path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return mgr
}

func serviceDatabaseValues(names ...string) string {
	out := "manifests:\n"
	for _, n := range names {
		out += "  - apiVersion: platform.dada-tuda.ru/v1alpha1\n" +
			"    kind: ServiceDatabaseV2\n" +
			"    metadata:\n" +
			"      name: " + n + "\n" +
			"    spec:\n" +
			"      shard: shard-0\n"
	}
	return out
}

// TestLocateServiceDatabase_FindsTheCarrierAppRefDoesNotName covers the shape
// that failed 17 of 21 SetDatabaseTier operations in production: the CR lives
// in the project's standalone service-databases carrier while appRef names the
// bound app, so the path derived from appRef holds no ServiceDatabaseV2 at all.
func TestLocateServiceDatabase_FindsTheCarrierAppRefDoesNotName(t *testing.T) {
	carrier := renderer.AppResourcesValuesGitPath("acme", "prod", "service-databases-acme")
	mgr := locatorRepo(t, map[string]string{
		renderer.AppResourcesValuesGitPath("acme", "prod", "api"): "manifests: []\n",
		carrier: serviceDatabaseValues("other-db", "fonbet-db"),
	})

	path, manifest, err := locateServiceDatabase(mgr, "acme", "prod", "api", "fonbet-db")
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if path != carrier {
		t.Fatalf("located %s, want %s", path, carrier)
	}
	if _, _, err := patchDatabaseTier(manifest, "fonbet-db", "free"); err != nil {
		t.Fatalf("the located manifest is not the named database: %v", err)
	}
}

// TestLocateServiceDatabase_PrefersTheDerivedPath keeps the scan from changing
// where an ordinary app-bound database is patched: two files carry a database
// of the same name only in this test, and the appRef one must win.
func TestLocateServiceDatabase_PrefersTheDerivedPath(t *testing.T) {
	bound := renderer.AppResourcesValuesGitPath("acme", "prod", "api")
	mgr := locatorRepo(t, map[string]string{
		bound: serviceDatabaseValues("api-db"),
		renderer.AppResourcesValuesGitPath("acme", "prod", "aaa-earlier"): serviceDatabaseValues("api-db"),
	})

	path, _, err := locateServiceDatabase(mgr, "acme", "prod", "api", "api-db")
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if path != bound {
		t.Fatalf("located %s, want the appRef path %s", path, bound)
	}
}

// TestLocateServiceDatabase_ReportsAMissingDatabase asserts the failure stays a
// failure: a database whose CR is in no file at all must not silently patch a
// neighbour.
func TestLocateServiceDatabase_ReportsAMissingDatabase(t *testing.T) {
	mgr := locatorRepo(t, map[string]string{
		renderer.AppResourcesValuesGitPath("acme", "prod", "api"): serviceDatabaseValues("api-db"),
	})

	if _, _, err := locateServiceDatabase(mgr, "acme", "prod", "api", "ghost-db"); err == nil {
		t.Fatal("a database with no CR in git located successfully")
	}
}
