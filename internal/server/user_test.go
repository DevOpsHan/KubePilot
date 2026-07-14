package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		"role":"inspector",
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
		Username: "other-admin",
		Password: "admin-secret",
		Role:     store.UserRoleGlobalAdmin,
		Status:   store.UserStatusActive,
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
