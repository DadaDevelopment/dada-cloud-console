package worker

import (
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func ambiguityReconciler() *StatusReconciler {
	return &StatusReconciler{ambiguous: map[string]bool{}}
}

func TestResolveSnapshotEnvSingleClaimant(t *testing.T) {
	id := uuid.New()
	got, ok := ambiguityReconciler().resolveSnapshotEnv("PublicApi", "api-shop-ru", []uuid.UUID{id}, nil, nil)
	if !ok || got != id {
		t.Fatalf("single claimant: got (%v, %v), want (%v, true)", got, ok, id)
	}
}

func TestResolveSnapshotEnvUntrackedResource(t *testing.T) {
	r := ambiguityReconciler()
	if _, ok := r.resolveSnapshotEnv("PublicApi", "api-foreign-ru", nil, nil, nil); ok {
		t.Fatal("a CR with no snapshot in any project must not be attributed")
	}
	if len(r.ambiguous) != 0 {
		t.Fatalf("untracked resource warned as ambiguous: %v", r.ambiguous)
	}
}

func TestResolveSnapshotEnvLabelsBreakTheTie(t *testing.T) {
	live, stale := uuid.New(), uuid.New()
	owners := map[uuid.UUID]envOwner{
		live:  {project: "ggrk52", environment: "prod"},
		stale: {project: "example-project", environment: "prod"},
	}
	labels := map[string]string{projectLabel: "ggrk52", environmentLabel: "prod"}

	r := ambiguityReconciler()
	got, ok := r.resolveSnapshotEnv("PublicApi", "api-zerkalo-ru", []uuid.UUID{stale, live}, labels, owners)
	if !ok {
		t.Fatal("a name claimed by a live env and a stale copy must resolve to the live env, not freeze forever")
	}
	if got != live {
		t.Fatalf("resolved to %v, want the label-matching env %v", got, live)
	}
	if len(r.ambiguous) != 0 {
		t.Fatalf("resolved name left marked ambiguous: %v", r.ambiguous)
	}
}

func TestResolveSnapshotEnvUnlabelledCRStaysSkipped(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	owners := map[uuid.UUID]envOwner{
		a: {project: "ggrk52", environment: "prod"},
		b: {project: "example-project", environment: "prod"},
	}

	r := ambiguityReconciler()
	if _, ok := r.resolveSnapshotEnv("PublicApi", "api-old-ru", []uuid.UUID{a, b}, nil, owners); ok {
		t.Fatal("a CR predating the labels must not be attributed by guesswork")
	}
	if !r.ambiguous["PublicApi/api-old-ru"] {
		t.Fatal("an unattributable name must be reported once, not skipped in silence")
	}
}

func TestResolveSnapshotEnvSameSlugOnBothClaimants(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	owners := map[uuid.UUID]envOwner{
		a: {project: "ggrk52", environment: "prod"},
		b: {project: "ggrk52", environment: "prod"},
	}
	labels := map[string]string{projectLabel: "ggrk52", environmentLabel: "prod"}

	if _, ok := ambiguityReconciler().resolveSnapshotEnv("S3Bucket", "assets", []uuid.UUID{a, b}, labels, owners); ok {
		t.Fatal("two rows under the same project and environment cannot be told apart by labels")
	}
}

func TestAmbiguityIsReportedOnceAndClearsOnRecovery(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	owners := map[uuid.UUID]envOwner{a: {project: "p", environment: "prod"}, b: {project: "q", environment: "prod"}}

	r := ambiguityReconciler()
	r.resolveSnapshotEnv("PublicApi", "api-dup-ru", []uuid.UUID{a, b}, nil, owners)
	r.resolveSnapshotEnv("PublicApi", "api-dup-ru", []uuid.UUID{a, b}, nil, owners)
	if !r.ambiguous["PublicApi/api-dup-ru"] {
		t.Fatal("repeated ambiguity lost its once-per-process marker")
	}

	if _, ok := r.resolveSnapshotEnv("PublicApi", "api-dup-ru", []uuid.UUID{a}, nil, owners); !ok {
		t.Fatal("removing the duplicate row must let the name sync again")
	}
	if r.ambiguous["PublicApi/api-dup-ru"] {
		t.Fatal("recovered name stayed marked, so a relapse would never be reported")
	}
}

// TestClusterScopedPassesGoThroughResolveSnapshotEnv pins the wiring, not the
// helper. The helper landed first as dead code: every cluster-scoped pass still
// carried its own `len(ids) != 1 { continue }`, so the fix shipped green and
// changed nothing. The bare skip is the bug itself — it is silent and permanent
// — and it must exist in exactly one place, behind the resolver.
func TestClusterScopedPassesGoThroughResolveSnapshotEnv(t *testing.T) {
	src, err := os.ReadFile("statusreconciler.go")
	if err != nil {
		t.Fatalf("read reconciler source: %v", err)
	}
	if n := strings.Count(string(src), "len(ids) != 1"); n != 0 {
		t.Fatalf("%d cluster-scoped pass(es) still skip ambiguous names directly instead of calling resolveSnapshotEnv", n)
	}
	for _, pass := range []string{
		"func (r *StatusReconciler) reconcileModels(ctx context.Context, owners map[uuid.UUID]envOwner)",
		"func (r *StatusReconciler) reconcileDatabases(ctx context.Context, owners map[uuid.UUID]envOwner)",
		"func (r *StatusReconciler) reconcilePublicApis(ctx context.Context, owners map[uuid.UUID]envOwner)",
		"func (r *StatusReconciler) reconcileS3Buckets(ctx context.Context, owners map[uuid.UUID]envOwner)",
	} {
		if !strings.Contains(string(src), pass) {
			t.Fatalf("pass does not take snapshot ownership and so cannot resolve ties: %s", pass)
		}
	}
}
