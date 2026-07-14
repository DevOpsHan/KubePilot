package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aiops/internal/store"
)

func TestAuthLoginMeAndLogout(t *testing.T) {
	st := openServerTestStore(t)
	createServerTestUser(t, st, "admin", "admin-secret", store.UserRoleGlobalAdmin)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte(`{"username":"admin","password":"admin-secret"}`)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("登录应成功: code=%d body=%s", loginRec.Code, loginRec.Body.String())
	}
	cookie := authCookieFromRecorder(t, loginRec)
	var loginBody struct {
		User  store.User `json:"user"`
		Token string     `json:"token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("解析登录响应失败: %v", err)
	}
	if loginBody.User.Username != "admin" || loginBody.User.PasswordHash != "" || loginBody.Token == "" {
		t.Fatalf("登录响应不正确或泄露密码哈希: %+v", loginBody)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("当前用户接口应成功: code=%d body=%s", meRec.Code, meRec.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("退出登录应成功: code=%d body=%s", logoutRec.Code, logoutRec.Body.String())
	}

	afterLogoutReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	afterLogoutReq.AddCookie(cookie)
	afterLogoutRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(afterLogoutRec, afterLogoutReq)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("退出后当前用户接口应返回 401: code=%d body=%s", afterLogoutRec.Code, afterLogoutRec.Body.String())
	}
}

func TestAuthSessionUsesThirtyMinuteSlidingExpiry(t *testing.T) {
	st := openServerTestStore(t)
	createServerTestUser(t, st, "admin", "admin-secret", store.UserRoleGlobalAdmin)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte(`{"username":"admin","password":"admin-secret"}`)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("登录应成功: code=%d body=%s", loginRec.Code, loginRec.Body.String())
	}
	cookie := authCookieFromRecorder(t, loginRec)
	if remaining := time.Until(cookie.Expires); remaining < 29*time.Minute || remaining > 31*time.Minute {
		t.Fatalf("登录会话应在约 30 分钟后过期: expires=%s remaining=%s", cookie.Expires, remaining)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("当前用户接口应成功: code=%d body=%s", meRec.Code, meRec.Body.String())
	}
	refreshed := authCookieFromRecorder(t, meRec)
	if remaining := time.Until(refreshed.Expires); remaining < 29*time.Minute || remaining > 31*time.Minute {
		t.Fatalf("认证请求应刷新 30 分钟滑动过期时间: expires=%s remaining=%s", refreshed.Expires, remaining)
	}
}

func TestAuthPasswordChangeRequiredFlow(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "auth-password")
	user, err := st.CreateUser(store.CreateUserInput{
		Username:   "ops",
		Role:       store.UserRoleClusterAdmin,
		Status:     store.UserStatusActive,
		ClusterIDs: []uint{cluster.ID},
	})
	if err != nil {
		t.Fatalf("创建需要改密的用户失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte(`{"username":"ops","password":"`+store.DefaultInitialUserPassword+`"}`)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("默认密码登录应返回改密状态: code=%d body=%s", loginRec.Code, loginRec.Body.String())
	}
	if len(loginRec.Result().Cookies()) != 0 {
		t.Fatalf("改密前不应签发正式会话: cookies=%+v", loginRec.Result().Cookies())
	}

	changeReq := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader([]byte(`{
		"username":"ops",
		"current_password":"`+store.DefaultInitialUserPassword+`",
		"new_password":"ops-new-secret"
	}`)))
	changeReq.Header.Set("Content-Type", "application/json")
	changeRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(changeRec, changeReq)
	if changeRec.Code != http.StatusOK {
		t.Fatalf("首次改密应成功并登录: code=%d body=%s", changeRec.Code, changeRec.Body.String())
	}
	cookie := authCookieFromRecorder(t, changeRec)
	changed, err := st.GetUser(user.ID)
	if err != nil {
		t.Fatalf("读取用户失败: %v", err)
	}
	if changed.PasswordChangeRequired {
		t.Fatalf("改密后应清除强制改密标记: %+v", changed)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	meReq.AddCookie(cookie)
	meRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("改密后会话应可用: code=%d body=%s", meRec.Code, meRec.Body.String())
	}
}

func TestExpiredUserCannotLogin(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "expired-user")
	expiresAt := time.Now().Add(-time.Hour)
	if _, err := st.CreateUser(store.CreateUserInput{
		Username:   "expired",
		Password:   "secret",
		Role:       store.UserRoleClusterAdmin,
		Status:     store.UserStatusActive,
		ExpiresAt:  &expiresAt,
		ClusterIDs: []uint{cluster.ID},
	}); err != nil {
		t.Fatalf("创建过期用户失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte(`{"username":"expired","password":"secret"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("过期用户登录应返回 403: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProtectedAPIsRequireAuthentication(t *testing.T) {
	st := openServerTestStore(t)
	createServerTestUser(t, st, "admin", "admin-secret", store.UserRoleGlobalAdmin)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问受保护接口应返回 401: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestApprovalActionsRespectClusterGrants(t *testing.T) {
	st := openServerTestStore(t)
	clusterA := createServerTestCluster(t, st, "auth-approval-a")
	clusterB := createServerTestCluster(t, st, "auth-approval-b")
	clusterAdmin, err := st.CreateUser(store.CreateUserInput{
		Username:               "cluster-admin",
		Password:               "admin-secret",
		Role:                   store.UserRoleClusterAdmin,
		Status:                 store.UserStatusActive,
		ClusterIDs:             []uint{clusterA.ID},
		PasswordChangeRequired: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("创建集群管理员失败: %v", err)
	}
	ticket, err := st.CreateTicket(clusterA.ID, &store.ApprovalTicket{
		Action:    "restart_pod",
		Namespace: "default",
		Target:    "pod-a",
		Reason:    "权限测试",
		Risk:      "low",
	})
	if err != nil {
		t.Fatalf("创建审批工单失败: %v", err)
	}
	otherTicket, err := st.CreateTicket(clusterB.ID, &store.ApprovalTicket{
		Action:    "restart_pod",
		Namespace: "default",
		Target:    "pod-b",
		Reason:    "权限测试",
		Risk:      "low",
	})
	if err != nil {
		t.Fatalf("创建其他集群审批工单失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, clusterA.ID, nil)

	clusterAdminReq := httptest.NewRequest(http.MethodPost, "/api/approvals/"+strconvUint(ticket.ID)+"/reject", nil)
	attachServerTestSession(t, st, clusterAdminReq, clusterAdmin.ID)
	clusterAdminRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(clusterAdminRec, clusterAdminReq)
	if clusterAdminRec.Code != http.StatusOK {
		t.Fatalf("集群管理员应能拒绝授权集群工单: code=%d body=%s", clusterAdminRec.Code, clusterAdminRec.Body.String())
	}
	updated, err := st.GetTicketByID(ticket.ID)
	if err != nil {
		t.Fatalf("读取工单失败: %v", err)
	}
	if updated.Approver != "cluster-admin" || updated.Status != store.TicketRejected {
		t.Fatalf("工单审批人和状态不正确: %+v", updated)
	}

	otherReq := httptest.NewRequest(http.MethodPost, "/api/approvals/"+strconvUint(otherTicket.ID)+"/reject", nil)
	attachServerTestSession(t, st, otherReq, clusterAdmin.ID)
	otherRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(otherRec, otherReq)
	if otherRec.Code != http.StatusForbidden {
		t.Fatalf("集群管理员不应能审批未授权集群工单: code=%d body=%s", otherRec.Code, otherRec.Body.String())
	}
}

func TestClusterAdminCannotUseGlobalAdminAPIs(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "cluster-admin-scope")
	clusterAdmin, err := st.CreateUser(store.CreateUserInput{
		Username:               "cluster-admin",
		Password:               "admin-secret",
		Role:                   store.UserRoleClusterAdmin,
		Status:                 store.UserStatusActive,
		ClusterIDs:             []uint{cluster.ID},
		PasswordChangeRequired: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("创建集群管理员失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, nil)

	// 全局管理员专属接口:模型管理(models:manage)、用户管理(users:manage),集群管理员均无权
	tests := []struct {
		method string
		target string
		body   string
	}{
		{http.MethodPost, "/api/models", `{"name":"m","provider":"openai","model_name":"m","api_key":"k"}`},
		{http.MethodGet, "/api/users", ""},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.target, bytes.NewReader([]byte(tt.body)))
		req.Header.Set("Content-Type", "application/json")
		attachServerTestSession(t, st, req, clusterAdmin.ID)
		rec := httptest.NewRecorder()
		srv.Echo().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("集群管理员访问 %s 应返回 403: code=%d body=%s", tt.target, rec.Code, rec.Body.String())
		}
	}
}

func createServerTestUser(t *testing.T, st *store.Store, username, password, role string) *store.User {
	t.Helper()
	user, err := st.CreateUser(store.CreateUserInput{
		Username:               username,
		DisplayName:            username,
		Password:               password,
		Role:                   role,
		Status:                 store.UserStatusActive,
		PasswordChangeRequired: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	return user
}

func boolPtr(value bool) *bool {
	return &value
}

func attachServerTestAdminSession(t *testing.T, st *store.Store, req *http.Request) *store.User {
	t.Helper()
	user := createServerTestUser(
		t,
		st,
		fmt.Sprintf("admin-%d", time.Now().UnixNano()),
		"admin-secret",
		store.UserRoleGlobalAdmin,
	)
	attachServerTestSession(t, st, req, user.ID)
	return user
}

func attachServerTestSession(t *testing.T, st *store.Store, req *http.Request, userID uint) {
	t.Helper()
	token, _, err := st.CreateAuthSession(userID, time.Hour)
	if err != nil {
		t.Fatalf("创建测试会话失败: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
}

func authCookieFromRecorder(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == authCookieName && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("响应缺少认证 Cookie: %s", rec.Body.String())
	return nil
}
