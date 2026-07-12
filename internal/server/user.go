package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"aiops/internal/store"
)

type createUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	ExpiresIn   string `json:"expires_in"`
	ClusterIDs  []uint `json:"cluster_ids"`
}

type updateUserRequest struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	ExpiresAt   *string `json:"expires_at"`
	Status      *string `json:"status"`
	ClusterIDs  *[]uint `json:"cluster_ids"`
}

func (s *Server) handleListUsers(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageUsers); err != nil {
		return err
	}
	if err := s.cleanupExpiredDeletedUsers(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	users, err := s.store.ListUsers()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	users, err = s.filterManageableUsers(c, users)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(users), "users": users, "roles": store.ValidRoles()})
}

func (s *Server) handleCreateUser(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageUsers); err != nil {
		return err
	}
	if err := s.cleanupExpiredDeletedUsers(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	var req createUserRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体不正确"})
	}
	expiresAt, err := expiryFromOption(req.ExpiresIn)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.validateUserAssignment(c, nil, req.Role, req.ClusterIDs); err != nil {
		return err
	}
	user, err := s.store.CreateUser(store.CreateUserInput{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Role:        req.Role,
		Status:      store.UserStatusActive,
		ExpiresAt:   expiresAt,
		ClusterIDs:  req.ClusterIDs,
	})
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleUpdateUser(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageUsers); err != nil {
		return err
	}
	if err := s.cleanupExpiredDeletedUsers(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的用户 ID"})
	}
	var req updateUserRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体不正确"})
	}
	target, err := s.store.GetUser(uint(id))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	targetRole := target.Role
	if req.ClusterIDs != nil {
		if err := s.validateUserAssignment(c, target, targetRole, *req.ClusterIDs); err != nil {
			return err
		}
	} else if err := s.requireManageTargetUser(c, target); err != nil {
		return err
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		parsed, err := parseUserExpiresAt(*req.ExpiresAt)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		expiresAt = parsed
	}
	user, err := s.store.UpdateUser(uint(id), store.UpdateUserInput{
		DisplayName: req.DisplayName,
		Email:       req.Email,
		ExpiresAt:   expiresAt,
		Status:      req.Status,
		ClusterIDs:  req.ClusterIDs,
	})
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleResetUserPassword(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageUsers); err != nil {
		return err
	}
	if err := s.cleanupExpiredDeletedUsers(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	id, err := parseUserID(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	target, err := s.store.GetUser(id)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireManageTargetUser(c, target); err != nil {
		return err
	}
	user, err := s.store.ResetUserPassword(id)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"user":               user,
		"temporary_password": store.DefaultInitialUserPassword,
	})
}

func (s *Server) handleDeleteUser(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageUsers); err != nil {
		return err
	}
	if err := s.cleanupExpiredDeletedUsers(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	id, err := parseUserID(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	current := currentUser(c)
	if current != nil && current.ID == id {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "不能删除当前登录用户"})
	}
	target, err := s.store.GetUser(id)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireManageTargetUser(c, target); err != nil {
		return err
	}
	if target.Role == store.UserRoleGlobalAdmin {
		count, err := s.store.CountGlobalAdminsExcluding(id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if count == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "不能删除最后一个全局管理员"})
		}
	}
	if err := s.store.SoftDeleteUser(id); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	_ = s.cleanupExpiredDeletedUsers()
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) validateUserAssignment(c echo.Context, target *store.User, role string, clusterIDs []uint) error {
	if s.store == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "数据库未就绪")
	}
	actor := currentUser(c)
	if actor == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "请先登录")
	}
	role = strings.TrimSpace(role)
	if role == "" {
		role = store.UserRoleInspector
	}
	if actor.Role == store.UserRoleGlobalAdmin {
		if store.RoleRequiresClusterGrants(role) && len(clusterIDs) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "该角色必须至少授权一个集群")
		}
		return nil
	}
	if actor.Role != store.UserRoleClusterAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "当前用户没有权限管理用户")
	}
	if role == store.UserRoleGlobalAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "集群管理员不能创建或修改全局管理员")
	}
	if target != nil && target.Role == store.UserRoleGlobalAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "集群管理员不能管理全局管理员")
	}
	if !store.RoleRequiresClusterGrants(role) {
		return echo.NewHTTPError(http.StatusForbidden, "集群管理员只能管理集群管理员或巡检执行者")
	}
	if len(clusterIDs) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "该角色必须至少授权一个集群")
	}
	if err := s.ensureClusterIDsSubset(actor.ID, clusterIDs); err != nil {
		return err
	}
	return nil
}

func (s *Server) requireManageTargetUser(c echo.Context, target *store.User) error {
	actor := currentUser(c)
	if actor == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "请先登录")
	}
	if actor.Role == store.UserRoleGlobalAdmin {
		return nil
	}
	if actor.Role != store.UserRoleClusterAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "当前用户没有权限管理用户")
	}
	if target == nil || target.Role == store.UserRoleGlobalAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "集群管理员不能管理全局管理员")
	}
	targetIDs, err := s.store.ListUserClusterIDs(target.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if len(targetIDs) == 0 {
		return echo.NewHTTPError(http.StatusForbidden, "目标用户没有集群授权范围")
	}
	return s.ensureClusterIDsSubset(actor.ID, targetIDs)
}

func (s *Server) ensureClusterIDsSubset(actorID uint, clusterIDs []uint) error {
	allowed, err := s.store.ListUserClusterIDs(actorID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	allowedSet := make(map[uint]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	for _, id := range clusterIDs {
		if _, ok := allowedSet[id]; !ok {
			return echo.NewHTTPError(http.StatusForbidden, "不能分配或操作未授权集群")
		}
	}
	return nil
}

func (s *Server) filterManageableUsers(c echo.Context, users []store.User) ([]store.User, error) {
	actor := currentUser(c)
	if actor == nil {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "请先登录")
	}
	if actor.Role == store.UserRoleGlobalAdmin {
		return users, nil
	}
	if actor.Role != store.UserRoleClusterAdmin {
		return nil, echo.NewHTTPError(http.StatusForbidden, "当前用户没有权限管理用户")
	}
	allowed, err := s.store.ListUserClusterIDs(actor.ID)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	allowedSet := make(map[uint]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	filtered := make([]store.User, 0, len(users))
	for _, user := range users {
		if user.Role == store.UserRoleGlobalAdmin {
			continue
		}
		if user.ID == actor.ID || userClusterGrantsWithin(user.ClusterGrants, allowedSet) {
			filtered = append(filtered, user)
		}
	}
	return filtered, nil
}

func userClusterGrantsWithin(grants []store.UserClusterGrant, allowed map[uint]struct{}) bool {
	if len(grants) == 0 {
		return false
	}
	for _, grant := range grants {
		if _, ok := allowed[grant.ClusterID]; !ok {
			return false
		}
	}
	return true
}

func parseUserID(c echo.Context) (uint, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "无效的用户 ID")
	}
	return uint(id), nil
}

func expiryFromOption(option string) (*time.Time, error) {
	now := time.Now()
	switch strings.TrimSpace(option) {
	case "3m", "":
		value := now.AddDate(0, 3, 0)
		return &value, nil
	case "6m":
		value := now.AddDate(0, 6, 0)
		return &value, nil
	case "1y":
		value := now.AddDate(1, 0, 0)
		return &value, nil
	case "3y":
		value := now.AddDate(3, 0, 0)
		return &value, nil
	default:
		return nil, echo.NewHTTPError(http.StatusBadRequest, "无效的用户有效期")
	}
}

func parseUserExpiresAt(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "用户失效日期不能为空")
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return &parsed, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "用户失效日期格式不正确")
	}
	endOfDay := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, parsed.Location())
	return &endOfDay, nil
}

func (s *Server) cleanupExpiredDeletedUsers() error {
	if s.store == nil {
		return nil
	}
	_, err := s.store.PurgeDeletedUsers(store.UserDeletedRetention, time.Now())
	return err
}
