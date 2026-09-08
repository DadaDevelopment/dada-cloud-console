package agentruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

const guidedReplyFormat = "referral_guided_v1"

// GuidedPlaybook is trusted operator-authored configuration. Facts used by
// routes are customer reports only, never proof of registration or payment.
type GuidedPlaybook struct {
	Version       string        `json:"version"`
	AgentName     string        `json:"agent_name"`
	MaxReplyChars int           `json:"max_reply_chars"`
	Qualification []GuidedSlot  `json:"qualification"`
	Routes        []GuidedRoute `json:"routes"`
	AnswerCardIDs []string      `json:"answer_card_ids"`
	Cards         []GuidedCard  `json:"cards"`
}
type GuidedSlot struct {
	Fact   string `json:"fact"`
	CardID string `json:"card_id"`
}
type GuidedRoute struct {
	ID            string   `json:"id"`
	RequiredFacts []string `json:"required_facts"`
	MissingFacts  []string `json:"missing_facts"`
	CardIDs       []string `json:"card_ids"`
}
type GuidedCard struct {
	ID         string   `json:"id"`
	SelectWhen string   `json:"select_when"`
	Paragraphs []string `json:"paragraphs"`
	SourceRefs []string `json:"source_refs"`
}
type GuidedChoice struct {
	ID         string   `json:"id"`
	SelectWhen string   `json:"select_when"`
	Paragraphs []string `json:"paragraphs"`
}
type GuidedContext struct {
	// PreviousReply is the last persisted runtime assistant text, not a claim of
	// Telegram delivery. The kagent session retains a plan, not this rendering.
	PreviousReply  string         `json:"previous_reply,omitempty"`
	Version        string         `json:"version"`
	NextStep       string         `json:"next_step"`
	AdvanceCardIDs []string       `json:"advance_card_ids"`
	AnswerChoices  []GuidedChoice `json:"answer_choices"`
}
type guidedConfig struct {
	playbook *GuidedPlaybook
	chats    map[string]bool
	err      error
}

var syntheticGuidedChat = regexp.MustCompile(`^-900[0-9]+$`)

func strictGuidedJSON(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid guided JSON")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing guided JSON")
	}
	return nil
}
func loadGuidedPlaybook(path string) (*GuidedPlaybook, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("guided playbook unavailable")
	}
	return parseGuidedPlaybook(raw)
}
func parseGuidedPlaybook(raw []byte) (*GuidedPlaybook, error) {
	if len(raw) > 128*1024 {
		return nil, fmt.Errorf("guided playbook too large")
	}
	var p GuidedPlaybook
	if err := strictGuidedJSON(raw, &p); err != nil {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}
func guidedConfigFromEnv() *guidedConfig {
	path := strings.TrimSpace(os.Getenv("AGENT_GUIDED_PLAYBOOK_PATH"))
	inline := strings.TrimSpace(os.Getenv("AGENT_GUIDED_PLAYBOOK_JSON"))
	ids := strings.TrimSpace(os.Getenv("AGENT_GUIDED_CHAT_IDS"))
	if path == "" && inline == "" && ids == "" {
		return nil
	}
	c := &guidedConfig{chats: map[string]bool{}}
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if !syntheticGuidedChat.MatchString(id) {
			c.err = fmt.Errorf("guided chat scope must contain exact synthetic IDs")
			return c
		}
		c.chats[id] = true
	}
	if path != "" && inline != "" {
		c.err = fmt.Errorf("configure exactly one guided playbook source")
	} else if inline != "" {
		c.playbook, c.err = parseGuidedPlaybook([]byte(inline))
	} else {
		c.playbook, c.err = loadGuidedPlaybook(path)
	}
	return c
}
func (r *Runtime) guidedFor(conv Conversation) (*GuidedPlaybook, error) {
	if r.guided == nil {
		return nil, nil
	}
	// A broken candidate must not break other customer chats.
	if !r.guided.chats[conv.ExternalID] {
		return nil, nil
	}
	if r.guided.err != nil {
		return nil, r.guided.err
	}
	if conv.Channel != "telegram" || conv.AgentName != r.guided.playbook.AgentName {
		return nil, nil
	}
	return r.guided.playbook, nil
}
func (p *GuidedPlaybook) card(id string) (GuidedCard, bool) {
	for _, c := range p.Cards {
		if c.ID == id {
			return c, true
		}
	}
	return GuidedCard{}, false
}
func (p *GuidedPlaybook) renderIDs(ids []string) (string, error) {
	if len(ids) < 1 || len(ids) > 3 {
		return "", fmt.Errorf("expected one to three guided cards")
	}
	seen := map[string]bool{}
	paragraphs := []string{}
	for _, id := range ids {
		c, ok := p.card(id)
		if !ok || seen[id] {
			return "", fmt.Errorf("unknown or duplicate guided card")
		}
		seen[id] = true
		paragraphs = append(paragraphs, c.Paragraphs...)
	}
	text := strings.Join(paragraphs, "\n\n")
	if len(paragraphs) > 3 || utf8.RuneCountInString(text) > p.MaxReplyChars || strings.Count(replyURL.ReplaceAllString(text, ""), "?") > 1 {
		return "", fmt.Errorf("guided reply exceeds text or question budget")
	}
	return text, nil
}
func (p *GuidedPlaybook) validate() error {
	if !domainName.MatchString(p.Version) || !domainName.MatchString(p.AgentName) || p.MaxReplyChars < 40 || p.MaxReplyChars > 700 || len(p.Cards) < 1 || len(p.Cards) > 100 || len(p.Qualification) > 16 || len(p.Routes) == 0 || len(p.Routes) > 32 {
		return fmt.Errorf("invalid guided playbook bounds")
	}
	seen := map[string]bool{}
	for _, c := range p.Cards {
		if !domainName.MatchString(c.ID) || seen[c.ID] || len(c.SourceRefs) == 0 || strings.TrimSpace(c.SelectWhen) == "" || len(c.SelectWhen) > 1000 || len(c.Paragraphs) == 0 {
			return fmt.Errorf("invalid guided card metadata")
		}
		seen[c.ID] = true
		for _, para := range c.Paragraphs {
			if strings.TrimSpace(para) != para || para == "" || strings.ContainsAny(para, "\n\r—–") || !utf8.ValidString(para) {
				return fmt.Errorf("invalid guided card text")
			}
		}
	}
	for _, c := range p.Cards {
		if _, err := p.renderIDs([]string{c.ID}); err != nil {
			return err
		}
	}
	slots := map[string]bool{}
	for _, q := range p.Qualification {
		if !domainName.MatchString(q.Fact) || slots[q.Fact] {
			return fmt.Errorf("invalid qualification slot")
		}
		slots[q.Fact] = true
		if _, err := p.renderIDs([]string{q.CardID}); err != nil {
			return err
		}
	}
	routes := map[string]bool{}
	for _, route := range p.Routes {
		if !domainName.MatchString(route.ID) || routes[route.ID] {
			return fmt.Errorf("invalid guided route")
		}
		routes[route.ID] = true
		facts := map[string]bool{}
		for _, key := range append(append([]string{}, route.RequiredFacts...), route.MissingFacts...) {
			if !domainName.MatchString(key) || facts[key] {
				return fmt.Errorf("invalid route facts")
			}
			facts[key] = true
		}
		if _, err := p.renderIDs(route.CardIDs); err != nil {
			return err
		}
	}
	answers := map[string]bool{}
	for _, id := range p.AnswerCardIDs {
		if _, ok := p.card(id); !ok || answers[id] {
			return fmt.Errorf("invalid answer card reference")
		}
		answers[id] = true
	}
	return nil
}
func hasGuidedFact(state RuntimeState, key string) bool {
	return strings.TrimSpace(state.ReportedFacts[key].Value) != ""
}
func (p *GuidedPlaybook) next(state RuntimeState) (string, []string) {
	for _, q := range p.Qualification {
		if !hasGuidedFact(state, q.Fact) {
			return "qualification_" + q.Fact, []string{q.CardID}
		}
	}
	for _, route := range p.Routes {
		matches := true
		for _, key := range route.RequiredFacts {
			matches = matches && hasGuidedFact(state, key)
		}
		for _, key := range route.MissingFacts {
			matches = matches && !hasGuidedFact(state, key)
		}
		if matches {
			return route.ID, route.CardIDs
		}
	}
	return "coverage_gap", nil
}
func (p *GuidedPlaybook) context(state RuntimeState, history []Message) *GuidedContext {
	step, ids := p.next(state)
	out := &GuidedContext{Version: p.Version, NextStep: step, AdvanceCardIDs: ids, AnswerChoices: []GuidedChoice{}}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			out.PreviousReply = history[i].Content
			break
		}
	}
	for _, id := range p.AnswerCardIDs {
		c, _ := p.card(id)
		out.AnswerChoices = append(out.AnswerChoices, GuidedChoice{c.ID, c.SelectWhen, c.Paragraphs})
	}
	return out
}

// guidedReplyObject requires exactly one JSON object and rejects duplicate keys.
// This is protocol normalization, never prose extraction or recursive unwrapping.
func guidedReplyObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("guided reply must be an object")
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid guided reply object")
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("invalid guided reply key")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate guided reply key")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("invalid guided reply value")
		}
		fields[key] = value
	}
	if tok, err = dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, fmt.Errorf("invalid guided reply object")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing guided reply content")
	}
	return fields, nil
}
func normalizeGuidedReply(raw []byte) ([]byte, error) {
	if len(raw) > 16384 {
		return nil, fmt.Errorf("guided reply too large")
	}
	fields, err := guidedReplyObject(raw)
	if err != nil {
		return nil, err
	}
	if choice, ok := fields["choice"]; ok {
		if len(fields) != 1 {
			return nil, fmt.Errorf("choice wrapper accepts no extra fields")
		}
		if _, err := guidedReplyObject(choice); err != nil {
			return nil, err
		}
		return choice, nil
	}
	return raw, nil
}
func (p *GuidedPlaybook) render(raw string, state RuntimeState) (string, error) {
	var plan struct {
		Kind    string   `json:"kind"`
		Version string   `json:"playbook_version"`
		CardIDs []string `json:"card_ids,omitempty"`
	}
	normalized, err := normalizeGuidedReply([]byte(raw))
	if err != nil {
		return "", err
	}
	if err := strictGuidedJSON(normalized, &plan); err != nil {
		return "", err
	}
	if plan.Version != p.Version {
		return "", fmt.Errorf("stale guided playbook version")
	}
	switch plan.Kind {
	case "advance":
		if len(plan.CardIDs) != 0 {
			return "", fmt.Errorf("advance accepts no model card IDs")
		}
		_, ids := p.next(state)
		return p.renderIDs(ids)
	case "answer":
		allowed := map[string]bool{}
		for _, id := range p.AnswerCardIDs {
			allowed[id] = true
		}
		for _, id := range plan.CardIDs {
			if !allowed[id] {
				return "", fmt.Errorf("card is not allowed as a direct answer")
			}
		}
		return p.renderIDs(plan.CardIDs)
	default:
		return "", fmt.Errorf("unknown guided response act")
	}
}
