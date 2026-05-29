package api

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) metricsHandler(c *gin.Context) {
	c.String(http.StatusOK, fmt.Sprintf(`# HELP helpdesk_requests_total Total requests processed
# TYPE helpdesk_requests_total counter
helpdesk_requests_total %d
# HELP helpdesk_active_sessions Currently active sessions
# TYPE helpdesk_active_sessions gauge
helpdesk_active_sessions %d
# HELP helpdesk_errors_total Total errors
# TYPE helpdesk_errors_total counter
helpdesk_errors_total %d
`, s.totalReqs.Load(), s.activeSess.Load(), s.errCount.Load()))
}
