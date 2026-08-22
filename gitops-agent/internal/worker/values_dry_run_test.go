package worker

import (
	"errors"
	"strings"
	"testing"
)

// handMaintained is the shape that cost internal/prod/telemost-bot its
// configuration: a values.yaml assembled by hand, carrying keys the console has
// no rows for.
const handMaintained = `common:
  servicePort: 8000
  useDotEnv: true
  extraEnv:
    - name: PGHOST
      value: db.internal
    - name: PGUSER
      value: bot
`

func TestBuildValuesPlan_NamesTheLossesTheDeployWouldBeRefusedFor(t *testing.T) {
	rendered := `common:
  servicePort: 8080
  extraEnv:
    - name: NEW_KEY
      value: x
`
	plan := buildValuesPlan(handMaintained, nil, rendered, "apps/bot/values.yaml", nil)

	if plan.FirstWrite {
		t.Fatal("a file that exists was reported as a first write")
	}
	removed := strings.Join(plan.Removed, ",")
	for _, want := range []string{"common.extraEnv.PGHOST", "common.extraEnv.PGUSER", "common.useDotEnv"} {
		if !strings.Contains(removed, want) {
			t.Errorf("plan does not report losing %s (removed=%v)", want, plan.Removed)
		}
	}
	if !contains(plan.Changed, "common.servicePort") {
		t.Errorf("servicePort 8000 -> 8080 is not reported as changed: %v", plan.Changed)
	}
	if !contains(plan.Added, "common.extraEnv.NEW_KEY") {
		t.Errorf("the variable being written is not reported as added: %v", plan.Added)
	}
	if len(plan.WouldBlock) == 0 {
		t.Fatal("a write that deletes hand-maintained keys was not flagged as one the guard would refuse")
	}
	if !strings.Contains(plan.Verdict, "WOULD BE REFUSED") {
		t.Errorf("verdict %q does not say the real deploy would be refused", plan.Verdict)
	}
}

func TestBuildValuesPlan_ExpectedDropIsNotABlocker(t *testing.T) {
	rendered := `common:
  servicePort: 8000
  useDotEnv: true
  extraEnv:
    - name: PGUSER
      value: bot
`
	plan := buildValuesPlan(handMaintained, nil, rendered, "apps/bot/values.yaml", []string{"common.extraEnv.PGHOST"})

	if len(plan.WouldBlock) != 0 {
		t.Errorf("deleting the very variable the caller declared was flagged as a clobber: %v", plan.WouldBlock)
	}
	if !contains(plan.Removed, "common.extraEnv.PGHOST") {
		t.Errorf("the declared removal is missing from the plan: %v", plan.Removed)
	}
	if strings.Contains(plan.Verdict, "REFUSED") {
		t.Errorf("verdict %q refuses a delete the caller asked for", plan.Verdict)
	}
}

func TestBuildValuesPlan_MissingFileIsAFirstWrite(t *testing.T) {
	plan := buildValuesPlan("", errors.New("no such file"), "common: {}\n", "apps/bot/values.yaml", nil)
	if !plan.FirstWrite || len(plan.WouldBlock) != 0 {
		t.Fatalf("first write reported as dangerous: %+v", plan)
	}
	if !plan.DryRun {
		t.Error("plan does not mark itself as a dry run, so a reader cannot tell it from a result")
	}
}

func TestOverlayPendingEnv_PutsTheUnwrittenChangeIntoTheRender(t *testing.T) {
	env := resolvedEnv{
		Plain:  map[string]string{"PGHOST": "db.internal"},
		Secret: map[string]string{"BOT_TOKEN": "s3cret"},
	}

	overlayPendingEnv(env, []string{"NEW_KEY", "BOT_TOKEN"}, []string{"PGHOST"})

	if _, still := env.Plain["PGHOST"]; still {
		t.Error("a variable the caller means to delete is still in the render, so the plan would not show the delete")
	}
	if env.Plain["NEW_KEY"] == "" {
		t.Error("the variable being written is absent from the render, so the plan would describe every consequence except the write")
	}
	if env.Secret["BOT_TOKEN"] == "s3cret" {
		t.Error("a pending write to a secret variable left the stored value in place")
	}
	if _, leaked := env.Plain["BOT_TOKEN"]; leaked {
		t.Error("a secret variable was moved into the plaintext half of the render")
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
