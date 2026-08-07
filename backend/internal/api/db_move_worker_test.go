package api

import (
	"strings"
	"testing"
)

func TestDBMoveJobNameIsStableAndShort(t *testing.T) {
	id := "ea951403-0c32-4c9d-ab41-a25e4dc1a25c"
	name := dbMoveJobName(id)
	if name != dbMoveJobName(id) {
		t.Fatal("the name must be derived, not generated: a second tick has to find the Job it already created")
	}
	if len(name) > 63 {
		t.Fatalf("job name %q is longer than a Kubernetes name may be", name)
	}
	if !strings.HasPrefix(name, "db-move-") {
		t.Fatalf("job name %q does not say what it is", name)
	}
}

func TestSchemaCopyJobStopsOnFirstError(t *testing.T) {
	job := schemaCopyJob("db-move-abc", dbMove{Datname: "odds-research", TargetShard: "shard-2"},
		"postgres://src", "postgres://dst")
	script := job.Spec.Template.Spec.Containers[0].Command[2]

	if !strings.Contains(script, "ON_ERROR_STOP=1") {
		t.Fatalf("a half-loaded schema would replicate silently short of tables: %s", script)
	}
	if !strings.Contains(script, "--schema-only") {
		t.Fatalf("the data is carried by replication, not by the dump: %s", script)
	}
	if !strings.Contains(script, "pipefail") {
		t.Fatalf("without pipefail a failing pg_dump exits 0 through the pipe: %s", script)
	}
	if !strings.Contains(script, "--no-owner") {
		t.Fatalf("a dump carrying owners fails on a shard that lacks those roles: %s", script)
	}
}

func TestSchemaCopyJobNeverRestartsInPlace(t *testing.T) {
	job := schemaCopyJob("db-move-abc", dbMove{Datname: "odds-research"}, "postgres://src", "postgres://dst")
	if *job.Spec.BackoffLimit > 2 {
		t.Fatalf("backoffLimit = %d: a repeatedly retried dump hammers the shard it is moving off", *job.Spec.BackoffLimit)
	}
	if job.Spec.Template.Spec.RestartPolicy != "Never" {
		t.Fatalf("restart policy = %q", job.Spec.Template.Spec.RestartPolicy)
	}
	if job.Spec.TTLSecondsAfterFinished == nil {
		t.Fatal("a finished Job must expire, or moves pile up as Completed pods nobody removes")
	}
}
