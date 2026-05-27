package api

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"helpdesk-agent/memory"

	"github.com/cloudwego/eino/adk"
	"github.com/gin-gonic/gin"
)

// Server wraps the Gin HTTP server with the agent runner.
type Server struct {
	runner   *adk.Runner
	shortMem *memory.ShortTermMemory
	longMem  *memory.LongTermMemory
}

// ChatRequest is the API request body.
type ChatRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Message   string `json:"message" binding:"required"`
}

// NewServer creates a new API server with the given runner.
func NewServer(runner *adk.Runner, shortMem *memory.ShortTermMemory, longMem *memory.LongTermMemory) *Server {
	return &Server{
		runner:   runner,
		shortMem: shortMem,
		longMem:  longMem,
	}
}

// RegisterRoutes registers the Gin routes.
func (s *Server) RegisterRoutes(r *gin.Engine) {
	r.POST("/api/chat", s.handleChat)
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}

// handleChat processes a chat message and streams the response via SSE.
func (s *Server) handleChat(c *gin.Context) {
	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.shortMem.Append(req.SessionID, "user", req.Message)

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
				log.Printf("agent error: %v", event.Err)
				fmt.Fprintf(w, "event: error\ndata: %v\n\n", event.Err)
				return false
			}
			if event.Output != nil && event.Output.MessageOutput != nil {
				msg, err := event.Output.MessageOutput.GetMessage()
				if err == nil && msg != nil {
					fullResponse += msg.Content
					fmt.Fprintf(w, "data: %s\n\n", msg.Content)
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
