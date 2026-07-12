package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestHandleHealthIdentifiesKubePilotCore(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	if err := (&Server{}).handleHealth(e.NewContext(req, rec)); err != nil {
		t.Fatalf("健康检查返回错误: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("健康检查状态码错误: got=%d want=%d", rec.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析健康检查响应失败: %v", err)
	}
	if got := body["service"]; got != "kubepilot-core" {
		t.Fatalf("健康检查服务标识错误: got=%q want=%q", got, "kubepilot-core")
	}
}
