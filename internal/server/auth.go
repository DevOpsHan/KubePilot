package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"aiops/internal/store"
)

const (
	authCookieName = "aiops_auth"
	currentUserKey = "current_user"
	authSessionTTL = 30 * time.Minute
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	Username        string `json:"username"`
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type loginResponse struct {
	User                   *store.User `json:"user"`
	Token                  string      `json:"token,omitempty"`
	PasswordChangeRequired bool        `json:"password_change_required"`
}

func (s *Server) authMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if s.store == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
		}
		token := bearerToken(c.Request().Header.Get("Authorization"))
		if token == "" {
			if cookie, err := c.Cookie(authCookieName); err == nil {
				token = cookie.Value
			}
		}
		user, session, err := s.store.TouchAuthSession(token, authSessionTTL)
		if err != nil {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "请先登录"})
		}
		setAuthCookie(c, token, session.ExpiresAt)
		c.Set(currentUserKey, user)
		return next(c)
	}
}

func (s *Server) handleAuthLogin(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体不正确"})
	}
	user, err := s.store.AuthenticateUser(req.Username, req.Password)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, store.ErrUserDisabled) || errors.Is(err, store.ErrUserExpired) || errors.Is(err, store.ErrUserDeleted) {
			status = http.StatusForbidden
		}
		return c.JSON(status, map[string]string{"error": err.Error()})
	}
	if user.PasswordChangeRequired {
		return c.JSON(http.StatusOK, loginResponse{User: user, PasswordChangeRequired: true})
	}
	token, session, err := s.store.CreateAuthSession(user.ID, authSessionTTL)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	setAuthCookie(c, token, session.ExpiresAt)
	return c.JSON(http.StatusOK, loginResponse{User: user, Token: token})
}

func (s *Server) handleAuthChangePassword(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	var req changePasswordRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体不正确"})
	}
	user, err := s.store.AuthenticateUser(req.Username, req.CurrentPassword)
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, store.ErrUserDisabled) || errors.Is(err, store.ErrUserExpired) || errors.Is(err, store.ErrUserDeleted) {
			status = http.StatusForbidden
		}
		return c.JSON(status, map[string]string{"error": err.Error()})
	}
	if err := s.store.ChangeUserPassword(user.ID, req.CurrentPassword, req.NewPassword); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	updated, err := s.store.GetUser(user.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	token, session, err := s.store.CreateAuthSession(updated.ID, authSessionTTL)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	setAuthCookie(c, token, session.ExpiresAt)
	return c.JSON(http.StatusOK, loginResponse{User: updated, Token: token})
}

func (s *Server) handleAuthMe(c echo.Context) error {
	user := currentUser(c)
	if user == nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "请先登录"})
	}
	return c.JSON(http.StatusOK, map[string]any{"user": user, "permissions": permissionsForRole(user.Role)})
}

func (s *Server) handleAuthLogout(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	token := bearerToken(c.Request().Header.Get("Authorization"))
	if token == "" {
		if cookie, err := c.Cookie(authCookieName); err == nil {
			token = cookie.Value
		}
	}
	if token != "" {
		_ = s.store.RevokeAuthSession(token)
	}
	clearAuthCookie(c)
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func requirePermission(c echo.Context, permission string) error {
	user := currentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "请先登录")
	}
	if !store.RoleHasPermission(user.Role, permission) {
		return echo.NewHTTPError(http.StatusForbidden, "当前用户没有权限执行该操作")
	}
	return nil
}

func (s *Server) requireGlobalAdmin(c echo.Context) error {
	user := currentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "请先登录")
	}
	if user.Role != store.UserRoleGlobalAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "该操作仅全局管理员可执行")
	}
	return nil
}

func (s *Server) requireClusterPermission(c echo.Context, permission string, clusterID uint) error {
	if err := requirePermission(c, permission); err != nil {
		return err
	}
	user := currentUser(c)
	if user == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "请先登录")
	}
	if user.Role == store.UserRoleGlobalAdmin {
		return nil
	}
	ok, err := s.store.UserHasClusterAccess(user.ID, clusterID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !ok {
		return echo.NewHTTPError(http.StatusForbidden, "当前用户没有权限操作该集群")
	}
	return nil
}

func (s *Server) authorizedClusterIDSet(c echo.Context) (map[uint]struct{}, bool, error) {
	user := currentUser(c)
	if user == nil {
		return nil, false, echo.NewHTTPError(http.StatusUnauthorized, "请先登录")
	}
	if user.Role == store.UserRoleGlobalAdmin {
		return nil, true, nil
	}
	ids, err := s.store.ListUserClusterIDs(user.ID)
	if err != nil {
		return nil, false, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	allowed := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		allowed[id] = struct{}{}
	}
	return allowed, false, nil
}

func (s *Server) authorizedClusterIDs(c echo.Context) ([]uint, bool, error) {
	user := currentUser(c)
	if user == nil {
		return nil, false, echo.NewHTTPError(http.StatusUnauthorized, "请先登录")
	}
	if user.Role == store.UserRoleGlobalAdmin {
		return nil, true, nil
	}
	ids, err := s.store.ListUserClusterIDs(user.ID)
	if err != nil {
		return nil, false, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return ids, false, nil
}

func currentUser(c echo.Context) *store.User {
	value := c.Get(currentUserKey)
	user, _ := value.(*store.User)
	return user
}

func currentUsername(c echo.Context) string {
	user := currentUser(c)
	if user == nil {
		return "未知用户"
	}
	if strings.TrimSpace(user.DisplayName) != "" {
		return user.DisplayName
	}
	return user.Username
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	const prefix = "Bearer "
	if strings.HasPrefix(header, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(header, prefix))
	}
	return ""
}

func setAuthCookie(c echo.Context, token string, expires time.Time) {
	c.SetCookie(&http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/api",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearAuthCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/api",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func permissionsForRole(role string) []string {
	all := []string{
		store.PermissionViewClusters,
		store.PermissionManageClusters,
		store.PermissionClusterExec,
		store.PermissionManageModels,
		store.PermissionManageUsers,
		store.PermissionRunInspect,
		store.PermissionViewApprovals,
		store.PermissionApproveRemediation,
		store.PermissionViewAlerts,
		store.PermissionManageAlerts,
		store.PermissionManageNotifications,
		store.PermissionManageInspection,
	}
	permissions := make([]string, 0, len(all))
	for _, permission := range all {
		if store.RoleHasPermission(role, permission) {
			permissions = append(permissions, permission)
		}
	}
	return permissions
}
