package api

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func probeMove() dbMove {
	return dbMove{
		ID:          "84726f34-72f8-457b-95b9-fc43a27ce2cd",
		Datname:     "dada-move-probe",
		OwnerRole:   "dada_move_probe",
		SourceShard: "shard-1",
		TargetShard: "shard-0",
	}
}

// A pod that dies is not a failed copy: the Job retries up to its backoff
// limit, and reading Status.Failed marked the whole move failed on the first
// crashed pod - while the Job that would have succeeded on the retry was still
// running.
func TestCopySchemaWaitsOutRetries(t *testing.T) {
	m := probeMove()
	job := schemaCopyJob(dbMoveJobName(m.ID), m, "src", "dst")
	job.Status.Failed = 1
	c := &jobSchemaCopier{cs: fake.NewSimpleClientset(job)}

	done, err := c.CopySchema(context.Background(), m, "src", "dst")
	if err != nil {
		t.Fatalf("a retrying job is not an error yet: %v", err)
	}
	if done {
		t.Fatal("a job with no successful pod is not done")
	}
}

func TestCopySchemaFailsWhenTheJobGivesUp(t *testing.T) {
	m := probeMove()
	job := schemaCopyJob(dbMoveJobName(m.ID), m, "src", "dst")
	job.Status.Failed = 3
	job.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobFailed,
		Status: corev1.ConditionTrue,
		Reason: "BackoffLimitExceeded",
	}}
	c := &jobSchemaCopier{cs: fake.NewSimpleClientset(job)}

	if _, err := c.CopySchema(context.Background(), m, "src", "dst"); err == nil {
		t.Fatal("a job past its backoff limit must fail the move")
	} else if !strings.Contains(err.Error(), "BackoffLimitExceeded") {
		t.Fatalf("the reason the job gave up belongs in the error: %v", err)
	}
}

func TestCopySchemaReportsSuccess(t *testing.T) {
	m := probeMove()
	job := schemaCopyJob(dbMoveJobName(m.ID), m, "src", "dst")
	job.Status.Succeeded = 1
	c := &jobSchemaCopier{cs: fake.NewSimpleClientset(job)}

	done, err := c.CopySchema(context.Background(), m, "src", "dst")
	if err != nil || !done {
		t.Fatalf("CopySchema = %v, %v, want true, nil", done, err)
	}
}

func TestCopySchemaCreatesTheJobOnce(t *testing.T) {
	m := probeMove()
	cs := fake.NewSimpleClientset()
	c := &jobSchemaCopier{cs: cs}

	for i := 0; i < 3; i++ {
		if done, err := c.CopySchema(context.Background(), m, "src", "dst"); err != nil || done {
			t.Fatalf("tick %d: CopySchema = %v, %v", i, done, err)
		}
	}
	jobs, err := cs.BatchV1().Jobs(dbMoveNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("every tick started a new dump against the same database: %d jobs", len(jobs.Items))
	}
}

// dash has no pipefail, so a shell without bash would report the exit status of
// psql - which succeeds on the empty stream a failed pg_dump leaves behind.
func TestSchemaCopyJobRunsUnderBash(t *testing.T) {
	job := schemaCopyJob("db-move-x", probeMove(), "src", "dst")
	cmd := job.Spec.Template.Spec.Containers[0].Command
	if cmd[0] != "/bin/bash" {
		t.Fatalf("command[0] = %q, want /bin/bash", cmd[0])
	}
	if !strings.Contains(cmd[2], "pipefail") {
		t.Fatalf("a broken dump must not be masked by psql: %q", cmd[2])
	}
}
