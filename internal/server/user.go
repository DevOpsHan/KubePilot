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
	DisplayName  *string `json:"display_name"`
	Email        *string `json:"email"`
	Role         *string `json:"role"`
	ExpiresAt    *string `json:"expires_at"`
	NeverExpires *bool   `json:"never_expires"`
	Status       *string `json:"status"`
	ClusterIDs   *[]uint `json:"cluster_ids"`
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
	if err := s.validateUserAssignment(req.Role, req.ClusterIDs); err != nil {
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
	if req.Role != nil {
		targetRole = strings.TrimSpace(*req.Role)
	}
	assignmentClusterIDs, err := s.store.ListUserClusterIDs(target.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if req.ClusterIDs != nil {
		assignmentClusterIDs = *req.ClusterIDs
	}
	if req.Role != nil || req.ClusterIDs != nil {
		if err := s.validateUserAssignment(targetRole, assignmentClusterIDs); err != nil {
			return err
		}
	}
	var expiresAt *time.Time
	expiresAtSet := false
	if req.NeverExpires != nil {
		if *req.NeverExpires {
			if req.ExpiresAt != nil {
				return c.JSON(http.StatusBadRequest, map[string]string{"error": "永不过期与失效日期不能同时设置"})
			}
			expiresAtSet = true
		} else if req.ExpiresAt == nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "设置有限有效期时必须提供失效日期"})
		}
	}
	if req.ExpiresAt != nil {
		parsed, err := parseUserExpiresAt(*req.ExpiresAt)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		expiresAt = parsed
		expiresAtSet = true
	}
	user, err := s.store.UpdateUser(uint(id), store.UpdateUserInput{
		DisplayName:  req.DisplayName,
		Email:        req.Email,
		Role:         req.Role,
		ExpiresAt:    expiresAt,
		ExpiresAtSet: expiresAtSet,
		Status:       req.Status,
		ClusterIDs:   req.ClusterIDs,
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
	if err := s.store.SoftDeleteUser(id); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	_ = s.cleanupExpiredDeletedUsers()
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// validateUserAssignment 校验目标角色的集群授权要求。
// 用户管理入口 requirePermission(users:manage) 已保证仅全局管理员可达。
func (s *Server) validateUserAssignment(role string, clusterIDs []uint) error {
	role = strings.TrimSpace(role)
	if role == "" {
		role = store.UserRoleClusterAdmin
	}
	valid := false
	for _, item := range store.ValidRoles() {
		if role == item {
			valid = true
			break
		}
	}
	if !valid {
		return echo.NewHTTPError(http.StatusBadRequest, store.ErrInvalidRole.Error())
	}
	if store.RoleRequiresClusterGrants(role) && len(clusterIDs) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "该角色必须至少授权一个集群")
	}
	return nil
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
