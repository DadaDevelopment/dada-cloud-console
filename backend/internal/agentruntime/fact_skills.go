package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
)

// ParseFactSkills reads AGENT_FACT_SKILLS ("amount=price,account=registration")
// into a fact key -> procedure name map. The catalog in the prompt already
// calls these procedures mandatory, yet the model loaded price in 4 of 36
// conversations that recorded an amount (2026-09-14 18:00Z onward), so the
// rules that govern the reply after a fact were usually absent. Activation by
// recorded fact makes them deterministic without a Russian-text rules engine:
// the model records facts reliably, so the fact is the trigger.
func ParseFactSkills(raw string) map[string]string {
	skills := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		fact, skill, ok := strings.Cut(pair, "=")
		fact, skill = strings.TrimSpace(fact), strings.TrimSpace(skill)
		if !ok || fact == "" || skill == "" {
			continue
		}
		skills[fact] = skill
	}
	return skills
}

// ensureFactSkills activates every procedure a recorded fact makes mandatory.
// A procedure missing for this agent is logged and skipped: the mapping is
// shared configuration, and a gap there must not silence a conversation.
func (r *Runtime) ensureFactSkills(ctx context.Context, conv Conversation, state RuntimeState) (RuntimeState, error) {
	if len(r.factSkills) == 0 || r.domains == nil {
		return state, nil
	}
	facts := make([]string, 0, len(state.ReportedFacts))
	for fact := range state.ReportedFacts {
		if _, mapped := r.factSkills[fact]; mapped {
			facts = append(facts, fact)
		}
	}
	sort.Strings(facts)
	for _, fact := range facts {
		name := r.factSkills[fact]
		if _, active := state.ActiveSkills[name]; active {
			continue
		}
		content, err := r.domains.GetDomain(ctx, conv.AgentName, name)
		if err != nil {
			log.Warn().Str("conversation", conv.ID.String()).Str("agent", conv.AgentName).Str("fact", fact).Str("skill", name).Err(err).
				Msg("agentruntime: procedure mandatory for a recorded fact is unavailable")
			continue
		}
		sum := sha256.Sum256([]byte(content))
		state, err = r.states.ActivateSkill(ctx, conv.ID, name, content, hex.EncodeToString(sum[:]))
		if err != nil {
			return RuntimeState{}, fmt.Errorf("activate skill %s for fact %s: %w", name, fact, err)
		}
	}
	return state, nil
}
