package api

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/agentchat"
)

// agentChatCardFixture is one write tool's confirmation card pinned against the
// tool's REAL generated argument schema.
//
// args uses only field names the swagger-derived schema actually emits -- that
// is the whole point of the fixture. The model fills the generated schema, so a
// card that reads a field name the schema never produces renders an empty
// target and asks the user to approve a blank action: 'Connect GitHub
// repository ""', 'Start ownership verification for domain ""'. Both shipped.
//
// want lists substrings the card must show; deny lists substrings it must never
// show (secret values).
type agentChatCardFixture struct {
	args map[string]any
	want []string
	deny []string
}

var agentChatCardFixtures = map[string]agentChatCardFixture{
	"restartApp":         {args: map[string]any{"appName": "api"}, want: []string{"api"}},
	"triggerBuild":       {args: map[string]any{"appName": "api"}, want: []string{"api"}},
	"deployTrigger":      {args: map[string]any{"image": "ghcr.io/acme/api:v2"}, want: []string{"ghcr.io/acme/api:v2"}},
	"cancelBuild":        {args: map[string]any{"buildId": "b-9"}, want: []string{"b-9"}},
	"retryOperation":     {args: map[string]any{"operationId": "op-3"}, want: []string{"op-3"}},
	"setEnvVar":          {args: map[string]any{"appName": "api", "key": "TOKEN", "value": "sk_live_leak"}, want: []string{"TOKEN", "api"}, deny: []string{"sk_live_leak"}},
	"deleteEnvVar":       {args: map[string]any{"appName": "api", "key": "TOKEN"}, want: []string{"TOKEN", "api"}},
	"rollbackApp":        {args: map[string]any{"appName": "api"}, want: []string{"api"}},
	"rollbackDeployment": {args: map[string]any{"deploymentId": "d-1"}, want: []string{"d-1"}},
	"promoteDeployment":  {args: map[string]any{"deploymentId": "d-2"}, want: []string{"d-2"}},
	"updateAppImage":     {args: map[string]any{"appName": "api", "image": "ghcr.io/acme/api:v3"}, want: []string{"api", "ghcr.io/acme/api:v3"}},
	"updateAppProfile":   {args: map[string]any{"appName": "api", "profile": "medium"}, want: []string{"api", "medium"}},
	"updateAppStorage":   {args: map[string]any{"appName": "api", "size": "10Gi", "path": "/data"}, want: []string{"api", "10Gi", "/data", "never shrunk"}},
	"triggerAutofix":     {args: map[string]any{"appName": "api", "error": "npm ci failed"}, want: []string{"api", "npm ci failed", "pull request"}},
	"probeAppNetwork":    {args: map[string]any{"appName": "api", "target": "s3.ru1.storage.beget.cloud", "port": float64(443)}, want: []string{"api", "s3.ru1.storage.beget.cloud", "443"}},

	"createDatabase": {args: map[string]any{"name": "orders", "database": "orders", "app_ref": "api", "backup_enabled": true}, want: []string{"orders", "api", "backups enabled", "PostgreSQL"}},
	"createEndpoint": {args: map[string]any{"appName": "api", "fqdn": "api.example.ru", "auth_enabled": false}, want: []string{"api.example.ru", "api", "no auth"}},
	"createS3Bucket": {args: map[string]any{"name": "media", "bucket_name": "acme-media", "region": "ru-1", "public": true}, want: []string{"media", "acme-media", "ru-1", "publicly readable"}},

	"createApp": {
		args: map[string]any{
			"name": "bot", "image": "ghcr.io/acme/bot:v1", "framework": "python", "worker": true,
			"profile": "large", "replicas": float64(5), "port": float64(8080), "workload_type": "StatefulSet",
			"volume": map[string]any{"path": "/data", "size": "50Gi", "storage_class": "longhorn-prod"},
		},
		want: []string{"bot", "ghcr.io/acme/bot:v1", "python", "worker", "quota", "large", "5 replica", "8080", "StatefulSet", "50Gi", "/data", "never shrunk"},
	},
	"createProject":        {args: map[string]any{"slug": "shop", "display_name": "Shop", "default_environment": "prod"}, want: []string{"shop", "Shop", "prod"}},
	"ensureDefaultProject": {args: map[string]any{}, want: []string{"default project"}},
	"connectGitRepo":       {args: map[string]any{"repo_full_name": "acme/api", "production_branch": "main", "app_name": "api"}, want: []string{"acme/api", "main", "api"}},

	"addDomainAuthorization":    {args: map[string]any{"apex_domain": "biba.ru"}, want: []string{"biba.ru"}},
	"verifyDomainAuthorization": {args: map[string]any{"id": "auth-1"}, want: []string{"auth-1"}},
	"attachHostname":            {args: map[string]any{"appName": "api", "hostname": "www.biba.ru"}, want: []string{"www.biba.ru", "api"}},
	"upsertManagedRecord":       {args: map[string]any{"authId": "a-1", "type": "A", "name": "@", "contents": []any{"1.2.3.4"}}, want: []string{"A", "@", "1.2.3.4"}},

	"createDatabaseBackup":   {args: map[string]any{"name": "orders"}, want: []string{"orders"}},
	"downloadDatabaseBackup": {args: map[string]any{"name": "orders", "backupId": "b-7"}, want: []string{"orders", "b-7", "no login"}},
	"restoreDatabase":        {args: map[string]any{"name": "orders", "backup_id": "b-7"}, want: []string{"orders", "b-7", "OVERWRITES"}},

	"bulkSetEnvVars":   {args: map[string]any{"appName": "api", "vars": []any{map[string]any{"key": "A_KEY", "value": "sk_live_leak"}, map[string]any{"key": "B_KEY"}}}, want: []string{"A_KEY", "B_KEY", "api"}, deny: []string{"sk_live_leak"}},
	"createDeployHook": {args: map[string]any{"appName": "api", "name": "ci"}, want: []string{"ci", "api"}},
}

func agentChatTestToolset(t *testing.T) *agentchat.Toolset {
	t.Helper()
	ts, err := agentchat.BuildToolset(EmbeddedSpec(), "http://backend.test")
	if err != nil {
		t.Fatalf("build toolset: %v", err)
	}
	return ts
}

// agentChatWriteToolSchemas returns every confirmation-gated tool with the set
// of argument names its generated schema actually carries.
func agentChatWriteToolSchemas(t *testing.T) map[string]map[string]bool {
	t.Helper()
	ts := agentChatTestToolset(t)
	out := map[string]map[string]bool{}
	for _, def := range ts.Defs {
		name := def.Function.Name
		if !ts.IsWrite(name) {
			continue
		}
		props := map[string]bool{}
		if params, ok := def.Function.Parameters["properties"].(map[string]any); ok {
			for k := range params {
				props[k] = true
			}
		}
		out[name] = props
	}
	return out
}

// TestAgentChatConfirmSummary_ArgNamesMatchTheGeneratedSchema is the guard that
// makes the card/schema mismatch impossible to reintroduce silently: every
// argument name a fixture feeds the card must be a real property of that tool's
// generated schema, so renaming a request field in the Go DTO breaks this test
// instead of quietly blanking a production confirmation card.
func TestAgentChatConfirmSummary_ArgNamesMatchTheGeneratedSchema(t *testing.T) {
	schemas := agentChatWriteToolSchemas(t)

	for name, fixture := range agentChatCardFixtures {
		props, ok := schemas[name]
		if !ok {
			t.Errorf("%s: fixture exists but the tool is not a confirmation-gated write tool any more", name)
			continue
		}
		for key := range fixture.args {
			if !props[key] {
				var have []string
				for k := range props {
					have = append(have, k)
				}
				sort.Strings(have)
				t.Errorf("%s: card reads argument %q, which its schema does not emit; schema has %v", name, key, have)
			}
		}
	}
}

func TestAgentChatConfirmSummary_EveryWriteToolHasAFixture(t *testing.T) {
	for name := range agentChatWriteToolSchemas(t) {
		if _, ok := agentChatCardFixtures[name]; !ok {
			t.Errorf("%s is confirmation-gated but has no card fixture, so nobody checks what its card says", name)
		}
	}
}

func TestAgentChatConfirmSummary_RendersTheRealTarget(t *testing.T) {
	for name, fixture := range agentChatCardFixtures {
		raw, err := json.Marshal(fixture.args)
		if err != nil {
			t.Fatalf("%s: marshal fixture: %v", name, err)
		}
		summary := agentChatConfirmSummary(name, string(raw), "proj", "prod")

		if strings.HasPrefix(summary, "Run "+name) {
			t.Errorf("%s: fell through to the generic placeholder summary %q", name, summary)
		}
		if strings.Contains(summary, `""`) {
			t.Errorf("%s: card has an empty quoted field, the user would approve a blank target: %q", name, summary)
		}
		for _, want := range fixture.want {
			if !strings.Contains(summary, want) {
				t.Errorf("%s: summary %q is missing %q", name, summary, want)
			}
		}
		for _, deny := range fixture.deny {
			if strings.Contains(summary, deny) {
				t.Errorf("%s: summary leaked a secret value %q: %q", name, deny, summary)
			}
		}
	}
}

// TestAgentChatSummaryFor_ProjectScopedToolsResolveNames pins that every write
// tool carrying its own projectId gets project/env names resolved for its card.
func TestAgentChatSummaryFor_EveryProjectScopedWriteToolResolvesNames(t *testing.T) {
	schemas := agentChatWriteToolSchemas(t)
	appScoped := map[string]bool{
		"restartApp": true, "triggerBuild": true, "deployTrigger": true, "cancelBuild": true,
		"retryOperation": true, "setEnvVar": true, "deleteEnvVar": true, "rollbackApp": true,
		"rollbackDeployment": true, "promoteDeployment": true, "updateAppImage": true,
		"updateAppProfile": true, "updateAppStorage": true, "ensureDefaultProject": true,
		"createProject": true, "probeAppNetwork": true, "triggerAutofix": true,
	}
	for name, props := range schemas {
		if appScoped[name] || !props["projectId"] {
			continue
		}
		if !toolsNeedingProjectEnvNames[name] {
			t.Errorf("%s carries its own projectId but is not name-resolved for the card", name)
		}
	}
}
