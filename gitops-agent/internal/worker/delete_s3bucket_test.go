package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// s3ValuesFixture writes a resources.values.yaml carrying the given S3Bucket
// names into a throwaway worktree and returns a Manager rooted at it plus the
// values path, so the removal path can be exercised without a remote.
func s3ValuesFixture(t *testing.T, projectSlug, envSlug, appRef string, bucketNames ...string) (*git.Manager, string) {
	t.Helper()

	mgr := git.New(git.RepoConfig{
		RepoURL:   "https://example.invalid/scm/dada/argo-infra.git",
		Branch:    "main",
		LocalBase: t.TempDir(),
	})
	valuesPath := renderer.S3BucketResourcesValuesGitPath(projectSlug, envSlug, appRef)

	var b strings.Builder
	b.WriteString("manifests:\n")
	for _, name := range bucketNames {
		yaml, err := renderer.RenderS3Bucket(renderer.S3BucketSpec{
			Name:        name,
			BucketName:  name + "-7a387969e082",
			Region:      "ru1",
			ProjectSlug: projectSlug,
			EnvSlug:     envSlug,
			OperationID: "11111111-1111-1111-1111-111111111111",
		})
		if err != nil {
			t.Fatalf("RenderS3Bucket(%s): %v", name, err)
		}
		for i, line := range strings.Split(strings.TrimRight(yaml, "\n"), "\n") {
			if i == 0 {
				b.WriteString("  - " + line + "\n")
				continue
			}
			b.WriteString("    " + line + "\n")
		}
	}

	full := filepath.Join(mgr.LocalPath(), valuesPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write values: %v", err)
	}
	return mgr, valuesPath
}

// A bucket sharing its carrier with a sibling must be removed alone: the file
// stays, so the commit is a file edit and the carrier app is left standing.
func TestDeleteS3Bucket_RemovesOnlyTheNamedBucket(t *testing.T) {
	mgr, valuesPath := s3ValuesFixture(t, "agent-sandbox", "prod", "", "my-s3-bucket", "dada-archive")

	file, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{{"S3Bucket", "dada-archive"}})
	if err != nil {
		t.Fatalf("removeManifestsFile: %v", err)
	}
	if !changed {
		t.Fatal("removing a bucket that is in the file must report a change")
	}
	if strings.Contains(file.Content, "dada-archive") {
		t.Errorf("deleted bucket still present in the values file:\n%s", file.Content)
	}
	if !strings.Contains(file.Content, "my-s3-bucket") {
		t.Errorf("sibling bucket was dropped by a single-bucket delete:\n%s", file.Content)
	}
	empty, err := manifestsFileIsEmpty(file)
	if err != nil {
		t.Fatalf("manifestsFileIsEmpty: %v", err)
	}
	if empty {
		t.Fatal("carrier still holds a bucket; reporting it empty would tear the carrier app down and delete the surviving bucket with it")
	}
}

// The last bucket in a standalone carrier: the file goes empty, which is the
// signal doDeleteS3Bucket uses to remove the whole carrier app instead of
// committing a manifests list ArgoCD refuses to auto-sync.
func TestDeleteS3Bucket_LastBucketEmptiesTheCarrier(t *testing.T) {
	mgr, valuesPath := s3ValuesFixture(t, "agent-sandbox", "prod", "", "dada-archive")

	file, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{{"S3Bucket", "dada-archive"}})
	if err != nil {
		t.Fatalf("removeManifestsFile: %v", err)
	}
	if !changed {
		t.Fatal("removing the only bucket must report a change")
	}
	empty, err := manifestsFileIsEmpty(file)
	if err != nil {
		t.Fatalf("manifestsFileIsEmpty: %v", err)
	}
	if !empty {
		t.Fatalf("last bucket removed but the carrier is not reported empty:\n%s", file.Content)
	}
}

// A bucket that is not in the owner's manifests list (delivered by a helm chart
// template, or hand-written into the cluster) yields no change — the condition
// doDeleteS3Bucket turns into a failed operation rather than a green "deleted"
// over a bucket that is still live and still billed.
func TestDeleteS3Bucket_UnknownBucketIsNotAChange(t *testing.T) {
	mgr, valuesPath := s3ValuesFixture(t, "agent-sandbox", "prod", "", "my-s3-bucket")

	_, changed, err := removeManifestsFile(mgr, valuesPath, [][2]string{{"S3Bucket", "mimir-blocks"}})
	if err != nil {
		t.Fatalf("removeManifestsFile: %v", err)
	}
	if changed {
		t.Fatal("a bucket absent from the values file must not report a change")
	}
}

// The delete has to look in the same file the create wrote to: the bound app's
// chart when the bucket has an app_ref, the per-project carrier otherwise.
func TestS3BucketResourcesValuesGitPath_FollowsOwnership(t *testing.T) {
	standalone := renderer.S3BucketResourcesValuesGitPath("agent-sandbox", "prod", "")
	if !strings.Contains(standalone, "/apps/s3-buckets-agent-sandbox/resources.values.yaml") {
		t.Errorf("env-level bucket path = %q, want the s3-buckets-<project> carrier", standalone)
	}
	bound := renderer.S3BucketResourcesValuesGitPath("agent-sandbox", "prod", "api")
	if !strings.Contains(bound, "/apps/api/resources.values.yaml") {
		t.Errorf("bound bucket path = %q, want the bound app's chart", bound)
	}
}
