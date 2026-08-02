package api

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/billing"
)

const agentChatFrontendAppDir = "../../../frontend/app"

// agentChatConsoleRoutesNotAdvertised are console pages that exist but are
// deliberately absent from agentChatConsoleRoutes, with the reason. A new page
// is a drift failure until someone triages it into one list or the other.
var agentChatConsoleRoutesNotAdvertised = map[string]string{
	"/billing/return": "payment-gateway return landing, reachable only as a redirect target from the PSP",
}

var agentChatPathToken = regexp.MustCompile(`(^|[\s"(])((?:/[A-Za-z0-9{}_-]+)+)`)

var agentChatCamelToken = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:[A-Z][A-Za-z0-9]*)+\b`)

// agentChatPromptNonToolIdentifiers are camelCase words in the prompt that name
// an argument or a URL placeholder rather than a tool.
var agentChatPromptNonToolIdentifiers = map[string]bool{
	"projectId":  true,
	"envId":      true,
	"appName":    true,
	"appId":      true,
	"buildId":    true,
	"serverName": true,
}

// agentChatRoutesFromDisk derives console routes from the Next.js app router
// tree: one route per page.tsx, (group) segments dropped, [param] rewritten to
// {param}. Returns nil when the frontend tree is not present.
func agentChatRoutesFromDisk(t *testing.T) map[string]bool {
	t.Helper()
	if _, err := os.Stat(agentChatFrontendAppDir); err != nil {
		return nil
	}
	routes := map[string]bool{}
	err := filepath.WalkDir(agentChatFrontendAppDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "page.tsx" {
			return nil
		}
		rel, err := filepath.Rel(agentChatFrontendAppDir, filepath.Dir(path))
		if err != nil {
			return err
		}
		var segs []string
		for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
			switch {
			case seg == "." || seg == "":
			case strings.HasPrefix(seg, "(") && strings.HasSuffix(seg, ")"):
			case strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]"):
				segs = append(segs, "{"+strings.Trim(seg, "[].")+"}")
			default:
				segs = append(segs, seg)
			}
		}
		if len(segs) == 0 {
			return nil
		}
		route := "/" + strings.Join(segs, "/")
		if strings.HasPrefix(route, "/en/") || route == "/en" {
			return nil
		}
		routes[route] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk frontend app dir: %v", err)
	}
	return routes
}

func agentChatConsoleRouteSet() map[string]bool {
	set := map[string]bool{}
	for _, r := range agentChatConsoleRoutes {
		set[r] = true
	}
	return set
}

// TestAgentChatConsoleRoutes_ExistOnDisk is the half of the drift guard that
// catches the shipped bug: the prompt advertised /billing and
// /projects/{projectId}/apps/{appName}/logs, neither of which is a page, so the
// assistant handed users clickable links straight to a 404.
func TestAgentChatConsoleRoutes_ExistOnDisk(t *testing.T) {
	disk := agentChatRoutesFromDisk(t)
	if disk == nil {
		t.Skipf("frontend tree not present at %s", agentChatFrontendAppDir)
	}
	for _, route := range agentChatConsoleRoutes {
		if !disk[route] {
			t.Errorf("prompt advertises %s, but no page.tsx renders it -- that link is a 404", route)
		}
	}
}

// TestAgentChatConsoleRoutes_CoverEveryConsolePage is the other half: a page
// added to the console must be either advertised or explicitly excused, so the
// assistant does not keep telling users a feature has no page.
func TestAgentChatConsoleRoutes_CoverEveryConsolePage(t *testing.T) {
	disk := agentChatRoutesFromDisk(t)
	if disk == nil {
		t.Skipf("frontend tree not present at %s", agentChatFrontendAppDir)
	}
	advertised := agentChatConsoleRouteSet()
	consolePrefixes := []string{"/projects", "/admin", "/ai-studio", "/deploy", "/billing"}

	var missing []string
	for route := range disk {
		isConsole := false
		for _, p := range consolePrefixes {
			if route == p || strings.HasPrefix(route, p+"/") {
				isConsole = true
				break
			}
		}
		if !isConsole || advertised[route] {
			continue
		}
		if _, excused := agentChatConsoleRoutesNotAdvertised[route]; excused {
			continue
		}
		missing = append(missing, route)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("console pages exist that the assistant will never link: %v (add them to agentChatConsoleRoutes or excuse them in agentChatConsoleRoutesNotAdvertised)", missing)
	}
}

// TestAgentChatSystemPrompt_LinksOnlyRealRoutes checks the prose too, not just
// the generated list: the two dead routes were hand-written sentences, not list
// entries.
func TestAgentChatSystemPrompt_LinksOnlyRealRoutes(t *testing.T) {
	prompt := agentChatSystemPrompt(agentChatRequest{})
	known := agentChatConsoleRouteSet()

	for _, m := range agentChatPathToken.FindAllStringSubmatch(prompt, -1) {
		path := m[2]
		if !known[path] {
			t.Errorf("prompt contains path %q, which is not a known console route", path)
		}
	}
}

// TestAgentChatConsoleRoutes_MatchTheFrontendAllowlist closes the last gap: the
// panel only turns a path into a link when it matches CONSOLE_ROUTES in
// frontend/lib/agent-chat-links.ts, so a route the prompt advertises but that
// allowlist does not know renders as dead text next to working links.
func TestAgentChatConsoleRoutes_MatchTheFrontendAllowlist(t *testing.T) {
	const linksFile = "../../../frontend/lib/agent-chat-links.ts"
	raw, err := os.ReadFile(linksFile)
	if err != nil {
		t.Skipf("frontend link allowlist not present at %s", linksFile)
	}
	allowed := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(/[^"]*)"`).FindAllStringSubmatch(string(raw), -1) {
		allowed[m[1]] = true
	}
	for _, route := range agentChatConsoleRoutes {
		ts := strings.NewReplacer("{", "[", "}", "]").Replace(route)
		if !allowed[ts] {
			t.Errorf("prompt advertises %s, but the panel allowlist has no %s -- the assistant's link renders as plain text", route, ts)
		}
	}
}

func TestAgentChatSystemPrompt_NamesOnlyToolsThatExist(t *testing.T) {
	prompt := agentChatSystemPrompt(agentChatRequest{})
	ts := agentChatTestToolset(t)

	for _, name := range agentChatCamelToken.FindAllString(prompt, -1) {
		if agentChatPromptNonToolIdentifiers[name] {
			continue
		}
		if !ts.Has(name) {
			t.Errorf("prompt names tool %q, which is not in the catalog -- the model will call an unknown tool or promise a capability that does not exist", name)
		}
	}
}

// TestAgentChatSystemPrompt_CannotDoListMatchesTheCatalog pins the honesty of
// the WHAT YOU CANNOT DO block. The shipped prompt claimed creating an app
// server "is the only thing you cannot do", while the catalog also had no tool
// for boxes, deletes, moves, archive upload, diagnosis or autofix -- so the
// assistant confidently promised half a dozen actions it could not perform.
// If any of these ever becomes a real tool, this test fails and the prompt must
// stop declaring it impossible.
func TestAgentChatSystemPrompt_CannotDoListMatchesTheCatalog(t *testing.T) {
	prompt := agentChatSystemPrompt(agentChatRequest{})
	ts := agentChatTestToolset(t)

	declaredUnavailable := []string{
		"createAppServer", "startAppServer",
		"createBox", "boxUp", "resumeBox", "suspendBox", "extendBox", "crystallizeBox", "deleteBox",
		"deleteApp", "deleteProject", "moveApp",
		"uploadSourceArchive",
		"diagnoseApp", "autofixApp", "triggerAutofix",
		"revealEnvVar", "getDatabaseCredentials", "getS3Credentials", "revealModelKey",
		"scaleApp", "updateAppReplicas", "addProjectMember", "createAIGatewayKey", "changeBillingPlan",
	}
	for _, name := range declaredUnavailable {
		if ts.Has(name) {
			t.Errorf("prompt tells the user %q is impossible for the assistant, but the catalog now exposes it", name)
		}
	}

	if strings.Contains(prompt, "is the only thing you cannot do") {
		t.Error("prompt still claims a single capability gap; the catalog has many")
	}
	for _, must := range []string{"WHAT YOU CANNOT DO", "Managed databases are PostgreSQL ONLY", "VERTICAL only", "no cron job", "PR preview environments are free"} {
		if !strings.Contains(prompt, must) {
			t.Errorf("prompt lost the %q statement", must)
		}
	}
}

// TestAgentChatSystemPrompt_FreePlanQuotaMatchesBilling pins the one product
// number the prompt states outright against the file the running binary
// enforces. The prompt claimed two Free apps for a while after commit 91d5a92
// lowered the quota to one, which turns the agent into a source of false
// promises at exactly the moment it proposes createApp.
func TestAgentChatSystemPrompt_FreePlanQuotaMatchesBilling(t *testing.T) {
	plans, err := billing.LoadPlans("")
	if err != nil {
		t.Fatalf("load plans: %v", err)
	}
	apps := -1
	for _, p := range plans {
		if p.Key == "free" {
			apps = p.Quotas.Apps
		}
	}
	if apps < 0 {
		t.Fatal("no free plan in the embedded plan set")
	}

	prompt := agentChatSystemPrompt(agentChatRequest{})
	want := fmt.Sprintf("the Free plan allows %d app", apps)
	if !strings.Contains(prompt, want) {
		t.Fatalf("prompt does not state the real Free app quota (%d): missing %q", apps, want)
	}
	for n := 0; n <= 10; n++ {
		if n == apps {
			continue
		}
		if bad := fmt.Sprintf("the Free plan allows %d app", n); strings.Contains(prompt, bad) {
			t.Fatalf("prompt states a Free app quota the binary does not enforce: %q", bad)
		}
	}
}

// TestAgentChatSystemPrompt_ProjectSlugRuleMatchesTheValidator pins the naming
// rule against projectSlugRe: the prompt used to fold the project slug into the
// generic 63-character rule, which lets the model propose slugs the backend
// rejects and burn the turn's tool budget on the retry.
func TestAgentChatSystemPrompt_ProjectSlugRuleMatchesTheValidator(t *testing.T) {
	prompt := agentChatSystemPrompt(agentChatRequest{})
	if !strings.Contains(prompt, "createProject's slug is stricter still: 3 to 40 characters") {
		t.Fatal("prompt does not state the project slug length rule")
	}
	for _, bad := range []string{"ab", "a-slug-that-is-far-longer-than-the-forty-character-ceiling", "1shop", "-shop"} {
		if projectSlugRe.MatchString(bad) {
			t.Fatalf("validator accepts %q, so the prompt rule stated is wrong", bad)
		}
	}
	for _, ok := range []string{"shop", "my-shop-2"} {
		if !projectSlugRe.MatchString(ok) {
			t.Fatalf("validator rejects %q, so the prompt rule stated is wrong", ok)
		}
	}
}
