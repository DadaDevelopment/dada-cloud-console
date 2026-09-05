package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Refresh only procedures already selected for this conversation. A missing or
// invalid procedure blocks the run instead of falling back to obsolete content.
func (r *Runtime) refreshActiveSkills(ctx context.Context, conv Conversation, state RuntimeState) (RuntimeState, error) {
	names := make([]string, 0, len(state.ActiveSkills))
	for name := range state.ActiveSkills {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if r.domains == nil {
			return RuntimeState{}, fmt.Errorf("active skill provider unavailable")
		}
		content, err := r.domains.GetDomain(ctx, conv.AgentName, name)
		if err != nil {
			return RuntimeState{}, fmt.Errorf("refresh active skill %s: %w", name, err)
		}
		sum := sha256.Sum256([]byte(content))
		digest := hex.EncodeToString(sum[:])
		if state.ActiveSkills[name] == (ActiveSkill{Content: content, Digest: digest}) {
			continue
		}
		state, err = r.states.ActivateSkill(ctx, conv.ID, name, content, digest)
		if err != nil {
			return RuntimeState{}, fmt.Errorf("refresh active skill %s: %w", name, err)
		}
	}
	return state, nil
}
