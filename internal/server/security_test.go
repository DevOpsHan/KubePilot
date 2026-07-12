package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityStatusReportsSecretKeyReadinessWithoutExposingValue(t *testing.T) {
	secret := "12345678901234567890123456789012"
	t.Setenv("AIOPS_SECRET_KEY", secret)

	srv := NewServer(nil, nil, nil, nil, nil, nil, 0, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/security/status", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询安全状态失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("安全状态接口不应泄露密钥值: %s", rec.Body.String())
	}
	var body struct {
		SecretKeyReady bool `json:"secret_key_ready"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析安全状态失败: %v", err)
	}
	if !body.SecretKeyReady {
		t.Fatalf("32 字节 AIOPS_SECRET_KEY 应被识别为可用: %+v", body)
	}
}

func TestSecurityStatusReportsMissingSecretKey(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "")

	srv := NewServer(nil, nil, nil, nil, nil, nil, 0, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/security/status", nil)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询安全状态失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		SecretKeyReady bool   `json:"secret_key_ready"`
		Message        string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析安全状态失败: %v", err)
	}
	if body.SecretKeyReady {
		t.Fatalf("空 AIOPS_SECRET_KEY 不应被识别为可用: %+v", body)
	}
	if !strings.Contains(body.Message, "AIOPS_SECRET_KEY") {
		t.Fatalf("缺少密钥时应返回可读提示: %+v", body)
	}
}
