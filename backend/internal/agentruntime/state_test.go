package agentruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestValidateStatePatch(t *testing.T) {
	source := uuid.New()
	require.NoError(t, validateStatePatch(StatePatch{ReportedFacts: map[string]ReportedFact{"deposit": {Value: "reported", SourceMessageID: source}}, OpenLoops: map[string]OpenLoop{"access": {Question: "when?", SourceMessageID: source, Status: "open"}}}))
	for _, tc := range []struct {
		name  string
		patch StatePatch
		want  error
	}{
		{"missing evidence", StatePatch{ReportedFacts: map[string]ReportedFact{"a": {Value: "yes"}}}, ErrInvalidStateEvidence},
		{"missing loop evidence", StatePatch{OpenLoops: map[string]OpenLoop{"a": {Question: "when", Status: "open"}}}, ErrInvalidStateEvidence},
		{"empty key", StatePatch{ReportedFacts: map[string]ReportedFact{" ": {Value: "yes", SourceMessageID: source}}}, ErrInvalidStatePatch},
		{"empty quote", StatePatch{ReportedFacts: map[string]ReportedFact{"a": {Value: " ", SourceMessageID: source}}}, ErrInvalidFactQuote},
		{"large value", StatePatch{ReportedFacts: map[string]ReportedFact{"a": {Value: strings.Repeat("x", 1025), SourceMessageID: source}}}, ErrInvalidStatePatch},
		{"NUL value", StatePatch{ReportedFacts: map[string]ReportedFact{"a": {Value: "x\x00y", SourceMessageID: source}}}, ErrInvalidStatePatch},
		{"invalid status", StatePatch{OpenLoops: map[string]OpenLoop{"a": {Question: "when", Status: "verified", SourceMessageID: source}}}, ErrInvalidStatePatch},
	} {
		t.Run(tc.name, func(t *testing.T) { require.ErrorIs(t, validateStatePatch(tc.patch), tc.want) })
	}
}

func TestValidateFactQuote(t *testing.T) {
	for _, tc := range []struct {
		name, source, value string
		valid               bool
	}{
		{"exact intent", "Хорошо, напишу в поддержку завтра.", "напишу в поддержку", true},
		{"whole message", "открывал сам", "открывал сам", true},
		{"intent is not completion", "Напишу в поддержку", "обратился в поддержку", false},
		{"invented negation", "открывал сам", "без реферальной привязки", false},
		{"no case normalization", "Напишу", "напишу", false},
		{"no whitespace normalization", "уже  пополнил", "уже пополнил", false},
		{"empty quote", "hello", "", false},
		{"blank quote", "hello world", " ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFactQuote(tc.value, tc.source)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrInvalidFactQuote)
				require.Contains(t, err.Error(), "verbatim quote")
			}
		})
	}
}
func TestMergeStatePatchPreservesUnrelatedAndCapsAccumulation(t *testing.T) {
	state := RuntimeState{ReportedFacts: map[string]ReportedFact{"old": {Value: "old"}}, OpenLoops: map[string]OpenLoop{"unrelated": {Question: "keep", Status: "open"}}, AgentEnabled: false, CRMStatusSync: "pending"}
	require.NoError(t, mergeStatePatch(&state, StatePatch{ReportedFacts: map[string]ReportedFact{"new": {Value: "new"}}}))
	require.Equal(t, "old", state.ReportedFacts["old"].Value)
	require.Equal(t, "keep", state.OpenLoops["unrelated"].Question)
	require.False(t, state.AgentEnabled)
	require.Equal(t, "pending", state.CRMStatusSync)
	for i := len(state.ReportedFacts); i < MaxReportedFacts; i++ {
		state.ReportedFacts[fmt.Sprint(i)] = ReportedFact{Value: "x"}
	}
	require.ErrorIs(t, mergeStatePatch(&state, StatePatch{ReportedFacts: map[string]ReportedFact{"overflow": {Value: "x"}}}), ErrInvalidStatePatch)
	require.Len(t, state.ReportedFacts, MaxReportedFacts)
	_, exists := state.ReportedFacts["overflow"]
	require.False(t, exists)
}
func TestValidateActiveSkill(t *testing.T) {
	content := "Use the documented procedure."
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	require.NoError(t, validateActiveSkill("registration", content, digest))
	require.ErrorIs(t, validateActiveSkill("registration", content+" changed", digest), ErrInvalidStatePatch)
	require.ErrorIs(t, validateActiveSkill("", content, digest), ErrInvalidStatePatch)
	large := strings.Repeat("x", MaxSkillContentBytes+1)
	sum = sha256.Sum256([]byte(large))
	require.ErrorIs(t, validateActiveSkill("large", large, hex.EncodeToString(sum[:])), ErrInvalidStatePatch)
}
