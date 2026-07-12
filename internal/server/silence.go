package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"aiops/internal/store"
)

type silenceResponse struct {
	store.AlertSilence
	ClusterName string `json:"cluster_name"`
}

// handleListSilences 返回当前生效(未过期)的静默规则,按授权集群过滤。
func (s *Server) handleListSilences(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionViewAlerts); err != nil {
		return err
	}
	var clusterIDs []uint
	allowed, all, err := s.authorizedClusterIDSet(c)
	if err != nil {
		return err
	}
	if !all {
		clusterIDs = make([]uint, 0, len(allowed))
		for id := range allowed {
			clusterIDs = append(clusterIDs, id)
		}
		if len(clusterIDs) == 0 {
			return c.JSON(http.StatusOK, map[string]any{"count": 0, "silences": []silenceResponse{}})
		}
	}
	silences, err := s.store.ListActiveSilences(clusterIDs)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	clusters, err := s.store.ListClusters()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	clusterNames := make(map[uint]string, len(clusters))
	for _, cluster := range clusters {
		clusterNames[cluster.ID] = cluster.Name
	}
	responses := make([]silenceResponse, 0, len(silences))
	for _, silence := range silences {
		responses = append(responses, silenceResponse{AlertSilence: silence, ClusterName: clusterNames[silence.ClusterID]})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(responses), "silences": responses})
}

// handleCreateSilence 创建静默规则:命中的告警不再推送外部通知并在 UI 标记。
func (s *Server) handleCreateSilence(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	var req struct {
		ClusterID     uint   `json:"cluster_id"`
		Rule          string `json:"rule"`
		Namespace     string `json:"namespace"`
		Name          string `json:"name"`
		DurationHours int    `json:"duration_hours"`
		Comment       string `json:"comment"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体格式错误"})
	}
	req.Rule = strings.TrimSpace(req.Rule)
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Name = strings.TrimSpace(req.Name)
	if req.ClusterID == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "cluster_id 不能为空"})
	}
	if req.Rule == "" && req.Namespace == "" && req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "至少填写规则、命名空间、对象名中的一项"})
	}
	if req.DurationHours < 1 || req.DurationHours > 720 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "静默时长需在 1~720 小时之间"})
	}
	if err := s.requireClusterPermission(c, store.PermissionManageAlerts, req.ClusterID); err != nil {
		return err
	}
	operator := currentUsername(c)
	silence := &store.AlertSilence{
		ClusterID: req.ClusterID,
		Rule:      req.Rule,
		Namespace: req.Namespace,
		Name:      req.Name,
		Comment:   strings.TrimSpace(req.Comment),
		CreatedBy: operator,
		ExpiresAt: time.Now().Add(time.Duration(req.DurationHours) * time.Hour),
	}
	if err := s.store.CreateSilence(silence); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	_ = s.store.AddAudit(req.ClusterID, &store.AuditLog{
		Action:   "create_silence",
		Operator: operator,
		Target:   silenceScopeText(silence),
		Detail:   silence.Comment,
		Result:   "静默至 " + silence.ExpiresAt.Format("2006-01-02 15:04"),
	})
	return c.JSON(http.StatusOK, map[string]any{"silence": silence})
}

// handleDeleteSilence 提前解除静默规则。
func (s *Server) handleDeleteSilence(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的静默 ID"})
	}
	silence, err := s.store.GetSilenceByID(uint(id))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "静默规则不存在"})
	}
	if err := s.requireClusterPermission(c, store.PermissionManageAlerts, silence.ClusterID); err != nil {
		return err
	}
	if err := s.store.DeleteSilence(silence.ID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	_ = s.store.AddAudit(silence.ClusterID, &store.AuditLog{
		Action:   "delete_silence",
		Operator: currentUsername(c),
		Target:   silenceScopeText(silence),
		Detail:   silence.Comment,
		Result:   "已解除静默",
	})
	return c.JSON(http.StatusOK, map[string]string{"message": "已解除静默"})
}

// silenceScopeText 静默范围的可读描述(用于审计与前端展示兜底)。
func silenceScopeText(sl *store.AlertSilence) string {
	parts := make([]string, 0, 3)
	if sl.Rule != "" {
		parts = append(parts, "规则="+sl.Rule)
	}
	if sl.Namespace != "" {
		parts = append(parts, "命名空间="+sl.Namespace)
	}
	if sl.Name != "" {
		parts = append(parts, "对象="+sl.Name)
	}
	return strings.Join(parts, " ")
}
