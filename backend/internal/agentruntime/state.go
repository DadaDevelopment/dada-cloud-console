package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrStateConflict        = errors.New("agentruntime: stale state version")
	ErrInvalidStatePatch    = errors.New("agentruntime: invalid state patch")
	ErrInvalidStateEvidence = errors.New("agentruntime: source must be a user message in this conversation")
	ErrInvalidFactQuote     = errors.New("agentruntime: reported fact value must be a nonempty verbatim quote from its referenced user message")
)

const (
	MaxReportedFacts     = 64
	MaxOpenLoops         = 32
	MaxActiveSkills      = 8
	MaxSkillContentBytes = 8192
)

// ReportedFact quotes what a customer said, never a paraphrase or independent verification.
type ReportedFact struct {
	Value           string    `json:"value"`
	SourceMessageID uuid.UUID `json:"source_message_id"`
}
type OpenLoop struct {
	Question        string    `json:"question"`
	SourceMessageID uuid.UUID `json:"source_message_id"`
	Status          string    `json:"status"`
}
type ActiveSkill struct {
	Content string `json:"content"`
	Digest  string `json:"digest"`
}
type RuntimeState struct {
	Version       int64                   `json:"version"`
	AgentEnabled  bool                    `json:"agent_enabled"`
	ReportedFacts map[string]ReportedFact `json:"reported_facts"`
	OpenLoops     map[string]OpenLoop     `json:"open_loops"`
	ActiveSkills  map[string]ActiveSkill  `json:"active_skills"`
	PauseReason   string                  `json:"pause_reason,omitempty"`
	CRMStatusSync string                  `json:"crm_status_sync,omitempty"`
}

// StatePatch only updates named entries. Agent enablement, loaded skill content,
// and CRM outcomes are runtime-owned and cannot be patched by the model.
type StatePatch struct {
	ReportedFacts map[string]ReportedFact `json:"reported_facts,omitempty"`
	OpenLoops     map[string]OpenLoop     `json:"open_loops,omitempty"`
}
type StateStore interface {
	GetState(context.Context, uuid.UUID) (RuntimeState, error)
	ApplyState(context.Context, uuid.UUID, int64, StatePatch) (RuntimeState, error)
	ActivateSkill(context.Context, uuid.UUID, string, string, string) (RuntimeState, error)
	PauseAgent(context.Context, uuid.UUID, string) (RuntimeState, error)
	MarkPauseCRMSync(context.Context, uuid.UUID, string) (RuntimeState, error)
}

func boundedStateText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validateFactQuote(value, sourceContent string) error {
	if strings.TrimSpace(value) == "" || !strings.Contains(sourceContent, value) {
		return ErrInvalidFactQuote
	}
	return nil
}

func validateStatePatch(p StatePatch) error {
	if len(p.ReportedFacts) > MaxReportedFacts || len(p.OpenLoops) > MaxOpenLoops {
		return fmt.Errorf("%w: too many entries", ErrInvalidStatePatch)
	}
	for key, fact := range p.ReportedFacts {
		if strings.TrimSpace(fact.Value) == "" {
			return ErrInvalidFactQuote
		}
		if !boundedStateText(key, 80) || !boundedStateText(fact.Value, 1024) {
			return fmt.Errorf("%w: invalid fact", ErrInvalidStatePatch)
		}
		if fact.SourceMessageID == uuid.Nil {
			return ErrInvalidStateEvidence
		}
	}
	for key, loop := range p.OpenLoops {
		if !boundedStateText(key, 80) || !boundedStateText(loop.Question, 1024) || (loop.Status != "open" && loop.Status != "resolved") {
			return fmt.Errorf("%w: invalid open loop", ErrInvalidStatePatch)
		}
		if loop.SourceMessageID == uuid.Nil {
			return ErrInvalidStateEvidence
		}
	}
	return nil
}

func mergeStatePatch(state *RuntimeState, patch StatePatch) error {
	facts := make(map[string]ReportedFact, len(state.ReportedFacts)+len(patch.ReportedFacts))
	loops := make(map[string]OpenLoop, len(state.OpenLoops)+len(patch.OpenLoops))
	for key, val := range state.ReportedFacts {
		facts[key] = val
	}
	for key, val := range patch.ReportedFacts {
		facts[key] = val
	}
	for key, val := range state.OpenLoops {
		loops[key] = val
	}
	for key, val := range patch.OpenLoops {
		loops[key] = val
	}
	if len(facts) > MaxReportedFacts || len(loops) > MaxOpenLoops {
		return fmt.Errorf("%w: accumulated state exceeds limit", ErrInvalidStatePatch)
	}
	state.ReportedFacts, state.OpenLoops = facts, loops
	return nil
}

func validateActiveSkill(name, content, digest string) error {
	sum := sha256.Sum256([]byte(content))
	if !boundedStateText(name, 80) || !boundedStateText(content, MaxSkillContentBytes) || digest != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("%w: invalid skill name, content, or SHA256 digest", ErrInvalidStatePatch)
	}
	return nil
}
