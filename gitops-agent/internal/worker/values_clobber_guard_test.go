package worker

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// handMaintainedValues is internal/prod/telemost-bot as it stood in argo-infra
// on 2026-08-21: eight environment variables, a service port of 8000 and
// useDotEnv, none of which the console's database has ever heard of, plus an
// ingress block outside the console's ownership.
const handMaintainedValues = `common:
  image:
    repository: reg/telemost-bot
    tag: master-1.0.0-42
  servicePort: 8000
  useDotEnv: true
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

// consoleRenderKnowingOnlyTheImage is what RenderAppValues emits for an app the
// console only knows the image of -- the shape every env-var save produces for a
// hand-maintained app, because env_vars holds no rows for it.
const consoleRenderKnowingOnlyTheImage = `common:
  image:
    repository: reg/telemost-bot
    tag: master-1.0.0-42
  replicas: 1
`

// TestGuardValuesClobber_RefusesTheEnvSaveThatStrippedTelemostBot is the
// regression test for 2026-08-21: setEnvVar through MCP re-rendered
// internal/prod/telemost-bot from a database that knew only its image, and the
// merge deleted eight extraEnv entries, servicePort 8000 and useDotEnv, because
// all three are ownedCommonKeys and the render was silent about them. The bot
// came back up with no Postgres.
//
// It is the same class as the 2026-08-02 loss on the same app, which was fixed
// only for the resize endpoint. The guard existed but was scoped to
// op.Unattended(), and an MCP call runs under a service account, so it was
// exempt.
func TestGuardValuesClobber_RefusesTheEnvSaveThatStrippedTelemostBot(t *testing.T) {
	const valuesPath = "projects/internal/prod/telemost-bot/values.yaml"
	mgr := locatorRepo(t, map[string]string{valuesPath: handMaintainedValues})

	merged, err := renderer.MergeAppValues(handMaintainedValues, consoleRenderKnowingOnlyTheImage)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	w := &DBWatcher{}
	err = w.guardValuesClobber(mgr, "telemost-bot", valuesPath, merged, nil)
	if err == nil {
		t.Fatal("an env save that deletes extraEnv, servicePort and useDotEnv must be refused, got nil")
	}
	for _, want := range []string{"extraEnv", "servicePort", "useDotEnv"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %s, got: %v", want, err)
		}
	}
}

// TestGuardValuesClobber_IgnoresKeysTheMergePreserves proves the guard measures
// the bytes about to be committed and not the raw render. common.ingress is
// outside ownedCommonKeys, so the merge carries it through untouched -- reporting
// it would fail deploys over a loss that never happens, which is why the guard
// could not be turned on for every deploy until it moved behind the merge.
func TestGuardValuesClobber_IgnoresKeysTheMergePreserves(t *testing.T) {
	merged, err := renderer.MergeAppValues(handMaintainedValues, consoleRenderKnowingOnlyTheImage)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if strings.Contains(mergedDropReport(t, handMaintainedValues, merged), "ingress") {
		t.Fatalf("merge preserves common.ingress; the guard must not report it")
	}
}

// TestGuardValuesClobber_AllowsTheDropTheOperationDeclared covers deleteEnvVar:
// removing a key has to be able to remove it from git, so an operation that
// declares the path it means to drop passes, while an undeclared loss in the
// same file still fails.
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
	merged, err := renderer.MergeAppValues(existing, rendered)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	w := &DBWatcher{}
	if err := w.guardValuesClobber(mgr, "web", valuesPath, merged,
		[]string{"common.extraEnv.DROP_ME"}); err != nil {
		t.Fatalf("a declared drop must be allowed, got: %v", err)
	}
	if err := w.guardValuesClobber(mgr, "web", valuesPath, merged, nil); err == nil {
		t.Fatal("the same drop with nothing declared must be refused")
	}
}

func mergedDropReport(t *testing.T, existing, merged string) string {
	t.Helper()
	return renderer.DescribeDropped(renderer.DroppedPaths(existing, merged))
}
