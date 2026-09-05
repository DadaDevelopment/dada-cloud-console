package agentruntime

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type Server struct {
	runtime        *Runtime
	pool           *pgxpool.Pool
	a2a            A2AClient
	scheduler      *IdleScheduler
	token          string
	pauseCRM       PauseCRM
	pauseSyncLocks [64]sync.Mutex
}

func NewServer(pool *pgxpool.Pool, gitopsBasePath string) *Server {
	store := NewPGStore(pool)
	hooks := NewHookExecutor(pool)
	a2a := NewA2AClient()
	domains := NewFileDomainProvider(gitopsBasePath)

	runtime := NewRuntime(store, hooks, a2a, domains)
	runtime.contacts = contactSyncFromEnv(store.(*pgStore))
	runtime.courtesyAgents = map[string]bool{}
	for _, name := range strings.Split(os.Getenv("AGENT_COURTESY_SUPPRESSION_AGENTS"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			runtime.courtesyAgents[name] = true
		}
	}

	token := os.Getenv("AGENT_RUNTIME_TOKEN")
	runtime.contextKey = []byte(token)
	return &Server{runtime: runtime, pool: pool, a2a: a2a, token: token, pauseCRM: NewHTTPPauseCRM(os.Getenv("AGENT_PAUSE_CRM_URL"), os.Getenv("AGENT_PAUSE_CRM_TOKEN"), os.Getenv("AGENT_PAUSE_CRM_STATUS"))}
}

// StartIdleScheduler launches the proactive-invocation loop. idleTickSeconds
// <= 0 disables it. outboundURL empty = persist-only mode (follow-ups are
// saved but not delivered; each one logs that).
func (s *Server) StartIdleScheduler(ctx context.Context, idleTickSeconds int, outboundURL string) {
	if idleTickSeconds <= 0 {
		log.Info().Msg("agentruntime: idle scheduler disabled")
		return
	}
	var outbound ChannelOutbound
	if outboundURL != "" {
		outbound = NewHTTPChannelOutbound(outboundURL)
	}
	s.scheduler = NewIdleScheduler(s.pool, s.runtime, s.a2a, outbound, time.Duration(idleTickSeconds)*time.Second)
	go s.scheduler.Run(ctx)
	log.Info().Int("tick_seconds", idleTickSeconds).Bool("outbound", outboundURL != "").Msg("agentruntime: idle scheduler started")
}

func (s *Server) Handler() http.Handler {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/health", s.handleHealth)
	protected := r.Group("/")
	protected.Use(func(c *gin.Context) {
		supplied := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if len(s.token) < 32 {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "runtime authentication not configured"})
			return
		}
		if !strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(supplied), []byte(s.token)) != 1 {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
		c.Next()
	})
	protected.POST("/message", s.handleMessage)
	protected.POST("/tools/load-skill", s.handleLoadSkill)
	protected.POST("/tools/update-state", s.handleUpdateState)
	protected.POST("/tools/stop-agent", s.handleStopAgent)
	protected.POST("/hooks", s.handleCreateHook)
	protected.GET("/hooks", s.handleListHooks)
	protected.DELETE("/hooks/:id", s.handleDeleteHook)

	return r
}

// hookRequest is the dogfood-facing hook management payload: create a
// lifecycle hook without psql.
type hookRequest struct {
	AgentName     string         `json:"agent_name"`
	Name          string         `json:"name"`
	TriggerEvent  string         `json:"trigger_event"`
	TriggerConfig map[string]any `json:"trigger_config"`
	ActionType    string         `json:"action_type"`
	ActionConfig  map[string]any `json:"action_config"`
}

func (s *Server) handleCreateHook(c *gin.Context) {
	var req hookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.AgentName == "" || req.Name == "" || req.TriggerEvent == "" || req.ActionType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "agent_name, name, trigger_event and action_type are required"})
		return
	}
	triggerJSON, _ := json.Marshal(req.TriggerConfig)
	actionJSON, _ := json.Marshal(req.ActionConfig)

	var id string
	err := s.pool.QueryRow(c.Request.Context(), `
		INSERT INTO lifecycle_hooks (agent_name, name, trigger_event, trigger_config, action_type, action_config)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (agent_name, name) DO UPDATE
		   SET trigger_event  = EXCLUDED.trigger_event,
		       trigger_config = EXCLUDED.trigger_config,
		       action_type    = EXCLUDED.action_type,
		       action_config  = EXCLUDED.action_config,
		       enabled        = true
		RETURNING id::text
	`, req.AgentName, req.Name, req.TriggerEvent, triggerJSON, req.ActionType, actionJSON).Scan(&id)
	if err != nil {
		log.Error().Err(err).Msg("agentruntime: create hook failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id})
}

func (s *Server) handleListHooks(c *gin.Context) {
	agentName := c.Query("agent_name")
	if agentName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "agent_name query param required"})
		return
	}
	rows, err := s.pool.Query(c.Request.Context(), `
		SELECT id::text, name, trigger_event, trigger_config, action_type, action_config, enabled
		FROM lifecycle_hooks WHERE agent_name = $1 ORDER BY name
	`, agentName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list failed"})
		return
	}
	defer rows.Close()

	hooks := []gin.H{}
	for rows.Next() {
		var id, name, event, actionType string
		var triggerJSON, actionJSON []byte
		var enabled bool
		if err := rows.Scan(&id, &name, &event, &triggerJSON, &actionType, &actionJSON, &enabled); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "scan failed"})
			return
		}
		var triggerCfg, actionCfg map[string]any
		_ = json.Unmarshal(triggerJSON, &triggerCfg)
		_ = json.Unmarshal(actionJSON, &actionCfg)
		hooks = append(hooks, gin.H{
			"id": id, "name": name, "trigger_event": event,
			"trigger_config": triggerCfg, "action_type": actionType,
			"action_config": actionCfg, "enabled": enabled,
		})
	}
	c.JSON(http.StatusOK, gin.H{"hooks": hooks})
}

func (s *Server) handleDeleteHook(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such hook"})
		return
	}
	tag, err := s.pool.Exec(c.Request.Context(), `DELETE FROM lifecycle_hooks WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such hook"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

type messageRequest struct {
	AgentName               string               `json:"agent_name"`
	Channel                 string               `json:"channel"`
	ExternalID              string               `json:"external_id"`
	Actor                   actorRequest         `json:"actor"`
	Content                 string               `json:"content"`
	ChannelMessageID        string               `json:"channel_message_id"`
	ThreadID                string               `json:"thread_id"`
	SourceSentAt            *time.Time           `json:"source_sent_at"`
	ReplyToChannelMessageID string               `json:"reply_to_channel_message_id"`
	Messages                []inboundMessageJSON `json:"messages"`
}

type actorRequest struct {
	ExternalID string         `json:"external_id"`
	Username   string         `json:"username"`
	Metadata   map[string]any `json:"metadata"`
}

type inboundMessageJSON struct {
	Content                 string             `json:"content"`
	ChannelMessageID        string             `json:"channel_message_id"`
	ThreadID                string             `json:"thread_id"`
	SourceSentAt            *time.Time         `json:"source_sent_at"`
	ReplyToChannelMessageID string             `json:"reply_to_channel_message_id"`
	Links                   []RuntimeLinkMeta  `json:"links"`
	Attachment              *RuntimeAttachment `json:"attachment"`
}

type messageResponse struct {
	Suppressed              bool   `json:"suppressed,omitempty"`
	Text                    string `json:"text"`
	ReplyToChannelMessageID string `json:"reply_to_channel_message_id,omitempty"`
}

func (s *Server) handleMessage(c *gin.Context) {
	var req messageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	messages := make([]InboundMessage, 0, len(req.Messages))
	if len(req.Messages) == 0 && req.Content != "" {
		messages = append(messages, InboundMessage{
			Content:                 req.Content,
			ChannelMessageID:        req.ChannelMessageID,
			ThreadID:                req.ThreadID,
			SourceSentAt:            req.SourceSentAt,
			ReplyToChannelMessageID: req.ReplyToChannelMessageID,
		})
	}
	for _, m := range req.Messages {
		messages = append(messages, InboundMessage(m))
	}

	streaming := c.GetHeader("Accept") == "application/x-ndjson"
	emit := func(event any) {
		c.Header("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(c.Writer).Encode(event)
		c.Writer.Flush()
	}
	var onProcessing func()
	if streaming {
		onProcessing = func() { emit(gin.H{"event": "processing"}) }
	}
	resp, err := s.runtime.ProcessMessage(c.Request.Context(), MessageRequest{
		AgentName:  req.AgentName,
		Channel:    req.Channel,
		ExternalID: req.ExternalID,
		Actor: Actor{
			ExternalID: req.Actor.ExternalID,
			Username:   req.Actor.Username,
			Metadata:   req.Actor.Metadata,
		},
		Messages:     messages,
		OnProcessing: onProcessing,
	})
	if err != nil {
		log.Error().Err(err).Msg("agentruntime: process message failed")
		if streaming {
			emit(gin.H{"event": "error", "error": "message processing failed"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "message processing failed"})
		}
		return
	}
	if streaming {
		emit(gin.H{"event": "result", "result": messageResponse{Text: resp.Text, ReplyToChannelMessageID: resp.ReplyToChannelMessageID, Suppressed: resp.Suppressed}})
		return
	}
	c.JSON(http.StatusOK, messageResponse{Text: resp.Text, ReplyToChannelMessageID: resp.ReplyToChannelMessageID, Suppressed: resp.Suppressed})
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
