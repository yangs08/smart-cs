package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"helpdesk-agent/knowledge"
	"helpdesk-agent/memory"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
)

// Server wraps the Gin HTTP server with the agent runner.
type Server struct {
	runner     *adk.Runner
	shortMem   *memory.ShortTermMemory
	longMem    *memory.LongTermMemory
	kb         *knowledge.HybridStore
	totalReqs  atomic.Int64
	activeSess atomic.Int64
	errCount   atomic.Int64
}

// ChatRequest is the API request body.
type ChatRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Message   string `json:"message" binding:"required"`
}

// NewServer creates a new API server with the given runner.
func NewServer(runner *adk.Runner, shortMem *memory.ShortTermMemory, longMem *memory.LongTermMemory, kb *knowledge.HybridStore) *Server {
	return &Server{
		runner:   runner,
		shortMem: shortMem,
		longMem:  longMem,
		kb:       kb,
	}
}

// RegisterRoutes registers the Gin routes.
func (s *Server) RegisterRoutes(r *gin.Engine) {
	r.Use(s.requestLogger())
	r.StaticFile("/", "./frontend/chat.html")
	r.POST("/api/chat", s.handleChat)
	r.GET("/health", s.healthCheck)
	r.GET("/metrics", s.metricsHandler)
}

func (s *Server) healthCheck(c *gin.Context) {
	result := gin.H{"status": "ok"}

	if s.kb != nil {
		if err := s.kb.PingQdrant(c.Request.Context()); err != nil {
			result["qdrant"] = err.Error()
		} else {
			result["qdrant"] = "ok"
		}
		if err := s.kb.PingEmbedder(c.Request.Context()); err != nil {
			result["ollama"] = err.Error()
		} else {
			result["ollama"] = "ok"
		}
	}

	c.JSON(http.StatusOK, result)
}

// handleChat processes a chat message and streams the response via SSE.
func (s *Server) handleChat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.shortMem.Append(req.SessionID, "user", req.Message)
	s.activeSess.Add(1)
	defer s.activeSess.Add(-1)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	c.Stream(func(w io.Writer) bool {
		memEntries := s.longMem.Search(req.SessionID, req.Message, 3)

		sessionValues := map[string]any{
			"session_id": req.SessionID,
		}
		if len(memEntries) > 0 {
			var contexts []string
			for _, e := range memEntries {
				contexts = append(contexts, e.Content)
			}
			sessionValues["history_context"] = contexts
		}

		iter := s.runner.Query(c.Request.Context(), req.Message,
			adk.WithSessionValues(sessionValues),
		)

		var fullResponse string
		for {
			event, ok := iter.Next()
			if !ok {
				break
			}
			if event.Err != nil {
				s.errCount.Add(1)
				slog.Error("agent error", "error", event.Err, "session_id", req.SessionID)
				fmt.Fprintf(w, "event: error\ndata: internal error\n\n")
				return false
			}
			if event.Output != nil && event.Output.MessageOutput != nil {
				mv := event.Output.MessageOutput
				// Only stream final assistant responses (no tool calls).
				if mv.Role == schema.Assistant {
					msg, err := mv.GetMessage()
					if err == nil && msg != nil && msg.Content != "" && len(msg.ToolCalls) == 0 {
						// Skip intermediate text containing raw function call JSON.
						if strings.Contains(msg.Content, `"name": "`) &&
							strings.Contains(msg.Content, `"arguments"`) {
							continue
						}
						fullResponse += msg.Content
						fmt.Fprintf(w, "data: %s\n\n", msg.Content)
					}
				}
			}
		}

		if fullResponse != "" {
			s.longMem.Append(req.SessionID, "user: "+req.Message, nil)
		}

		fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
		return false
	})
}
