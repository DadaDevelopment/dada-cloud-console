package tggateway

import "testing"

func TestSaysAlias(t *testing.T) {
	aliases := []string{"вайбкодер", "vibe", "coder"}
	cases := []struct {
		text string
		want bool
	}{
		{"вайбкодер а что думаешь", true},
		{"Вайбкодер, глянь бенчи", true},
		{"эй vibe ответь", true},
		{"этот вайбкодерлайн Мне нравится", false},
		{"подвайбкодер мне", false},
		{"вайбкод", false},
		{"просто болтовня без обращений", false},
	}
	for _, tc := range cases {
		if got := saysAlias(tc.text, aliases); got != tc.want {
			t.Errorf("saysAlias(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
	if saysAlias("что-нибудь", nil) {
		t.Error("empty aliases must never match")
	}
}

func TestNameAliasesEngage(t *testing.T) {
	t.Setenv("TG_GROUP_NAME_ALIASES_TG_VIBECODER", "вайбкодер, vibe")
	p := NewGroupPolicyForAgent("somebot", "tg-vibecoder")
	d := p.Decide(groupMsg("вайбкодер ты живой?"))
	if !d.Engage || d.Reason != ReasonMention {
		t.Fatalf("alias call must engage as mention, got %+v", d)
	}
	d = p.Decide(groupMsg("мой вайбкодершот"))
	if d.Engage {
		t.Fatalf("substring of an alias in a short message must not engage, got %+v", d)
	}
}

func TestAliasKeyIsPerAgent(t *testing.T) {
	t.Setenv("TG_GROUP_NAME_ALIASES_TG_VIBECODER", "вайбкодер")
	p := NewGroupPolicyForAgent("otherbot", "tg-exchange-support")
	if len(p.NameAliases) != 0 {
		t.Fatalf("agent-specific key must not leak to another agent, got %v", p.NameAliases)
	}
}
