package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aiops/internal/store"
)

func TestUserManagementCreateEditResetAndDelete(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "user-mgmt-a")
	admin := createServerTestUser(t, st, "admin", "admin-secret", store.UserRoleGlobalAdmin)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	createReq := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader([]byte(`{
		"username":"ops",
		"display_name":"运维同学",
		"email":"ops@example.com",
		"role":"cluster_admin",
		"expires_in":"3m",
		"cluster_ids":[`+strconvUint(cluster.ID)+`]
	}`)))
	createReq.Header.Set("Content-Type", "application/json")
	attachServerTestSession(t, st, createReq, admin.ID)
	createRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("创建用户应成功: code=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var createBody struct {
		User store.User `json:"user"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("解析创建用户响应失败: %v", err)
	}
	if createBody.User.Email != "ops@example.com" || createBody.User.ExpiresAt == nil || !createBody.User.PasswordChangeRequired || len(createBody.User.ClusterGrants) != 1 {
		t.Fatalf("创建用户响应不正确: %+v", createBody.User)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte(`{"username":"ops","password":"`+store.DefaultInitialUserPassword+`"}`)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK || len(loginRec.Result().Cookies()) != 0 {
		t.Fatalf("首次登录应要求改密且不签发会话: code=%d cookies=%d body=%s", loginRec.Code, len(loginRec.Result().Cookies()), loginRec.Body.String())
	}
	if !strings.Contains(loginRec.Body.String(), `"password_change_required":true`) {
		t.Fatalf("首次登录响应应标记需要改密: %s", loginRec.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/users/"+strconvUint(createBody.User.ID), bytes.NewReader([]byte(`{
		"display_name":"值班同学",
		"email":"duty@example.com",
		"expires_at":"2027-06-29",
		"cluster_ids":[`+strconvUint(cluster.ID)+`]
	}`)))
	updateReq.Header.Set("Content-Type", "application/json")
	attachServerTestSession(t, st, updateReq, admin.ID)
	updateRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("编辑用户应成功: code=%d body=%s", updateRec.Code, updateRec.Body.String())
	}

	resetReq := httptest.NewRequest(http.MethodPost, "/api/users/"+strconvUint(createBody.User.ID)+"/reset-password", nil)
	attachServerTestSession(t, st, resetReq, admin.ID)
	resetRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(resetRec, resetReq)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("重置密码应成功: code=%d body=%s", resetRec.Code, resetRec.Body.String())
	}
	resetUser, err := st.AuthenticateUser("ops", store.DefaultInitialUserPassword)
	if err != nil {
		t.Fatalf("重置后默认密码应可验证: %v", err)
	}
	if !resetUser.PasswordChangeRequired {
		t.Fatalf("重置密码后应要求用户再次改密: %+v", resetUser)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/users/"+strconvUint(createBody.User.ID), nil)
	attachServerTestSession(t, st, deleteReq, admin.ID)
	deleteRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("删除用户应成功: code=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	listReq := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	attachServerTestSession(t, st, listReq, admin.ID)
	listRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("用户列表应成功: code=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), `"username":"ops"`) {
		t.Fatalf("删除后的用户不应出现在用户列表: %s", listRec.Body.String())
	}
}

func TestUserDeletionGuardsCurrentAndLastAdmin(t *testing.T) {
	st := openServerTestStore(t)
	admin := createServerTestUser(t, st, "admin", "admin-secret", store.UserRoleGlobalAdmin)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	selfReq := httptest.NewRequest(http.MethodDelete, "/api/users/"+strconvUint(admin.ID), nil)
	attachServerTestSession(t, st, selfReq, admin.ID)
	selfRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(selfRec, selfReq)
	if selfRec.Code != http.StatusBadRequest {
		t.Fatalf("不应允许删除当前登录用户: code=%d body=%s", selfRec.Code, selfRec.Body.String())
	}

	otherAdmin, err := st.CreateUser(store.CreateUserInput{
		Username:               "other-admin",
		Password:               "admin-secret",
		Role:                   store.UserRoleGlobalAdmin,
		Status:                 store.UserStatusActive,
		PasswordChangeRequired: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("创建第二管理员失败: %v", err)
	}
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/users/"+strconvUint(admin.ID), nil)
	attachServerTestSession(t, st, deleteReq, otherAdmin.ID)
	deleteRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("存在其他管理员时应允许删除目标管理员: code=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestUserManagementCanChangeRoleAndRevokesOldSession(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "role-update")
	admin := createServerTestUser(t, st, "role-admin", "admin-secret", store.UserRoleGlobalAdmin)
	target, err := st.CreateUser(store.CreateUserInput{
		Username: "role-target", Password: "target-secret", Role: store.UserRoleClusterAdmin,
		Status: store.UserStatusActive, PasswordChangeRequired: boolPtr(false), ClusterIDs: []uint{cluster.ID},
	})
	if err != nil {
		t.Fatalf("创建目标用户失败: %v", err)
	}
	oldToken, _, err := st.CreateAuthSession(target, time.Hour)
	if err != nil {
		t.Fatalf("创建目标用户旧会话失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/users/"+strconvUint(target.ID), bytes.NewReader([]byte(`{
		"role":"global_admin",
		"cluster_ids":[]
	}`)))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestSession(t, st, req, admin.ID)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("全局管理员应能修改用户角色: code=%d body=%s", rec.Code, rec.Body.String())
	}
	updated, err := st.GetUser(target.ID)
	if err != nil {
		t.Fatalf("读取角色修改结果失败: %v", err)
	}
	if updated.Role != store.UserRoleGlobalAdmin || len(updated.ClusterGrants) != 0 {
		t.Fatalf("切换为全局管理员后应清理集群授权: %+v", updated)
	}
	if _, err := st.GetUserBySessionToken(oldToken); !errors.Is(err, store.ErrInvalidSession) {
		t.Fatalf("角色变化后目标用户旧会话必须失效: %v", err)
	}
}

func TestUserRoleUpdateValidatesTargetRoleAndProtectsLastAdmin(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "role-guard")
	admin := createServerTestUser(t, st, "only-admin", "admin-secret", store.UserRoleGlobalAdmin)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	cases := []struct {
		name string
		body string
	}{
		{name: "最后一个全局管理员不能降级", body: `{"role":"cluster_admin","cluster_ids":[` + strconvUint(cluster.ID) + `]}`},
		{name: "目标角色必须合法", body: `{"role":"inspector","cluster_ids":[` + strconvUint(cluster.ID) + `]}`},
		{name: "集群管理员必须授权集群", body: `{"role":"cluster_admin","cluster_ids":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/users/"+strconvUint(admin.ID), bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			attachServerTestSession(t, st, req, admin.ID)
			rec := httptest.NewRecorder()
			srv.Echo().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s 应被拒绝: code=%d body=%s", tc.name, rec.Code, rec.Body.String())
			}
			after, err := st.GetUser(admin.ID)
			if err != nil || after.Role != store.UserRoleGlobalAdmin {
				t.Fatalf("失败请求不应部分修改角色: user=%+v err=%v", after, err)
			}
		})
	}
}

func TestLastPersistentGlobalAdminCannotBeMadeNonPersistent(t *testing.T) {
	tests := []struct {
		name string
		body func(clusterID uint) string
	}{
		{
			name: "降级角色",
			body: func(clusterID uint) string {
				return `{"role":"cluster_admin","cluster_ids":[` + strconvUint(clusterID) + `]}`
			},
		},
		{name: "停用账号", body: func(uint) string { return `{"status":"disabled"}` }},
		{
			name: "有效期设为未来",
			body: func(uint) string {
				return `{"expires_at":"` + time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339) + `"}`
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openServerTestStore(t)
			cluster := createServerTestCluster(t, st, "usable-admin-guard")
			admin := createServerTestUser(t, st, "usable-admin", "admin-secret", store.UserRoleGlobalAdmin)
			disabled, err := st.CreateUser(store.CreateUserInput{
				Username: "disabled-admin", Password: "admin-secret", Role: store.UserRoleGlobalAdmin,
				Status: store.UserStatusDisabled,
			})
			if err != nil || disabled == nil {
				t.Fatalf("创建停用管理员失败: user=%+v err=%v", disabled, err)
			}
			expiredAt := time.Now().Add(-time.Hour)
			expired, err := st.CreateUser(store.CreateUserInput{
				Username: "expired-admin", Password: "admin-secret", Role: store.UserRoleGlobalAdmin,
				Status: store.UserStatusActive, ExpiresAt: &expiredAt,
			})
			if err != nil || expired == nil {
				t.Fatalf("创建过期管理员失败: user=%+v err=%v", expired, err)
			}
			futureExpiry := time.Now().AddDate(1, 0, 0)
			limited, err := st.CreateUser(store.CreateUserInput{
				Username: "limited-admin", Password: "admin-secret", Role: store.UserRoleGlobalAdmin,
				Status: store.UserStatusActive, ExpiresAt: &futureExpiry,
			})
			if err != nil || limited == nil {
				t.Fatalf("创建有限期限管理员失败: user=%+v err=%v", limited, err)
			}
			srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

			req := httptest.NewRequest(http.MethodPut, "/api/users/"+strconvUint(admin.ID), bytes.NewReader([]byte(tc.body(cluster.ID))))
			req.Header.Set("Content-Type", "application/json")
			attachServerTestSession(t, st, req, admin.ID)
			rec := httptest.NewRecorder()
			srv.Echo().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("最后一个持久全局管理员%s必须被拒绝: code=%d body=%s", tc.name, rec.Code, rec.Body.String())
			}
			after, err := st.GetUser(admin.ID)
			if err != nil || after.Role != store.UserRoleGlobalAdmin || after.Status != store.UserStatusActive || after.ExpiresAt != nil {
				t.Fatalf("失败更新必须完整回滚: user=%+v err=%v", after, err)
			}
		})
	}
}

func TestUserExpiryCanBeExplicitlyClearedAndRevokesOldSession(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "never-expire")
	admin := createServerTestUser(t, st, "expiry-admin", "admin-secret", store.UserRoleGlobalAdmin)
	futureExpiry := time.Now().AddDate(1, 0, 0)
	target, err := st.CreateUser(store.CreateUserInput{
		Username: "finite-user", Password: "user-secret", Role: store.UserRoleClusterAdmin,
		Status: store.UserStatusActive, ExpiresAt: &futureExpiry,
		PasswordChangeRequired: boolPtr(false), ClusterIDs: []uint{cluster.ID},
	})
	if err != nil {
		t.Fatalf("创建有限期限用户失败: %v", err)
	}
	oldToken, _, err := st.CreateAuthSession(target, time.Hour)
	if err != nil {
		t.Fatalf("创建目标用户旧会话失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/users/"+strconvUint(target.ID), bytes.NewReader([]byte(`{"never_expires":true}`)))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestSession(t, st, req, admin.ID)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("应允许显式清空用户有效期: code=%d body=%s", rec.Code, rec.Body.String())
	}
	updated, err := st.GetUser(target.ID)
	if err != nil || updated.ExpiresAt != nil {
		t.Fatalf("never_expires=true 应清空 expires_at: user=%+v err=%v", updated, err)
	}
	if _, err := st.GetUserBySessionToken(oldToken); !errors.Is(err, store.ErrInvalidSession) {
		t.Fatalf("有效期发生显式变化后旧会话必须撤销: %v", err)
	}
}

func TestUserExpiryUpdateRejectsConflictingInputs(t *testing.T) {
	st := openServerTestStore(t)
	admin := createServerTestUser(t, st, "expiry-validation-admin", "admin-secret", store.UserRoleGlobalAdmin)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	tests := []struct {
		name string
		body string
	}{
		{name: "永不过期与具体日期冲突", body: `{"never_expires":true,"expires_at":"2028-01-01"}`},
		{name: "有限期限缺少具体日期", body: `{"never_expires":false}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/users/"+strconvUint(admin.ID), bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			attachServerTestSession(t, st, req, admin.ID)
			rec := httptest.NewRecorder()
			srv.Echo().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("冲突的有效期输入必须被拒绝: code=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
