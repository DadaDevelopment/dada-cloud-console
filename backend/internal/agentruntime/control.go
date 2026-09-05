package agentruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// All arguments are fixed runtime descriptions, never submitted values or errors.
func rejectControl(c *gin.Context, code, message, hint string) {
	log.Warn().Str("error_code", code).Msg("agentruntime: control request rejected")
	c.JSON(http.StatusBadRequest, gin.H{"updated": false, "error": message, "error_code": code, "hint": hint})
}

func decodeControl(c *gin.Context, v any) bool {
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		// Decoder errors may contain rejected field names or values. Return
		// only a fixed classification and repair instructions, never err.Error().
		code := "invalid_request_shape"
		var syntax *json.SyntaxError
		var mismatch *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntax), errors.Is(err, io.EOF):
			code = "invalid_json"
		case errors.As(err, &mismatch):
			code = "invalid_field_type"
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			code = "unknown_field"
		}
		hint := "Send one JSON object with only fields defined by the tool schema; preserve each field's declared JSON type."
		if c.FullPath() == "/tools/update-state" {
			hint += " expected_version must be an integer. reported_facts and open_loops must be objects, not JSON strings. Each source_message_id must be the exact incoming_messages[].id UUID of a user message in this conversation, never a channel message ID."
		}
		rejectControl(c, code, "invalid control request", hint)
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		rejectControl(c, "trailing_data", "unexpected trailing data", "Send exactly one JSON object without trailing content.")
		return false
	}
	return true
}
func (s *Server) controlConversation(c *gin.Context, token string) (Conversation, bool) {
	claims, err := verifyContextToken([]byte(s.token), token, time.Now())
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid runtime context"})
		return Conversation{}, false
	}
	conv, err := s.runtime.store.GetConversation(c.Request.Context(), claims.ConversationID)
	if err != nil || conv.AgentName != claims.AgentName {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid runtime context"})
		return Conversation{}, false
	}
	return conv, true
}
func (s *Server) requireActive(c *gin.Context, conv Conversation) bool {
	state, err := s.runtime.states.GetState(c.Request.Context(), conv.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "state unavailable"})
		return false
	}
	if !state.AgentEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "agent disabled for conversation"})
		return false
	}
	return true
}
func (s *Server) handleLoadSkill(c *gin.Context) {
	var req struct {
		ContextToken string `json:"context_token"`
		Skill        string `json:"skill"`
	}
	if !decodeControl(c, &req) {
		return
	}
	conv, ok := s.controlConversation(c, req.ContextToken)
	if !ok || !s.requireActive(c, conv) {
		return
	}
	content, err := s.runtime.domains.GetDomain(c.Request.Context(), conv.AgentName, req.Skill)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "skill unavailable"})
		return
	}
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	state, err := s.runtime.states.ActivateSkill(c.Request.Context(), conv.ID, req.Skill, content, digest)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "skill activation failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"skill": req.Skill, "digest": digest, "content": content, "state_version": state.Version})
}
func (s *Server) handleUpdateState(c *gin.Context) {
	var req struct {
		ContextToken    string     `json:"context_token"`
		ExpectedVersion int64      `json:"expected_version"`
		Patch           StatePatch `json:"patch"`
	}
	if !decodeControl(c, &req) {
		return
	}
	conv, ok := s.controlConversation(c, req.ContextToken)
	if !ok || !s.requireActive(c, conv) {
		return
	}
	state, err := s.runtime.states.ApplyState(c.Request.Context(), conv.ID, req.ExpectedVersion, req.Patch)
	if err != nil {
		if errors.Is(err, ErrInvalidFactQuote) {
			// Keep this repairable validation result visible through GTR HTTP
			// tools, which discard non-2xx response bodies. Do not expose source
			// content or raw storage errors.
			c.JSON(http.StatusOK, gin.H{"updated": false, "error": ErrInvalidFactQuote.Error()})
			return
		}
		code := http.StatusInternalServerError
		if errors.Is(err, ErrStateConflict) {
			current, readErr := s.runtime.states.GetState(c.Request.Context(), conv.ID)
			if readErr != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "state unavailable"})
				return
			}
			// GTR HTTP tools discard bodies on non-2xx. Return an explicit
			// rejected update with current state so the next call can use CAS.
			c.JSON(http.StatusOK, gin.H{"updated": false, "error": "state version conflict", "state": current})
			return
		}
		if errors.Is(err, ErrInvalidStateEvidence) {
			rejectControl(c, "invalid_source_message", "invalid state evidence",
				"Use the exact incoming_messages[].id UUID of a user message in this conversation as source_message_id. Do not use channel_message_id, an assistant message, or an invented UUID.")
			return
		}
		if errors.Is(err, ErrInvalidStatePatch) {
			rejectControl(c, "invalid_patch", "invalid state patch",
				"Use at most 64 reported_facts and 32 open_loops, including existing entries. Keys must be nonempty and at most 80 bytes. Fact values and questions must be nonempty and at most 1024 bytes, without NUL characters. Each loop status must be open or resolved.")
			return
		}
		c.JSON(code, gin.H{"error": "state update rejected", "refresh_context": code == http.StatusConflict})
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": true, "state": state})
}
func (s *Server) handleStopAgent(c *gin.Context) {
	var req struct {
		ContextToken string `json:"context_token"`
		Reason       string `json:"reason"`
	}
	if !decodeControl(c, &req) {
		return
	}
	conv, ok := s.controlConversation(c, req.ContextToken)
	if !ok {
		return
	}
	state, err := s.runtime.states.PauseAgent(c.Request.Context(), conv.ID, req.Reason)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pause rejected"})
		return
	}
	// Persist pause before contacting CRM; retries never re-enable replies.
	state, err = s.syncPausedCRM(c.Request.Context(), conv)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"agent_enabled": false, "crm_status_sync": "pending"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent_enabled": false, "crm_status_sync": state.CRMStatusSync, "state_version": state.Version})
}
