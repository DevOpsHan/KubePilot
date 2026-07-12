package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aiops/internal/config"
)

// TestDispatcherSendsToBothChannels 用 httptest 验证钉钉/企微都收到 markdown 消息,且钉钉带加签参数。
func TestDispatcherSendsToBothChannels(t *testing.T) {
	var dingBody, weBody map[string]any
	var dingQuery string
	dingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dingQuery = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &dingBody)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer dingSrv.Close()
	weSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &weBody)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer weSrv.Close()

	d := NewFromConfig(config.NotificationConfig{
		Enabled:     true,
		MinSeverity: "warning",
		DingTalk:    config.DingTalkNotifyConfig{WebhookURL: dingSrv.URL, Secret: "SECtest"},
		WeCom:       config.WeComNotifyConfig{WebhookURL: weSrv.URL},
	}, nil)
	if d == nil || !d.Enabled() {
		t.Fatal("期望构造出可用 dispatcher")
	}
	d.Dispatch(context.Background(), Message{Title: "标题", Text: "### 内容"})

	if dingBody["msgtype"] != "markdown" {
		t.Fatalf("钉钉 msgtype 期望 markdown,实际 %v", dingBody["msgtype"])
	}
	if !strings.Contains(dingQuery, "sign=") || !strings.Contains(dingQuery, "timestamp=") {
		t.Fatalf("钉钉加签参数缺失,query=%s", dingQuery)
	}
	if weBody["msgtype"] != "markdown" {
		t.Fatalf("企微 msgtype 期望 markdown,实际 %v", weBody["msgtype"])
	}
}

// TestAllowSeverityAndDisabled 验证启用判定与级别阈值过滤。
func TestAllowSeverityAndDisabled(t *testing.T) {
	if NewFromConfig(config.NotificationConfig{Enabled: false}, nil) != nil {
		t.Fatal("未启用应返回 nil")
	}
	if NewFromConfig(config.NotificationConfig{Enabled: true}, nil) != nil {
		t.Fatal("启用但无渠道应返回 nil")
	}
	d := NewFromConfig(config.NotificationConfig{
		Enabled:     true,
		MinSeverity: "critical",
		WeCom:       config.WeComNotifyConfig{WebhookURL: "http://example.com"},
	}, nil)
	if d.AllowSeverity("warning") {
		t.Fatal("critical 阈值不应放行 warning")
	}
	if !d.AllowSeverity("critical") {
		t.Fatal("critical 阈值应放行 critical")
	}
}
