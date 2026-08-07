package api

import "testing"

// TestAgentChatNavigationPromised_KeepsThePromiseTheModelBreaks is the gate that
// replaces the prompt rule. Six production replays in a row never called
// open_console_page and two of them announced the move anyway -- the second on
// the build that added the ban -- so the sentence "I am opening that page" is
// made true here instead of being asked for in words.
func TestAgentChatNavigationPromised_KeepsThePromiseTheModelBreaks(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			"a russian promise with one page",
			"Открою вам страницу приложений: /projects/7a387969-e082-415c-8b61-1f53f7e18295/apps — перетащите туда папку.",
			"/projects/7a387969-e082-415c-8b61-1f53f7e18295/apps",
		},
		{
			"an english promise with one page",
			"I'll open /projects/p1/git/import for you now.",
			"/projects/p1/git/import",
		},
		{
			"a promise wrapped in a markdown link",
			"Сейчас открываю [список приложений](/projects/p1/apps).",
			"/projects/p1/apps",
		},
		{
			"a promise whose path ends the sentence",
			"Перевожу вас на /projects/p1/domains.",
			"/projects/p1/domains",
		},
		{
			"the same page written twice is still one destination",
			"Открою /projects/p1/apps. На /projects/p1/apps и находится загрузка.",
			"/projects/p1/apps",
		},
		{
			"a mention without a promise stays a mention",
			"Загрузка живёт на /projects/p1/apps, откройте её когда будете готовы.",
			"",
		},
		{
			"a promise pointing at two different pages names no destination",
			"Открою /projects/p1/apps, а потом /projects/p1/domains.",
			"",
		},
		{
			"a promise with a path the console does not serve",
			"Открою /projects/p1/apps/api/logs, там будут логи.",
			"",
		},
		{
			"a promise with no path at all",
			"Открою нужную страницу, как только вы скажете какую.",
			"",
		},
		{
			"an absolute url is not a console path",
			"Открою https://console.dada-tuda.ru/projects/p1/apps",
			"",
		},
		{
			"an empty answer",
			"",
			"",
		},
	}
	for _, c := range cases {
		got, ok := agentChatNavigationPromised(c.text)
		if c.want == "" {
			if ok {
				t.Errorf("%s: moved the user to %q, but the answer never sent them anywhere", c.name, got)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: promised a move and none happened, so the user waits for a tab that never moves", c.name)
			continue
		}
		if got != c.want {
			t.Errorf("%s: opened %q, want %q", c.name, got, c.want)
		}
	}
}

// TestAgentChatConsolePathsIn_ReadsEveryShapeAnAnswerWritesAPathIn guards the
// extractor on its own: the promise check is worthless if a path written the
// way the assistant actually writes it is invisible here.
func TestAgentChatConsolePathsIn_ReadsEveryShapeAnAnswerWritesAPathIn(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"bare path", "иди на /projects/p1/apps дальше", []string{"/projects/p1/apps"}},
		{"markdown target", "[апы](/projects/p1/apps)", []string{"/projects/p1/apps"}},
		{"backticked", "`/projects/p1/databases`", []string{"/projects/p1/databases"}},
		{"trailing comma", "/projects/p1/apps, затем", []string{"/projects/p1/apps"}},
		{"two distinct pages", "/projects/p1/apps и /projects/p1/domains", []string{"/projects/p1/apps", "/projects/p1/domains"}},
		{"host part of a url is not a path", "https://console.dada-tuda.ru/projects/p1/apps", nil},
		{"unknown route", "/projects/p1/apps/api/logs", nil},
		{"no path", "просто текст", nil},
	}
	for _, c := range cases {
		got := agentChatConsolePathsIn(c.text)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}
