package api

import (
	"strings"
	"testing"

	"github.com/dada-tuda/console/backend/internal/agentchat"
)

// TestAgentChatConsolePathIsRoute_GatesWhatTheAssistantMayOpen is the gate in
// front of open_console_page. The model fills the placeholders itself, so this
// is the only thing standing between a guessed path and a user whose page was
// yanked to a 404 without them clicking anything.
func TestAgentChatConsolePathIsRoute_GatesWhatTheAssistantMayOpen(t *testing.T) {
	cases := []struct {
		path string
		want bool
		why  string
	}{
		{"/projects", true, "a static route"},
		{"/projects/7a387969-e082-415c-8b61-1f53f7e18295/git/import", true, "a real id in a placeholder segment"},
		{"/projects/p1/apps/api/builds/42", true, "two placeholders in one route"},
		{"/projects/p1/apps/api#logs", true, "a hash targets an anchor on a page that exists"},
		{"/projects/p1/databases?tab=backups", true, "a query targets a page that exists"},
		{"/projects/p1/apps/api/logs", false, "an app logs page was never built"},
		{"/billing", false, "there is no top-level billing page"},
		{"/projects/p1/", true, "a trailing slash addresses the same page"},
		{"/projects//apps", false, "an empty placeholder segment is not a page"},
		{"https://console.example.com/projects", false, "an absolute URL is not a console path"},
		{"//evil.example.com/projects", false, "a protocol-relative URL leaves the console"},
		{"projects/p1", false, "a path must be rooted"},
		{"", false, "nothing at all"},
	}
	for _, c := range cases {
		if got := agentChatConsolePathIsRoute(c.path); got != c.want {
			t.Errorf("agentChatConsolePathIsRoute(%q) = %v, want %v -- %s", c.path, got, c.want, c.why)
		}
	}
}

// TestAgentChatSystemPrompt_TellsTheAssistantItCanMoveTheUser guards the point
// of the tool: the console is the product's own UI, so an answer that ends in
// "go to /projects/{id}/git" leaves the user a click that the assistant could
// have performed. The prompt has to say the tool exists and that it is one page
// per turn, otherwise the model keeps writing directions.
func TestAgentChatSystemPrompt_TellsTheAssistantItCanMoveTheUser(t *testing.T) {
	prompt := agentChatTestPrompt(t)

	links := prompt[strings.Index(prompt, "# LINKS"):]
	if cut := strings.Index(links[len("# LINKS"):], "\n# "); cut >= 0 {
		links = links[:len("# LINKS")+cut]
	}
	if !strings.Contains(links, agentchat.OpenPageTool) {
		t.Errorf("the LINKS section never mentions %s, so the assistant will keep asking the user to navigate by hand", agentchat.OpenPageTool)
	}
	if !strings.Contains(links, "once per turn") {
		t.Error("the LINKS section does not bound navigation to one page per turn; a model that moves the page twice moves it out from under the user mid-read")
	}
}
