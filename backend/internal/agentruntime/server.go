package agentruntime

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type Server struct {
	runtime        *Runtime
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

	token := os.Getenv("AGENT_RUNTIME_TOKEN")
	runtime.contextKey = []byte(token)
	return &Server{runtime: runtime, token: token, pauseCRM: NewHTTPPauseCRM(os.Getenv("AGENT_PAUSE_CRM_URL"), os.Getenv("AGENT_PAUSE_CRM_TOKEN"), os.Getenv("AGENT_PAUSE_CRM_STATUS"))}
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
	return r
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
	Content                 string            `json:"content"`
	ChannelMessageID        string            `json:"channel_message_id"`
	ThreadID                string            `json:"thread_id"`
	SourceSentAt            *time.Time        `json:"source_sent_at"`
	ReplyToChannelMessageID string            `json:"reply_to_channel_message_id"`
	Links                   []RuntimeLinkMeta `json:"links"`
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

	resp, err := s.runtime.ProcessMessage(c.Request.Context(), MessageRequest{
		AgentName:  req.AgentName,
		Channel:    req.Channel,
		ExternalID: req.ExternalID,
		Actor: Actor{
			ExternalID: req.Actor.ExternalID,
			Username:   req.Actor.Username,
			Metadata:   req.Actor.Metadata,
		},
		Messages: messages,
	})
	if err != nil {
		log.Error().Err(err).Msg("agentruntime: process message failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, messageResponse{Text: resp.Text, ReplyToChannelMessageID: resp.ReplyToChannelMessageID, Suppressed: resp.Suppressed})
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
