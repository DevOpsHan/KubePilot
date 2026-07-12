package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	"aiops/internal/agent"
	"aiops/internal/store"
)

// chatRequest 对话请求体。
type chatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
	ClusterID uint   `json:"cluster_id"`
}

// handleChat SSE 流式对话:实时推送 思考/工具调用/回答 事件。会话记忆存 Redis。
func (s *Server) handleChat(c echo.Context) error {
	if s.agent == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "对话助手未就绪",
		})
	}
	var req chatRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
	}
	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "消息不能为空"})
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}
	user := currentUser(c)
	if user == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "请先登录"})
	}
	clusterID := req.ClusterID
	sessionID := scopedSessionID(user.ID, clusterID, req.SessionID)
	identity := agent.UserIdentity{
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        user.Role,
	}
	if !s.agent.Ready() && !agent.CanAnswerIdentity(req.Message, identity) {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "模型未配置，请前往智能助手配置你的专属运维助手",
		})
	}
	clusterIDs, allClusters, err := s.authorizedClusterIDs(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	access := agent.ChatAccessScope{
		AllClusters: allClusters,
		ClusterIDs:  clusterIDs,
		Permissions: permissionsForRole(user.Role),
	}
	if clusterID != 0 {
		if err := s.requireClusterPermission(c, store.PermissionViewClusters, clusterID); err != nil {
			return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
		}
		if _, err := s.runtimeForClusterID(clusterID); err != nil {
			return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
		}
	}

	res := c.Response()
	res.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	res.Header().Set("Cache-Control", "no-cache, no-transform")
	res.Header().Set("Connection", "keep-alive")
	res.Header().Set("X-Accel-Buffering", "no")
	res.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(res)
	s.agent.ChatStream(c.Request().Context(), sessionID, req.Message, clusterID, identity, access, func(ev agent.StreamEvent) {
		// SSE 帧格式: "data: {json}\n\n"
		_, _ = res.Write([]byte("data: "))
		_ = enc.Encode(ev) // Encode 自带换行
		_, _ = res.Write([]byte("\n"))
		res.Flush()
	})
	return nil
}

// handleChatHistory 返回会话历史(前端刷新后用于恢复显示)。
func (s *Server) handleChatHistory(c echo.Context) error {
	if s.agent == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "对话助手未就绪"})
	}
	user := currentUser(c)
	if user == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "请先登录"})
	}
	sid := c.QueryParam("session_id")
	clusterID, err := optionalClusterID(c.QueryParam("cluster_id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	msgs, err := s.agent.History(c.Request().Context(), scopedSessionID(user.ID, clusterID, sid))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"messages": msgs})
}

// handleChatClear 清空指定会话。
func (s *Server) handleChatClear(c echo.Context) error {
	if s.agent == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "对话助手未就绪"})
	}
	user := currentUser(c)
	if user == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "请先登录"})
	}
	var req chatRequest
	_ = c.Bind(&req)
	clusterID := req.ClusterID
	if err := s.agent.ClearSession(c.Request().Context(), scopedSessionID(user.ID, clusterID, req.SessionID)); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func scopedSessionID(userID, clusterID uint, sessionID string) string {
	if sessionID == "" {
		sessionID = "default"
	}
	if clusterID == 0 {
		return fmt.Sprintf("user:%d:clusters:all:%s", userID, sessionID)
	}
	return fmt.Sprintf("user:%d:cluster:%d:%s", userID, clusterID, sessionID)
}

func optionalClusterID(raw string) (uint, error) {
	if raw == "" || raw == "0" {
		return 0, nil
	}
	var id uint
	if _, err := fmt.Sscanf(raw, "%d", &id); err != nil || id == 0 {
		return 0, fmt.Errorf("无效的集群 ID")
	}
	return id, nil
}
