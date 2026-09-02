package agentruntime

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type Server struct {
	runtime *Runtime
}

func NewServer(pool *pgxpool.Pool, gitopsBasePath string) *Server {
	store := NewPGStore(pool)
	hooks := NewHookExecutor(pool)
	a2a := NewA2AClient()
	domains := NewFileDomainProvider(gitopsBasePath)

	runtime := NewRuntime(store, hooks, a2a, domains)

	return &Server{runtime: runtime}
}

func (s *Server) Handler() http.Handler {
	r := gin.Default()
	r.POST("/message", s.handleMessage)
	r.GET("/health", s.handleHealth)
	return r
}

type messageRequest struct {
	AgentName               string       `json:"agent_name"`
	Channel                 string       `json:"channel"`
	ExternalID              string       `json:"external_id"`
	Actor                   actorRequest `json:"actor"`
	Content                 string       `json:"content"`
	ChannelMessageID        string       `json:"channel_message_id"`
	ThreadID                string       `json:"thread_id"`
	SourceSentAt            *time.Time   `json:"source_sent_at"`
	ReplyToChannelMessageID string       `json:"reply_to_channel_message_id"`
}

type actorRequest struct {
	ExternalID string         `json:"external_id"`
	Username   string         `json:"username"`
	Metadata   map[string]any `json:"metadata"`
}

type messageResponse struct {
	Text string `json:"text"`
}

func (s *Server) handleMessage(c *gin.Context) {
	var req messageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
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
		Content:                 req.Content,
		ChannelMessageID:        req.ChannelMessageID,
		ThreadID:                req.ThreadID,
		SourceSentAt:            req.SourceSentAt,
		ReplyToChannelMessageID: req.ReplyToChannelMessageID,
	})
	if err != nil {
		log.Error().Err(err).Msg("agentruntime: process message failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, messageResponse{Text: resp.Text})
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
