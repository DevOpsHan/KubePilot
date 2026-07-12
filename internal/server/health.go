package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// handleHealth 健康检查端点,供 K8S liveness/readiness 探针使用。
func (s *Server) handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "kubepilot-core",
	})
}
