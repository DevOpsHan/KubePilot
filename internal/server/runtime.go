package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"aiops/internal/clusterruntime"
	"aiops/internal/store"
)

func (s *Server) clusterIDFromRequest(c echo.Context) (uint, error) {
	raw := c.QueryParam("cluster_id")
	if raw == "" {
		if s.defaultClusterID == 0 {
			return 0, fmt.Errorf("默认集群未就绪")
		}
		return s.defaultClusterID, nil
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("无效的集群 ID")
	}
	return uint(id), nil
}

func (s *Server) optionalClusterIDFromRequest(c echo.Context) (uint, bool, error) {
	raw := c.QueryParam("cluster_id")
	if raw == "" || raw == "0" {
		return 0, false, nil
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, false, fmt.Errorf("无效的集群 ID")
	}
	return uint(id), true, nil
}

func (s *Server) runtimeFromRequest(c echo.Context) (*clusterruntime.Runtime, error) {
	clusterID, err := s.clusterIDFromRequest(c)
	if err != nil {
		return nil, err
	}
	if err := s.requireClusterPermission(c, store.PermissionViewClusters, clusterID); err != nil {
		return nil, err
	}
	return s.runtimeForClusterID(clusterID)
}

func (s *Server) runtimeForClusterID(clusterID uint) (*clusterruntime.Runtime, error) {
	if s.runtimeManager == nil {
		return nil, fmt.Errorf("集群运行时管理器未就绪")
	}
	return s.runtimeManager.Get(clusterID)
}

func runtimeErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Code
	}
	if err.Error() == "无效的集群 ID" {
		return http.StatusBadRequest
	}
	return http.StatusServiceUnavailable
}
