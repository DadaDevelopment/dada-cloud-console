package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestCreateS3Bucket_NameFallsBackToBucketName pins the live-defect fix for the
// createS3Bucket agent-chat action: michaelharlam@yandex.ru approved a
// createS3Bucket call carrying only bucket_name ("dating-service-assets"),
// which the handler bounced with 400 missing_name even though a valid kube
// name is derivable from bucket_name. He made no further write action for the
// following 25.5 hours. The same failure class fired 2026-08-04 and sat
// unfixed for 16 days.
//
// Each sub-test drives the real handler against a real Postgres so the
// fallback is proven through the actual INSERT path (operations +
// resource_snapshots), not a reimplementation of the derivation logic.
func TestCreateS3Bucket_NameFallsBackToBucketName(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool}
	userID := seedUser(t, pool)

	route := "/projects/:projectId/environments/:envId/s3buckets"
	post := func(projectID, envID uuid.UUID, body string) *httptest.ResponseRecorder {
		path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/s3buckets"
		return routeDatabaseCall(t, http.MethodPost, route, path, body, godClaims(userID), h.CreateS3Bucket)
	}

	snapshotName := func(projectID, envID uuid.UUID) (string, bool) {
		var name string
		err := pool.QueryRow(context.Background(),
			`SELECT name FROM resource_snapshots
			  WHERE project_id = $1 AND environment_id = $2 AND kind = 'S3Bucket'
			  ORDER BY first_seen_at DESC LIMIT 1`,
			projectID, envID,
		).Scan(&name)
		if err != nil {
			return "", false
		}
		return name, true
	}

	t.Run("empty name derives from bucket_name and is accepted", func(t *testing.T) {
		projectID, envID := seedOptimisticFixture(t, pool)
		rec := post(projectID, envID, `{"bucket_name":"dating-service-assets","public":true}`)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
		name, ok := snapshotName(projectID, envID)
		if !ok {
			t.Fatalf("no S3Bucket resource_snapshots row created")
		}
		if name != "dating-service-assets" {
			t.Errorf("derived name = %q, want %q", name, "dating-service-assets")
		}
	})

	t.Run("empty bucket_name falls back to name and is accepted", func(t *testing.T) {
		projectID, envID := seedOptimisticFixture(t, pool)
		rec := post(projectID, envID, `{"name":"media-assets"}`)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
		name, ok := snapshotName(projectID, envID)
		if !ok {
			t.Fatalf("no S3Bucket resource_snapshots row created")
		}
		if name != "media-assets" {
			t.Errorf("name = %q, want %q", name, "media-assets")
		}
	})

	t.Run("both empty stays a 400 with the original reason", func(t *testing.T) {
		projectID, envID := seedOptimisticFixture(t, pool)
		rec := post(projectID, envID, `{"public":true}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		outcome, reason, _ := lastAuditRow(t, pool, projectID, "CreateS3Bucket")
		if outcome != auditOutcomeFailure {
			t.Errorf("outcome = %q, want %q", outcome, auditOutcomeFailure)
		}
		if reason != "missing_name" {
			t.Errorf("reason = %q, want %q", reason, "missing_name")
		}
	})

	t.Run("bucket_name with no derivable kube name is a clear 400", func(t *testing.T) {
		projectID, envID := seedOptimisticFixture(t, pool)
		rec := post(projectID, envID, `{"bucket_name":"___"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		outcome, reason, _ := lastAuditRow(t, pool, projectID, "CreateS3Bucket")
		if outcome != auditOutcomeFailure {
			t.Errorf("outcome = %q, want %q", outcome, auditOutcomeFailure)
		}
		if reason == "missing_name" {
			t.Errorf("reason = %q, want a reason naming the derivation failure, not the empty-input reason", reason)
		}
	})
}
