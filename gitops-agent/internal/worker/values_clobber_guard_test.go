package worker

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// handMaintainedValues is internal/prod/telemost-bot as it stood in argo-infra
// on 2026-08-21: eight environment variables, a service port of 8000 and
// useDotEnv, none of which the console's database has ever heard of, plus an
// ingress block outside the console's ownership.
const handMaintainedValues = `common:
  image:
    name: nexus.dada-tuda.ru/dada/telemost-bot
    tag: master-1.0.0-42
  servicePort: 8000
  useDotEnv: "true"
  extraEnv:
    - name: BOT_TOKEN
      valueFrom:
        secretKeyRef:
          name: telemost-bot-secrets
          key: bot_token
    - name: POSTGRES_HOST
      value: telemost-bot-db
    - name: POSTGRES_PORT
      value: "5432"
    - name: POSTGRES_DB
      value: telemost
    - name: POSTGRES_USER
      value: telemost
    - name: POSTGRES_PASSWORD
      valueFrom:
        secretKeyRef:
          name: telemost-bot-db-credentials
          key: password
    - name: KEYCLOAK_URL
      value: https://auth.dada-tuda.ru
    - name: LOG_LEVEL
      value: info
  ingress:
    enabled: true
    hosts:
      - host: bot.example.ru
`

// consoleRenderKnowingOnlyTheImage is the real render for an app the console
// only knows the image, the framework-guessed port and one freshly saved
// variable of -- the shape every env-var save produces for a hand-maintained
// app, because env_vars holds no rows for the other eight.
//
// It comes out of RenderAppValues rather than being handwritten. The
// handwritten version of this fixture was silent about servicePort and
// useDotEnv, which made the loss look like two DELETIONS the clobber guard
// could see; the real render EMITS both, so the production loss was two
// in-place CHANGES that DroppedPaths can never report. That is why this test
// passed while the bot was down.
func consoleRenderKnowingOnlyTheImage(t *testing.T) string {
	t.Helper()
	rendered, err := renderer.RenderAppValues(renderer.AppSpec{
		Name:     "telemost-bot",
		Image:    "nexus.dada-tuda.ru/dada/telemost-bot:master-1.0.0-42",
		Replicas: 1,
		Port:     8080,
		Env:      map[string]string{"AGENTSYNC_BASE_URL": "https://agentsync.dada-tuda.ru"},
	})
	if err != nil {
		t.Fatalf("RenderAppValues: %v", err)
	}
	return rendered
}

// mergeAsProduction merges the way doDeployImageVersion does for an app whose
// port nobody chose: servicePort and service are guesses, useDotEnv always is.
func mergeAsProduction(t *testing.T, existing, rendered string, expectedDrops ...string) string {
	t.Helper()
	merged, err := renderer.MergeAppValuesWith(existing, rendered, renderer.MergeOptions{
		Advisory:      advisoryValuesKeys(map[string]any{"port_source": "framework_default"}),
		ExpectedDrops: expectedDrops,
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	return merged
}

// TestEnvSave_NoLongerStripsTelemostBot is the regression test for 2026-08-21:
// setEnvVar through MCP re-rendered internal/prod/telemost-bot from a database
// that knew only its image, and the merge deleted eight extraEnv entries and
// moved servicePort 8000 -> 8080 and useDotEnv true -> false, because all three
// are ownedCommonKeys. The bot came back up with no Postgres and a Service
// pointing at a port nothing listened on.
//
// The fix is in the merge, not in the refusal: the save has to SUCCEED, and it
// has to land the new variable and nothing else. A guard alone would only have
// converted a broken bot into a lever that answers "no" to every env save on
// every hand-maintained app.
func TestEnvSave_NoLongerStripsTelemostBot(t *testing.T) {
	const valuesPath = "projects/internal/prod/telemost-bot/values.yaml"
	mgr := locatorRepo(t, map[string]string{valuesPath: handMaintainedValues})

	merged := mergeAsProduction(t, handMaintainedValues, consoleRenderKnowingOnlyTheImage(t))

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(merged), &doc); err != nil {
		t.Fatalf("merged values do not parse: %v\n%s", err, merged)
	}
	c, _ := doc["common"].(map[string]any)
	if c == nil {
		t.Fatalf("no common mapping: %s", merged)
	}
	if c["servicePort"] != 8000 {
		t.Errorf("servicePort = %v, want the 8000 the bot listens on", c["servicePort"])
	}
	if c["useDotEnv"] != "true" {
		t.Errorf("useDotEnv = %v, want the \"true\" that is in git", c["useDotEnv"])
	}

	env, _ := c["extraEnv"].([]any)
	names := map[string]bool{}
	for _, e := range env {
		if entry, ok := e.(map[string]any); ok {
			names[fmt.Sprint(entry["name"])] = true
		}
	}
	for _, want := range []string{
		"BOT_TOKEN", "POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_DB",
		"POSTGRES_USER", "POSTGRES_PASSWORD", "KEYCLOAK_URL", "LOG_LEVEL",
	} {
		if !names[want] {
			t.Errorf("%s lives only in git and must survive an env save: %#v", want, env)
		}
	}
	if !names["AGENTSYNC_BASE_URL"] {
		t.Errorf("the variable the user just saved must land: %#v", env)
	}

	w := &DBWatcher{}
	if err := w.guardValuesClobber(mgr, "telemost-bot", valuesPath, merged, nil); err != nil {
		t.Fatalf("the env save must go through, got: %v", err)
	}
}

// TestEnvSave_StillRefusesAnUndeclaredDrop keeps the guard honest: it is the
// backstop for whatever the merge does not preserve, so it must still refuse a
// render that would delete a key only git knows.
func TestEnvSave_StillRefusesAnUndeclaredDrop(t *testing.T) {
	const valuesPath = "projects/internal/prod/telemost-bot/values.yaml"
	mgr := locatorRepo(t, map[string]string{valuesPath: handMaintainedValues})

	stripped := strings.Replace(handMaintainedValues, "  ingress:\n", "  gone:\n", 1)
	w := &DBWatcher{}
	if err := w.guardValuesClobber(mgr, "telemost-bot", valuesPath, stripped, nil); err == nil {
		t.Fatal("a commit that deletes common.ingress must be refused")
	}
}

// TestGuardValuesClobber_IgnoresKeysTheMergePreserves proves the guard measures
// the bytes about to be committed and not the raw render. common.ingress is
// outside ownedCommonKeys, so the merge carries it through untouched -- reporting
// it would fail deploys over a loss that never happens, which is why the guard
// could not be turned on for every deploy until it moved behind the merge.
func TestGuardValuesClobber_IgnoresKeysTheMergePreserves(t *testing.T) {
	merged := mergeAsProduction(t, handMaintainedValues, consoleRenderKnowingOnlyTheImage(t))
	if report := mergedDropReport(t, handMaintainedValues, merged); report != "" {
		t.Fatalf("the merge preserves everything git owns; the guard must report nothing, got: %s", report)
	}
}

// TestGuardValuesClobber_AllowsTheDropTheOperationDeclared covers deleteEnvVar:
// removing a key has to be able to remove it from git, so an operation that
// declares the path it means to drop carries that declaration through both the
// merge and the guard, while a render that is merely silent about the same
// entry no longer removes it at all.
func TestGuardValuesClobber_AllowsTheDropTheOperationDeclared(t *testing.T) {
	const valuesPath = "projects/acme/prod/web/values.yaml"
	existing := `common:
  image:
    repository: reg/web
    tag: v1
  extraEnv:
    - name: KEEP
      value: "1"
    - name: DROP_ME
      value: "2"
`
	rendered := `common:
  image:
    repository: reg/web
    tag: v1
  extraEnv:
    - name: KEEP
      value: "1"
`
	mgr := locatorRepo(t, map[string]string{valuesPath: existing})

	declared := mergeAsProduction(t, existing, rendered, "common.extraEnv.DROP_ME")
	if strings.Contains(declared, "DROP_ME") {
		t.Fatalf("a declared drop must leave the file, got:\n%s", declared)
	}
	w := &DBWatcher{}
	if err := w.guardValuesClobber(mgr, "web", valuesPath, declared,
		[]string{"common.extraEnv.DROP_ME"}); err != nil {
		t.Fatalf("a declared drop must be allowed, got: %v", err)
	}

	silent := mergeAsProduction(t, existing, rendered)
	if !strings.Contains(silent, "DROP_ME") {
		t.Fatal("silence about an entry is not an instruction to delete it")
	}
	if err := w.guardValuesClobber(mgr, "web", valuesPath, silent, nil); err != nil {
		t.Fatalf("nothing is dropped, so nothing may be refused, got: %v", err)
	}
}

func mergedDropReport(t *testing.T, existing, merged string) string {
	t.Helper()
	return renderer.DescribeDropped(renderer.DroppedPaths(existing, merged))
}
