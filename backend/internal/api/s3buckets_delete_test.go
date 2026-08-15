package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/models"
)

// The owner app decides which resources.values.yaml the agent edits. Pointing
// it at the wrong file makes the delete a no-op that still reports success, so
// the fallback chain is pinned here: the console's own app_ref wins, the
// git-watcher's app_name is the fallback, and the "s3-buckets-<project>"
// carrier name is not an app — it means the bucket is env-level.
func TestS3BucketAppRef_FallbackChain(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    string
	}{
		{"app_ref wins", `{"app_ref":"api","app_name":"s3-buckets-agent-sandbox"}`, "api"},
		{"app_name fallback", `{"app_name":"api"}`, "api"},
		{"carrier app_name is env-level", `{"app_name":"s3-buckets-agent-sandbox"}`, ""},
		{"no provenance at all", `{"bucket_name":"dada-archive-7a387969e082"}`, ""},
		{"empty summary", ``, ""},
		{"unparsable summary", `not json`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s3BucketAppRef([]byte(tc.summary)); got != tc.want {
				t.Errorf("s3BucketAppRef(%s) = %q, want %q", tc.summary, got, tc.want)
			}
		})
	}
}

// Deleting a bucket that has no snapshot must 404 and still leave a record of
// the attempt: a refused destructive delete is exactly the event the audit
// trail exists for.
func TestDeleteS3Bucket_UnknownNameIsAudited(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/s3buckets/ghost"
	rec := routeDatabaseCall(t, http.MethodDelete,
		"/projects/:projectId/environments/:envId/s3buckets/:name", path,
		"", godClaims(userID), h.DeleteS3Bucket)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	outcome, reason, _ := lastAuditRow(t, pool, projectID, "DeleteS3Bucket")
	if outcome != auditOutcomeFailure || reason != "not_found" {
		t.Errorf("audit row = (%q, %q), want (failure, not_found)", outcome, reason)
	}
}

// The happy path: an operation carrying the bucket name and its owner app, so
// the agent knows which file to edit, plus an audit row tying the intent to
// that operation. The intent row is pending, not success: the bucket is not
// gone until the agent commits and Crossplane destroys it.
func TestDeleteS3Bucket_EnqueuesOperationAndAudits(t *testing.T) {
	pool := testOptimisticPool(t)
	h := &Handler{pool: pool, cfg: &config.Config{}}
	userID := seedUser(t, pool)
	projectID, envID := seedOptimisticFixture(t, pool)

	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO resource_snapshots (project_id, environment_id, kind, name, summary_json)
		 VALUES ($1, $2, 'S3Bucket', 'dada-archive', $3)`,
		projectID, envID, `{"app_ref":"","app_name":"s3-buckets-agent-sandbox"}`,
	); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	path := "/projects/" + projectID.String() + "/environments/" + envID.String() + "/s3buckets/dada-archive"
	rec := routeDatabaseCall(t, http.MethodDelete,
		"/projects/:projectId/environments/:envId/s3buckets/:name", path,
		"", godClaims(userID), h.DeleteS3Bucket)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}

	var payloadRaw []byte
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT payload, status FROM operations
		  WHERE project_id = $1 AND action = 'DeleteS3Bucket'
		  ORDER BY created_at DESC LIMIT 1`,
		projectID,
	).Scan(&payloadRaw, &status); err != nil {
		t.Fatalf("expected a DeleteS3Bucket operation: %v", err)
	}
	if status != "Created" {
		t.Errorf("operation status = %q, want Created", status)
	}
	var payload models.DeleteS3BucketPayload
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("payload is not a DeleteS3BucketPayload: %v", err)
	}
	if payload.Name != "dada-archive" {
		t.Errorf("payload.Name = %q, want dada-archive", payload.Name)
	}
	if payload.AppRef != "" {
		t.Errorf("payload.AppRef = %q, want empty for an env-level bucket", payload.AppRef)
	}

	outcome, _, gotEnv := lastAuditRow(t, pool, projectID, "DeleteS3Bucket")
	if outcome != auditOutcomePending {
		t.Errorf("outcome = %q, want %q — the console has enqueued a delete, not performed one", outcome, auditOutcomePending)
	}
	if gotEnv == nil || *gotEnv != envID {
		t.Errorf("environment_id = %v, want %v", gotEnv, envID)
	}
}
