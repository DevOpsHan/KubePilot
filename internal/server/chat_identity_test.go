package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"aiops/internal/agent"
	"aiops/internal/ai"
	"aiops/internal/clusterruntime"
	"aiops/internal/session"
	"aiops/internal/store"
)

func TestChatAnswersAuthenticatedIdentityWithoutCallingModel(t *testing.T) {
	st := openChatIdentityTestStore(t)
	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "身份测试集群",
		Slug: "identity-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建身份测试集群失败: %v", err)
	}
	recorder := &chatModelRecorder{}
	srv := newChatIdentityTestServer(t, st, recorder)

	testCases := []struct {
		username string
		role     string
		message  string
		expected []string
	}{
		{username: "identity-admin", role: store.UserRoleGlobalAdmin, message: "我是谁？", expected: []string{"系统管理员", "平台系统最高权限者"}},
		{username: "identity-cluster-admin", role: store.UserRoleClusterAdmin, message: "我是谁？", expected: []string{"集群管理员", "已授权集群"}},
		{username: "identity-admin-en", role: store.UserRoleGlobalAdmin, message: "Who am I?", expected: []string{"System Administrator", "highest-privilege"}},
		{username: "identity-admin-polite", role: store.UserRoleGlobalAdmin, message: "请问我是谁？", expected: []string{"系统管理员", "平台系统最高权限者"}},
		{username: "identity-cluster-admin-en", role: store.UserRoleClusterAdmin, message: "What's my role?", expected: []string{"Cluster Administrator", "authorized clusters"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.username, func(t *testing.T) {
			input := store.CreateUserInput{
				Username:               testCase.username,
				DisplayName:            testCase.username,
				Password:               "secret",
				Role:                   testCase.role,
				Status:                 store.UserStatusActive,
				PasswordChangeRequired: boolPtr(false),
			}
			if testCase.role != store.UserRoleGlobalAdmin {
				input.ClusterIDs = []uint{cluster.ID}
			}
			user, createErr := st.CreateUser(input)
			if createErr != nil {
				t.Fatalf("创建身份测试用户失败: %v", createErr)
			}

			rec := performIdentityChatRequest(t, srv, st, user.ID, `{
				"session_id":"identity-session",
				"message":"`+testCase.message+`",
				"username":"forged-user",
				"role":"global_admin"
			}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("身份问答状态码错误: code=%d body=%s", rec.Code, rec.Body.String())
			}
			answer := rec.Body.String()
			for _, expected := range append([]string{testCase.username}, testCase.expected...) {
				if !strings.Contains(answer, expected) {
					t.Fatalf("身份回答缺少 %q: %s", expected, answer)
				}
			}
		})
	}
	if calls := recorder.callCount(); calls != 0 {
		t.Fatalf("确定性身份问答不应调用模型: calls=%d", calls)
	}
}

func TestChatAnswersAuthenticatedIdentityWithoutConfiguredModel(t *testing.T) {
	st := openChatIdentityTestStore(t)
	user := createServerTestUser(t, st, "local-identity-admin", "secret", store.UserRoleGlobalAdmin)
	ag := agent.New(nil, nil, session.NewMemoryStore())
	srv := NewServer(nil, nil, nil, st, ag, nil, 0, nil)

	rec := performIdentityChatRequest(t, srv, st, user.ID, `{
		"session_id":"local-identity-session",
		"message":"请问我是谁？"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("无模型时身份问答状态码错误: code=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, expected := range []string{"local-identity-admin", "系统管理员", "平台系统最高权限者", `"type":"done"`} {
		if !strings.Contains(rec.Body.String(), expected) {
			t.Fatalf("无模型时身份回答缺少 %q: %s", expected, rec.Body.String())
		}
	}
}

func TestChatInjectsAuthenticatedIdentityIntoSystemPrompt(t *testing.T) {
	st := openChatIdentityTestStore(t)
	user := createServerTestUser(t, st, "prompt-admin", "secret", store.UserRoleGlobalAdmin)
	recorder := &chatModelRecorder{}
	srv := newChatIdentityTestServer(t, st, recorder)

	rec := performIdentityChatRequest(t, srv, st, user.ID, `{
		"session_id":"prompt-session",
		"message":"请根据我的职责给一条值班建议",
		"username":"forged-user",
		"role":"inspector"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("对话状态码错误: code=%d body=%s", rec.Code, rec.Body.String())
	}

	requests := recorder.snapshot()
	if len(requests) != 1 || len(requests[0].Messages) == 0 {
		t.Fatalf("模型请求数量或消息不正确: %+v", requests)
	}
	systemPrompt := requests[0].Messages[0].Content
	for _, expected := range []string{"prompt-admin", store.UserRoleGlobalAdmin, "平台系统最高权限者", "服务端认证", "集群访问权限"} {
		if !strings.Contains(systemPrompt, expected) {
			t.Fatalf("系统提示缺少认证身份 %q: %s", expected, systemPrompt)
		}
	}
	if strings.Contains(systemPrompt, "forged-user") || strings.Contains(systemPrompt, "inspector") {
		t.Fatalf("系统提示不应采用请求体伪造身份: %s", systemPrompt)
	}
}

func TestChatHistoryIsIsolatedByAuthenticatedUser(t *testing.T) {
	st := openChatIdentityTestStore(t)
	firstUser := createServerTestUser(t, st, "first-admin", "secret", store.UserRoleGlobalAdmin)
	secondUser := createServerTestUser(t, st, "second-admin", "secret", store.UserRoleGlobalAdmin)
	recorder := &chatModelRecorder{}
	srv := newChatIdentityTestServer(t, st, recorder)

	first := performIdentityChatRequest(t, srv, st, firstUser.ID, `{
		"session_id":"shared-session",
		"message":"first-user-secret-context"
	}`)
	if first.Code != http.StatusOK {
		t.Fatalf("首个用户对话失败: code=%d body=%s", first.Code, first.Body.String())
	}
	second := performIdentityChatRequest(t, srv, st, secondUser.ID, `{
		"session_id":"shared-session",
		"message":"second-user-question"
	}`)
	if second.Code != http.StatusOK {
		t.Fatalf("第二个用户对话失败: code=%d body=%s", second.Code, second.Body.String())
	}

	requests := recorder.snapshot()
	if len(requests) != 2 {
		t.Fatalf("模型请求数量错误: got=%d", len(requests))
	}
	for _, message := range requests[1].Messages {
		if strings.Contains(message.Content, "first-user-secret-context") {
			t.Fatalf("第二个用户读取到了首个用户的会话历史: %+v", requests[1].Messages)
		}
	}
	if systemPrompt := requests[1].Messages[0].Content; !strings.Contains(systemPrompt, "second-admin") || strings.Contains(systemPrompt, "first-admin") {
		t.Fatalf("第二个用户的认证身份上下文不正确: %s", systemPrompt)
	}
}

func TestChatHistoryAndClearEndpointsAreIsolatedByAuthenticatedUser(t *testing.T) {
	st := openChatIdentityTestStore(t)
	firstUser := createServerTestUser(t, st, "history-first-admin", "secret", store.UserRoleGlobalAdmin)
	secondUser := createServerTestUser(t, st, "history-second-admin", "secret", store.UserRoleGlobalAdmin)
	recorder := &chatModelRecorder{}
	srv := newChatIdentityTestServer(t, st, recorder)

	for _, user := range []*store.User{firstUser, secondUser} {
		rec := performIdentityChatRequest(t, srv, st, user.ID, `{
			"session_id":"shared-endpoint-session",
			"message":"我是谁？"
		}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("写入用户 %s 会话失败: code=%d body=%s", user.Username, rec.Code, rec.Body.String())
		}
	}

	firstHistory := performAuthenticatedRequest(t, srv, st, firstUser.ID, http.MethodGet, "/api/chat/history?session_id=shared-endpoint-session", "")
	if !strings.Contains(firstHistory.Body.String(), firstUser.Username) || strings.Contains(firstHistory.Body.String(), secondUser.Username) {
		t.Fatalf("首个用户历史未正确隔离: %s", firstHistory.Body.String())
	}
	secondHistory := performAuthenticatedRequest(t, srv, st, secondUser.ID, http.MethodGet, "/api/chat/history?session_id=shared-endpoint-session", "")
	if !strings.Contains(secondHistory.Body.String(), secondUser.Username) || strings.Contains(secondHistory.Body.String(), firstUser.Username) {
		t.Fatalf("第二个用户历史未正确隔离: %s", secondHistory.Body.String())
	}

	clear := performAuthenticatedRequest(t, srv, st, firstUser.ID, http.MethodPost, "/api/chat/clear", `{"session_id":"shared-endpoint-session"}`)
	if clear.Code != http.StatusOK {
		t.Fatalf("清空首个用户会话失败: code=%d body=%s", clear.Code, clear.Body.String())
	}
	firstHistory = performAuthenticatedRequest(t, srv, st, firstUser.ID, http.MethodGet, "/api/chat/history?session_id=shared-endpoint-session", "")
	if strings.Contains(firstHistory.Body.String(), firstUser.Username) {
		t.Fatalf("首个用户历史未被清空: %s", firstHistory.Body.String())
	}
	secondHistory = performAuthenticatedRequest(t, srv, st, secondUser.ID, http.MethodGet, "/api/chat/history?session_id=shared-endpoint-session", "")
	if !strings.Contains(secondHistory.Body.String(), secondUser.Username) {
		t.Fatalf("清空首个用户不应影响第二个用户: %s", secondHistory.Body.String())
	}
}

func TestChatRejectsUnauthorizedRequestCluster(t *testing.T) {
	st := openChatIdentityTestStore(t)
	allowedCluster, err := st.CreateCluster(store.CreateClusterInput{Name: "授权集群", Slug: "allowed-cluster"})
	if err != nil {
		t.Fatalf("创建授权集群失败: %v", err)
	}
	deniedCluster, err := st.CreateCluster(store.CreateClusterInput{Name: "未授权集群", Slug: "denied-cluster"})
	if err != nil {
		t.Fatalf("创建未授权集群失败: %v", err)
	}
	user, err := st.CreateUser(store.CreateUserInput{
		Username:               "scoped-inspector",
		DisplayName:            "范围巡检员",
		Password:               "secret",
		Role:                   store.UserRoleClusterAdmin,
		Status:                 store.UserStatusActive,
		PasswordChangeRequired: boolPtr(false),
		ClusterIDs:             []uint{allowedCluster.ID},
	})
	if err != nil {
		t.Fatalf("创建范围测试用户失败: %v", err)
	}
	recorder := &chatModelRecorder{}
	srv := newChatIdentityTestServer(t, st, recorder)
	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{ClusterID: deniedCluster.ID, Cluster: deniedCluster}); err != nil {
		t.Fatalf("注册未授权集群运行时失败: %v", err)
	}
	srv.runtimeManager = manager

	rec := performIdentityChatRequest(t, srv, st, user.ID, fmt.Sprintf(`{
		"session_id":"denied-request-cluster",
		"message":"查看当前集群状态",
		"cluster_id":%d
	}`, deniedCluster.ID))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("未授权请求集群应返回 403: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if calls := recorder.callCount(); calls != 0 {
		t.Fatalf("未授权请求不应调用模型: calls=%d", calls)
	}
}

func TestChatToolCallsCannotEscapeAuthenticatedClusterScope(t *testing.T) {
	st := openChatIdentityTestStore(t)
	allowedCluster, err := st.CreateCluster(store.CreateClusterInput{Name: "授权工具集群", Slug: "allowed-tool-cluster"})
	if err != nil {
		t.Fatalf("创建授权工具集群失败: %v", err)
	}
	deniedCluster, err := st.CreateCluster(store.CreateClusterInput{Name: "机密工具集群", Slug: "secret-tool-cluster"})
	if err != nil {
		t.Fatalf("创建机密工具集群失败: %v", err)
	}
	user, err := st.CreateUser(store.CreateUserInput{
		Username:               "tool-scoped-inspector",
		DisplayName:            "工具范围巡检员",
		Password:               "secret",
		Role:                   store.UserRoleClusterAdmin,
		Status:                 store.UserStatusActive,
		PasswordChangeRequired: boolPtr(false),
		ClusterIDs:             []uint{allowedCluster.ID},
	})
	if err != nil {
		t.Fatalf("创建工具范围用户失败: %v", err)
	}

	manager := clusterruntime.NewManager()
	for _, cluster := range []*store.Cluster{allowedCluster, deniedCluster} {
		if err := manager.Register(&clusterruntime.Runtime{ClusterID: cluster.ID, Cluster: cluster}); err != nil {
			t.Fatalf("注册集群 %d 运行时失败: %v", cluster.ID, err)
		}
	}
	recorder := &chatModelRecorder{}
	srv := newChatToolCallTestServer(t, st, recorder, manager, deniedCluster.ID)

	rec := performIdentityChatRequest(t, srv, st, user.ID, `{
		"session_id":"tool-scope-session",
		"message":"请提供一份资源详情"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("工具范围对话失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	requests := recorder.snapshot()
	if len(requests) != 2 {
		t.Fatalf("模型请求数量错误: got=%d", len(requests))
	}
	var toolResult string
	for _, message := range requests[1].Messages {
		if message.Role == "tool" {
			toolResult = message.Content
		}
	}
	if toolResult == "" {
		t.Fatalf("第二轮模型请求缺少工具结果: %+v", requests[1].Messages)
	}
	if strings.Contains(toolResult, deniedCluster.Name) || strings.Contains(toolResult, deniedCluster.Slug) {
		t.Fatalf("工具结果泄露未授权集群: %s", toolResult)
	}
	if !strings.Contains(toolResult, "无权") && !strings.Contains(toolResult, "授权范围") {
		t.Fatalf("工具结果应明确拒绝越权访问: %s", toolResult)
	}
}

type chatIdentityCompletionRequest struct {
	Messages []ai.Message `json:"messages"`
}

type chatModelRecorder struct {
	mu       sync.Mutex
	requests []chatIdentityCompletionRequest
}

func (r *chatModelRecorder) record(request chatIdentityCompletionRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
}

func (r *chatModelRecorder) snapshot() []chatIdentityCompletionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]chatIdentityCompletionRequest(nil), r.requests...)
}

func (r *chatModelRecorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func newChatIdentityTestServer(t *testing.T, st *store.Store, recorder *chatModelRecorder) *Server {
	t.Helper()
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			t.Fatalf("模型请求路径不正确: %s", request.URL.Path)
		}
		var body chatIdentityCompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("解析模型请求失败: %v", err)
		}
		recorder.record(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"模型占位回答\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(modelServer.Close)

	ag := agent.New(
		ai.NewClient(modelServer.URL, "test-key", "test-model", 512, 5, 0),
		nil,
		session.NewMemoryStore(),
	)
	return NewServer(nil, nil, nil, st, ag, nil, 0, nil)
}

func newChatToolCallTestServer(t *testing.T, st *store.Store, recorder *chatModelRecorder, manager *clusterruntime.Manager, deniedClusterID uint) *Server {
	t.Helper()
	var requestCount int
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body chatIdentityCompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		recorder.record(body)
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")
		if requestCount == 1 {
			payload := fmt.Sprintf(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"scope-call","function":{"name":"list_nodes","arguments":"{\"cluster_id\":%d}"}}]}}]}`+"\n\n", deniedClusterID)
			_, _ = w.Write([]byte(payload))
		} else {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"范围检查完成\"}}]}\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(modelServer.Close)

	executor := agent.NewToolExecutor(nil, nil, st, nil, 0, manager)
	ag := agent.New(ai.NewClient(modelServer.URL, "test-key", "test-model", 512, 5, 0), executor, session.NewMemoryStore())
	return NewServer(nil, nil, nil, st, ag, nil, 0, manager)
}

func openChatIdentityTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func performIdentityChatRequest(t *testing.T, srv *Server, st *store.Store, userID uint, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestSession(t, st, req, userID)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	return rec
}

func performAuthenticatedRequest(t *testing.T, srv *Server, st *store.Store, userID uint, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	attachServerTestSession(t, st, req, userID)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	return rec
}
