package api

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		s.totalReqs.Add(1)

		path := c.Request.URL.Path
		method := c.Request.Method
		clientIP := c.ClientIP()

		c.Next()

		slog.Info("request",
			"method", method,
			"path", path,
			"status", c.Writer.Status(),
			"duration", time.Since(start),
			"ip", clientIP,
		)
	}
}
