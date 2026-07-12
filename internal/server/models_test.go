package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"aiops/internal/agent"
	"aiops/internal/ai"
	"aiops/internal/session"
	"aiops/internal/store"
)

func TestCreateModelProfileEncryptsKeyAndUpdatesAgent(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "12345678901234567890123456789012")
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ag := agent.New(nil, nil, session.NewMemoryStore())
	srv := NewServer(nil, nil, nil, st, ag, nil, 0, nil)
	raw := []byte(`{
		"name":"DeepSeek",
		"provider":"openai",
		"base_url":"https://api.deepseek.com",
		"model_name":"deepseek-chat",
		"api_key":"plain-secret",
		"max_completion_tokens":4096,
		"timeout_seconds":120,
		"temperature":0.1,
		"is_default":true,
		"enabled":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建模型失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "plain-secret") {
		t.Fatalf("响应不应泄露 API key: %s", rec.Body.String())
	}

	var body struct {
		Model struct {
			ID        uint   `json:"id"`
			ModelName string `json:"model_name"`
			HasAPIKey bool   `json:"has_api_key"`
			IsDefault bool   `json:"is_default"`
		} `json:"model"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Model.ID == 0 || body.Model.ModelName != "deepseek-chat" || !body.Model.HasAPIKey || !body.Model.IsDefault {
		t.Fatalf("响应模型信息不正确: %+v", body.Model)
	}

	defaultProfile, err := st.GetDefaultModelProfile()
	if err != nil {
		t.Fatalf("查询默认模型失败: %v", err)
	}
	if defaultProfile.EncryptedAPIKey == "" || defaultProfile.EncryptedAPIKey == "plain-secret" {
		t.Fatalf("API key 应加密保存: %+v", defaultProfile)
	}
	if got, want := ag.ActiveModelName(), "deepseek-chat"; got != want {
		t.Fatalf("默认模型创建后应立即注入 Agent: got %q want %q", got, want)
	}
}

func TestSetDefaultModelProfileUpdatesAgent(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "12345678901234567890123456789012")
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	first, err := st.CreateModelProfile(store.CreateModelProfileInput{
		Name:            "模型 A",
		Provider:        "openai",
		BaseURL:         "https://example.com/v1",
		ModelName:       "model-a",
		EncryptedAPIKey: mustEncryptForTest(t, "key-a"),
		IsDefault:       true,
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("创建模型 A 失败: %v", err)
	}
	second, err := st.CreateModelProfile(store.CreateModelProfileInput{
		Name:            "模型 B",
		Provider:        "openai",
		BaseURL:         "https://example.com/v1",
		ModelName:       "model-b",
		EncryptedAPIKey: mustEncryptForTest(t, "key-b"),
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("创建模型 B 失败: %v", err)
	}

	ag := agent.New(nil, nil, session.NewMemoryStore())
	srv := NewServer(nil, nil, nil, st, ag, nil, 0, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/models/"+strconvID(second.ID)+"/default", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("设置默认模型失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, want := ag.ActiveModelName(), "model-b"; got != want {
		t.Fatalf("默认模型切换后应立即注入 Agent: got %q want %q", got, want)
	}
	updatedFirst, err := st.GetModelProfile(first.ID)
	if err != nil {
		t.Fatalf("查询模型 A 失败: %v", err)
	}
	if updatedFirst.IsDefault {
		t.Fatal("模型 A 不应继续保持默认")
	}
}

func TestModelRuntimeStatusReportsRuntimeSource(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := NewServer(nil, nil, nil, st, agent.New(nil, nil, session.NewMemoryStore()), nil, 0, nil)
	srv.ApplyModelRuntime(ai.NewClient("https://api.deepseek.com", "key", "deepseek-v4-flash", 4096, 120, 0), ModelRuntimeStatus{
		Ready:     true,
		Source:    "runtime",
		Provider:  "deepseek",
		BaseURL:   "https://api.deepseek.com",
		ModelName: "deepseek-v4-flash",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/models/status", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询模型状态失败: code=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Current struct {
			Ready     bool   `json:"ready"`
			Source    string `json:"source"`
			Provider  string `json:"provider"`
			ModelName string `json:"model_name"`
			BaseURL   string `json:"base_url"`
		} `json:"current"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if !body.Current.Ready || body.Current.Source != "runtime" || body.Current.ModelName != "deepseek-v4-flash" {
		t.Fatalf("当前模型状态不正确: %+v", body.Current)
	}
}

func TestModelConnectionTestUsesSavedProfile(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "12345678901234567890123456789012")
	var sawAuth bool
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("请求路径不正确: %s", r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer model-key"; got == want {
			sawAuth = true
		}
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("解析模型请求失败: %v", err)
		}
		if req.Model != "test-model" {
			t.Fatalf("模型标识不正确: got %q want %q", req.Model, "test-model")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer modelServer.Close()

	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	profile, err := st.CreateModelProfile(store.CreateModelProfileInput{
		Name:                "测试模型",
		Provider:            "openai",
		BaseURL:             modelServer.URL,
		ModelName:           "test-model",
		EncryptedAPIKey:     mustEncryptForTest(t, "model-key"),
		MaxCompletionTokens: 16,
		TimeoutSeconds:      5,
		Enabled:             true,
	})
	if err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/models/"+strconvID(profile.ID)+"/test", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("测试连接失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !sawAuth {
		t.Fatal("测试连接没有使用保存的 API key")
	}
	if strings.Contains(rec.Body.String(), "model-key") {
		t.Fatalf("响应不应泄露 API key: %s", rec.Body.String())
	}
	var body struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if !body.OK || !strings.Contains(body.Message, "OK") {
		t.Fatalf("测试连接响应不正确: %+v", body)
	}
}

func TestListModelsDoesNotIncludeConfigModel(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "12345678901234567890123456789012")
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, err = st.CreateModelProfile(store.CreateModelProfileInput{
		Name:            "数据库模型",
		Provider:        "openai",
		BaseURL:         "https://example.com/v1",
		ModelName:       "db-model",
		EncryptedAPIKey: mustEncryptForTest(t, "db-key"),
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("创建数据库模型失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, agent.New(nil, nil, session.NewMemoryStore()), nil, 0, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询模型列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Count  int `json:"count"`
		Models []struct {
			Source    string `json:"source"`
			ReadOnly  bool   `json:"read_only"`
			IsDefault bool   `json:"is_default"`
			ModelName string `json:"model_name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析模型列表失败: %v", err)
	}
	if body.Count != 1 || len(body.Models) != 1 {
		t.Fatalf("模型列表数量不正确: %+v", body)
	}
	if body.Models[0].Source != "database" || body.Models[0].ReadOnly || body.Models[0].ModelName != "db-model" {
		t.Fatalf("模型列表应只包含数据库模型: %+v", body.Models[0])
	}
}

func TestDeletingLastDatabaseDefaultLeavesModelRuntimeUnconfigured(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "12345678901234567890123456789012")
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	profile, err := st.CreateModelProfile(store.CreateModelProfileInput{
		Name:            "数据库默认模型",
		Provider:        "openai",
		BaseURL:         "https://example.com/v1",
		ModelName:       "db-default",
		EncryptedAPIKey: mustEncryptForTest(t, "db-key"),
		IsDefault:       true,
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("创建数据库模型失败: %v", err)
	}

	ag := agent.New(nil, nil, session.NewMemoryStore())
	srv := NewServer(nil, nil, nil, st, ag, nil, 0, nil)
	if err := srv.applyModelProfile(profile); err != nil {
		t.Fatalf("应用数据库模型失败: %v", err)
	}
	if got, want := ag.ActiveModelName(), "db-default"; got != want {
		t.Fatalf("前置条件不正确: got %q want %q", got, want)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/models/"+strconvID(profile.ID), nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("删除数据库模型失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if ag.Ready() {
		t.Fatalf("删除最后一个数据库默认模型后不应继续保留模型客户端: %q", ag.ActiveModelName())
	}
	status := srv.CurrentModelRuntimeStatus()
	if status.Ready || status.Source != "none" {
		t.Fatalf("当前运行模型状态应变为未配置: %+v", status)
	}
}

func TestSyncDefaultModelPromotesFirstEnabledProfileWhenNoDefaultExists(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "12345678901234567890123456789012")
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	profile, err := st.CreateModelProfile(store.CreateModelProfileInput{
		Name:            "迁移后的模型",
		Provider:        "openai",
		BaseURL:         "https://example.com/v1",
		ModelName:       "migrated-model",
		EncryptedAPIKey: mustEncryptForTest(t, "db-key"),
		IsDefault:       true,
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("创建数据库模型失败: %v", err)
	}
	if err := st.ClearDefaultModelProfiles(); err != nil {
		t.Fatalf("清空默认模型失败: %v", err)
	}

	ag := agent.New(nil, nil, session.NewMemoryStore())
	srv := NewServer(nil, nil, nil, st, ag, nil, 0, nil)
	if err := srv.SyncDefaultModelFromStore(); err != nil {
		t.Fatalf("同步默认模型失败: %v", err)
	}
	if got, want := ag.ActiveModelName(), "migrated-model"; got != want {
		t.Fatalf("应自动启用第一条可用模型: got %q want %q", got, want)
	}
	updated, err := st.GetModelProfile(profile.ID)
	if err != nil {
		t.Fatalf("查询模型失败: %v", err)
	}
	if !updated.IsDefault {
		t.Fatal("第一条可用模型应被持久化为默认模型")
	}
}

func TestSyncDefaultModelReportsMissingSecretKeyInRuntimeStatus(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "12345678901234567890123456789012")
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	profile, err := st.CreateModelProfile(store.CreateModelProfileInput{
		Name:            "需要解密的模型",
		Provider:        "openai",
		BaseURL:         "https://example.com/v1",
		ModelName:       "encrypted-model",
		EncryptedAPIKey: mustEncryptForTest(t, "db-key"),
		IsDefault:       true,
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("创建数据库模型失败: %v", err)
	}

	t.Setenv("AIOPS_SECRET_KEY", "")
	srv := NewServer(nil, nil, nil, st, agent.New(nil, nil, session.NewMemoryStore()), nil, 0, nil)
	if err := srv.SyncDefaultModelFromStore(); err == nil {
		t.Fatal("缺少 AIOPS_SECRET_KEY 时同步默认模型应失败")
	}
	status := srv.CurrentModelRuntimeStatus()
	if status.Ready {
		t.Fatalf("解密失败时模型不应就绪: %+v", status)
	}
	if status.Source != "database" || status.ProfileID != profile.ID || status.ModelName != "encrypted-model" {
		t.Fatalf("解密失败状态应保留模型归属信息: %+v", status)
	}
	if !strings.Contains(status.LastError, "AIOPS_SECRET_KEY") {
		t.Fatalf("解密失败状态应带明确错误: %+v", status)
	}
}

func TestCreateModelRejectsWhenDatabaseModelLimitReached(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "12345678901234567890123456789012")
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for i := 0; i < 10; i++ {
		_, err := st.CreateModelProfile(store.CreateModelProfileInput{
			Name:            "数据库模型 " + strconv.Itoa(i+1),
			Provider:        "openai",
			BaseURL:         "https://example.com/v1",
			ModelName:       "db-model-" + strconv.Itoa(i+1),
			EncryptedAPIKey: mustEncryptForTest(t, "db-key"),
			Enabled:         true,
		})
		if err != nil {
			t.Fatalf("创建数据库模型失败: %v", err)
		}
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	raw := []byte(`{
		"name":"超限模型",
		"provider":"openai",
		"base_url":"https://example.com/v1",
		"model_name":"overflow-model",
		"api_key":"plain-secret",
		"enabled":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/models", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("达到模型数量上限后应拒绝新增: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "最多只能配置 10 个模型") {
		t.Fatalf("错误信息应说明数量上限: %s", rec.Body.String())
	}
}

func TestChatWithoutModelGuidesUserToAssistantConfig(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := NewServer(nil, nil, nil, st, agent.New(nil, nil, session.NewMemoryStore()), nil, 1, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader([]byte(`{"session_id":"s","message":"你好","cluster_id":1}`)))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("未配置模型时应返回 503: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "请前往智能助手配置你的专属运维助手") {
		t.Fatalf("错误信息应引导用户配置智能助手: %s", rec.Body.String())
	}
}

func mustEncryptForTest(t *testing.T, value string) string {
	t.Helper()
	encrypted, err := encryptModelAPIKey(value)
	if err != nil {
		t.Fatalf("加密测试 API key 失败: %v", err)
	}
	return encrypted
}

func strconvID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
