package worker

import (
	"testing"

	"github.com/google/uuid"
)

// A tenant app and a kagent-rendered Deployment share the name tg-vibecoder.
// The kagent one lives in namespace kagent, which is no environment namespace,
// so only the name-based fallback could attribute it; because the app already
// runs at home it must be skipped, otherwise its replica is summed into the
// snapshot and re-rendered into git on the next deploy.
func TestResolveWorkloadEnvSkipsForeignTwinOfHomedApp(t *testing.T) {
	env := uuid.New()
	envByNs := map[string]uuid.UUID{"agent-sandbox-prod": env}
	envNames := map[string]bool{"prod": true}
	refs := []workloadRef{
		{name: "tg-vibecoder-deploy", ns: "agent-sandbox-prod"},
		{name: "tg-vibecoder", ns: "kagent", labels: map[string]string{"app.kubernetes.io/managed-by": "kagent"}},
	}
	home := homeKeys(refs, envByNs, envNames)
	appEnvs := map[string][]uuid.UUID{"tg-vibecoder": {env}}

	if got, ok := resolveWorkloadEnv("tg-vibecoder", "agent-sandbox-prod", envByNs, appEnvs, nil, home); !ok || got != env {
		t.Fatalf("home workload: got (%v, %v), want (%v, true)", got, ok, env)
	}
	if _, ok := resolveWorkloadEnv("tg-vibecoder", "kagent", envByNs, appEnvs, nil, home); ok {
		t.Fatal("the kagent twin of an app that runs in its own namespace must not be attributed to it")
	}
}

// Namespace-override apps and adopted infra have no workload at home; the
// name-based fallback is the only way to attribute them and must keep working.
func TestResolveWorkloadEnvStillAttributesOverrideApps(t *testing.T) {
	env := uuid.New()
	envByNs := map[string]uuid.UUID{"platform-prod": env}
	refs := []workloadRef{{name: "jenkins", ns: "devops-tools"}}
	home := homeKeys(refs, envByNs, map[string]bool{"prod": true})

	live := map[string][]uuid.UUID{"jenkins": {env}}
	if got, ok := resolveWorkloadEnv("jenkins", "devops-tools", envByNs, live, nil, home); !ok || got != env {
		t.Fatalf("override app via live map: got (%v, %v), want (%v, true)", got, ok, env)
	}
	orphaned := map[string][]uuid.UUID{"jenkins": {env}}
	if got, ok := resolveWorkloadEnv("jenkins", "devops-tools", envByNs, nil, orphaned, home); !ok || got != env {
		t.Fatalf("override app via orphan fallback: got (%v, %v), want (%v, true)", got, ok, env)
	}
	if _, ok := resolveWorkloadEnv("jenkins", "devops-tools", envByNs, nil, nil, home); ok {
		t.Fatal("a name with no snapshot anywhere must stay unattributed")
	}
}
