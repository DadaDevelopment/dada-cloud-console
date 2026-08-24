package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/google/uuid"
)

// fakeAdopter stands in for adoption on the deploy path and counts how often it
// ran, which is what proves the retry is bounded.
type fakeAdopter struct {
	calls  int
	report adoptReport
	err    error
}

func (f *fakeAdopter) adopt(context.Context, db.Operation, string) (adoptReport, error) {
	f.calls++
	return f.report, f.err
}

func watcherAdopting(f *fakeAdopter) *DBWatcher {
	return &DBWatcher{adoptForDeployFn: f.adopt}
}

func deployOp() db.Operation {
	env := uuid.New()
	return db.Operation{ID: uuid.New(), ProjectID: uuid.New(), EnvironmentID: &env}
}

// TestClobberRefusalAdoptsInsteadOfSendingTheCallerAway is the point of the
// change: a deploy that trips the guard because git holds configuration the
// console never learned must adopt and render again by itself. Before this, the
// refusal named the adopt verb and the caller had to run it by hand -- which is
// exactly what happened to leadgen/prod/lead-gen on 2026-08-24.
func TestClobberRefusalAdoptsInsteadOfSendingTheCallerAway(t *testing.T) {
	f := &fakeAdopter{report: adoptReport{
		AdoptedPlain:     []string{"POSTGRES_HOST"},
		AdoptedSecretRef: []string{"BOT_TOKEN -> secret/telemost-bot-secrets:bot_token"},
		AdoptedShape:     []string{"port 8000"},
	}}
	guardErr := errors.New("would drop common.extraEnv.BOT_TOKEN")

	retry, err := watcherAdopting(f).adoptRetryAfterClobber(context.Background(), deployOp(), "lead-gen", guardErr, false)
	if err != nil {
		t.Fatalf("adoption learned the missing keys, so the deploy must go on, got: %v", err)
	}
	if !retry {
		t.Fatal("the render must be retried after adoption")
	}
	if f.calls != 1 {
		t.Fatalf("adoption ran %d times, want exactly once", f.calls)
	}
}

// TestRetriedDeployKeepsTheRefusalAndAdoptsNoSecondTime bounds the recursion:
// the render that follows an adoption gets the guard's answer as final, so no
// deploy can loop between adopting and refusing.
func TestRetriedDeployKeepsTheRefusalAndAdoptsNoSecondTime(t *testing.T) {
	f := &fakeAdopter{report: adoptReport{AdoptedPlain: []string{"ANYTHING"}}}
	guardErr := errors.New("would drop common.ingress")

	retry, err := watcherAdopting(f).adoptRetryAfterClobber(context.Background(), deployOp(), "lead-gen", guardErr, true)
	if retry {
		t.Fatal("a deploy that already adopted must not adopt and retry again")
	}
	if !errors.Is(err, guardErr) {
		t.Fatalf("err = %v, want the guard's own refusal", err)
	}
	if f.calls != 0 {
		t.Fatalf("adoption ran %d times on the retry, want 0", f.calls)
	}
}

// TestDropAdoptionCannotExplainIsStillRefused keeps the guard's teeth: adoption
// that adds nothing means the loss was never unlearned configuration, and a
// commit that really would delete an app's config must still be refused.
func TestDropAdoptionCannotExplainIsStillRefused(t *testing.T) {
	f := &fakeAdopter{report: adoptReport{}}
	guardErr := errors.New("would drop common.ingress")

	retry, err := watcherAdopting(f).adoptRetryAfterClobber(context.Background(), deployOp(), "web", guardErr, false)
	if retry {
		t.Fatal("adoption learned nothing, so there is nothing to retry with")
	}
	if !errors.Is(err, guardErr) {
		t.Fatalf("err = %v, want the guard's own refusal", err)
	}
}

// TestAdoptionFailureDoesNotHideTheGuard covers the case where adoption itself
// cannot run: the caller must still see why the deploy was refused, not why
// adoption failed, because the refusal is the fact about their app.
func TestAdoptionFailureDoesNotHideTheGuard(t *testing.T) {
	f := &fakeAdopter{err: errors.New("git remote unreachable")}
	guardErr := errors.New("would drop common.extraEnv.BOT_TOKEN")

	retry, err := watcherAdopting(f).adoptRetryAfterClobber(context.Background(), deployOp(), "web", guardErr, false)
	if retry {
		t.Fatal("nothing was adopted, so nothing may be retried")
	}
	if !errors.Is(err, guardErr) {
		t.Fatalf("err = %v, want the guard's own refusal", err)
	}
}
