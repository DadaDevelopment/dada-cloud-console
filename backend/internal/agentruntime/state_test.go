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
		{"large value", StatePatch{ReportedFacts: map[string]ReportedFact{"a": {Value: strings.Repeat("x", 1025), SourceMessageID: source}}}, ErrInvalidStatePatch},
		{"NUL value", StatePatch{ReportedFacts: map[string]ReportedFact{"a": {Value: "x\x00y", SourceMessageID: source}}}, ErrInvalidStatePatch},
		{"invalid status", StatePatch{OpenLoops: map[string]OpenLoop{"a": {Question: "when", Status: "verified", SourceMessageID: source}}}, ErrInvalidStatePatch},
	} {
		t.Run(tc.name, func(t *testing.T) { require.ErrorIs(t, validateStatePatch(tc.patch), tc.want) })
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
