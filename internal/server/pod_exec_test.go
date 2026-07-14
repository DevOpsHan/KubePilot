package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/store"
)

func TestCheckWebSocketOriginRequiresExactOrigin(t *testing.T) {
	tests := []struct {
		name           string
		origin         string
		host           string
		forwardedProto string
		tls            bool
		want           bool
	}{
		{name: "无 Origin 的非浏览器客户端", host: "console.example.com", want: true},
		{name: "HTTP 同源", origin: "http://console.example.com", host: "console.example.com", want: true},
		{name: "HTTP 默认端口", origin: "http://console.example.com:80", host: "console.example.com", want: true},
		{name: "HTTPS 代理同源", origin: "https://console.example.com", host: "console.example.com", forwardedProto: "https", want: true},
		{name: "多层 HTTPS 代理取首个协议", origin: "https://console.example.com", host: "console.example.com", forwardedProto: "https, http", want: true},
		{name: "HTTPS 默认端口", origin: "https://console.example.com:443", host: "console.example.com", forwardedProto: "https", want: true},
		{name: "TLS 同源", origin: "https://console.example.com", host: "console.example.com", tls: true, want: true},
		{name: "端口不同", origin: "http://console.example.com:5174", host: "console.example.com:8080", want: false},
		{name: "协议不同", origin: "http://console.example.com", host: "console.example.com", forwardedProto: "https", want: false},
		{name: "本地端口不同", origin: "http://localhost:5174", host: "localhost:8080", want: false},
		{name: "主机不同", origin: "https://evil.example.com", host: "console.example.com", forwardedProto: "https", want: false},
		{name: "null Origin", origin: "null", host: "console.example.com", want: false},
		{name: "非法 Origin", origin: "://bad", host: "console.example.com", want: false},
		{name: "包含路径", origin: "https://console.example.com/path", host: "console.example.com", forwardedProto: "https", want: false},
		{name: "IPv6 同源", origin: "http://[::1]:5174", host: "[::1]:5174", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := "http://" + tt.host + "/api/ws"
			if tt.tls {
				target = "https://" + tt.host + "/api/ws"
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwardedProto)
			}
			if got := checkWebSocketOrigin(req); got != tt.want {
				t.Fatalf("Origin 校验结果不正确: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestHandleListPodContainers(t *testing.T) {
	st := openServerTestStore(t)
	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "容器列表测试集群",
		Slug: "pod-containers-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	kubeClient := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "init-db"}},
			Containers:     []corev1.Container{{Name: "app"}, {Name: "sidecar"}},
			EphemeralContainers: []corev1.EphemeralContainer{
				{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debugger"}},
			},
		},
	})
	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{
		ClusterID: cluster.ID,
		Collector: collector.New(kubeClient),
	}); err != nil {
		t.Fatalf("注册测试运行时失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, manager)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/clusters/%d/pods/default/web/containers", cluster.ID), nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("容器列表接口应返回 200: got %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Containers []string `json:"containers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析容器列表失败: %v", err)
	}
	// 普通容器优先，确保终端默认值不会落到已结束的 init 容器。
	want := []string{"app", "sidecar", "init-db", "debugger"}
	if strings.Join(body.Containers, ",") != strings.Join(want, ",") {
		t.Fatalf("容器列表不正确: got %v want %v", body.Containers, want)
	}
}

func TestHandlePodLogsValidatesTailAndUsesBoundedDefault(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "pod-log-tail")
	user := createServerTestUser(t, st, "pod-log-admin", "secret", store.UserRoleGlobalAdmin)
	seenTail := make(chan string, 1)
	logServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenTail <- r.URL.Query().Get("tailLines")
		_, _ = w.Write([]byte("ready\n"))
	}))
	t.Cleanup(logServer.Close)
	cfg := &rest.Config{Host: logServer.URL}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("创建日志测试客户端失败: %v", err)
	}
	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{
		ClusterID: cluster.ID,
		Collector: collector.NewWithMetricsDynamicAndConfig(clientset, nil, nil, cfg),
	}); err != nil {
		t.Fatalf("注册日志测试运行时失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, manager)

	for _, tail := range []string{"bad", "-1", "5001"} {
		t.Run("tail="+tail, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/api/clusters/%d/pods/default/web/logs?tail=%s", cluster.ID, tail), nil)
			attachServerTestSession(t, st, req, user.ID)
			rec := httptest.NewRecorder()
			srv.Echo().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("非法 tail 必须返回 400: tail=%s code=%d body=%s", tail, rec.Code, rec.Body.String())
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/clusters/%d/pods/default/web/logs", cluster.ID), nil)
	attachServerTestSession(t, st, req, user.ID)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("默认日志请求应成功: code=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case tail := <-seenTail:
		if tail != "500" {
			t.Fatalf("缺省 tail 应限制为 500 行，实际 %q", tail)
		}
	case <-time.After(time.Second):
		t.Fatal("日志后端未收到请求")
	}
}

func TestServePodLogsOnceAppliesTimeAndByteLimits(t *testing.T) {
	t.Run("输出字节上限", func(t *testing.T) {
		col := newPodLogHTTPCollector(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("0123456789"))
		}))
		e := echo.New()
		rec := httptest.NewRecorder()
		ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/logs", nil), rec)
		srv := &Server{}
		err := srv.servePodLogsOnceWithLimits(ctx, col, 0, "default", "web",
			collector.LogStreamOptions{}, "tester", "default/web", time.Second, 8)
		if err != nil {
			t.Fatalf("读取受限日志失败: %v", err)
		}
		if rec.Code != http.StatusOK || rec.Header().Get(podLogTruncatedHeader) != "true" {
			t.Fatalf("超长日志应返回截断标记: code=%d headers=%v body=%q", rec.Code, rec.Header(), rec.Body.String())
		}
		if !strings.HasPrefix(rec.Body.String(), "01234567") || !strings.Contains(rec.Body.String(), "truncated") {
			t.Fatalf("日志截断内容不正确: %q", rec.Body.String())
		}
	})

	t.Run("读取超时", func(t *testing.T) {
		requestDone := make(chan struct{})
		col := newPodLogHTTPCollector(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(requestDone)
			_, _ = w.Write([]byte("ready\n"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		e := echo.New()
		rec := httptest.NewRecorder()
		ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/logs", nil), rec)
		srv := &Server{}
		started := time.Now()
		err := srv.servePodLogsOnceWithLimits(ctx, col, 0, "default", "web",
			collector.LogStreamOptions{}, "tester", "default/web", 25*time.Millisecond, 1024)
		if err != nil {
			t.Fatalf("超时响应写入失败: %v", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("一次性日志读取未按时取消: %s", elapsed)
		}
		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("一次性日志读取超时应返回 504: code=%d body=%s", rec.Code, rec.Body.String())
		}
		select {
		case <-requestDone:
		case <-time.After(time.Second):
			t.Fatal("读取超时后 Kubernetes 日志请求未被取消")
		}
	})
}

func newPodLogHTTPCollector(t *testing.T, handler http.Handler) *collector.Collector {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg := &rest.Config{Host: server.URL}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("创建日志测试客户端失败: %v", err)
	}
	return collector.NewWithMetricsDynamicAndConfig(clientset, nil, nil, cfg)
}

func TestWebSocketSessionContextStopsAtMaximumDuration(t *testing.T) {
	st := openServerTestStore(t)
	user := createServerTestUser(t, st, "ws-max-duration", "secret", store.UserRoleGlobalAdmin)
	token := createPodExecTestSession(t, st, user.ID)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	ctx, cancel := srv.newWebSocketSessionContext(
		context.Background(), token, 0, store.PermissionClusterExec,
		webSocketSessionLimits{MaxDuration: 30 * time.Millisecond, CheckInterval: time.Second},
	)
	defer cancel(nil)

	select {
	case <-ctx.Done():
		if cause := context.Cause(ctx); cause == nil || !strings.Contains(cause.Error(), "最大时长") {
			t.Fatalf("会话应因达到最大时长结束: %v", cause)
		}
	case <-time.After(time.Second):
		t.Fatal("WebSocket 会话未在最大时长到达后取消")
	}
}

func TestWebSocketSessionContextStopsAfterSessionRevoked(t *testing.T) {
	st := openServerTestStore(t)
	user := createServerTestUser(t, st, "ws-revoked", "secret", store.UserRoleGlobalAdmin)
	token := createPodExecTestSession(t, st, user.ID)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	ctx, cancel := srv.newWebSocketSessionContext(
		context.Background(), token, 0, store.PermissionClusterExec,
		webSocketSessionLimits{MaxDuration: time.Second, CheckInterval: 10 * time.Millisecond},
	)
	defer cancel(nil)
	if err := st.RevokeAuthSession(token); err != nil {
		t.Fatalf("撤销测试会话失败: %v", err)
	}

	select {
	case <-ctx.Done():
		if cause := context.Cause(ctx); cause == nil || !strings.Contains(cause.Error(), "登录会话") {
			t.Fatalf("会话撤销后的取消原因不正确: %v", cause)
		}
	case <-time.After(time.Second):
		t.Fatal("登录会话撤销后 WebSocket 未及时取消")
	}
}

func TestWebSocketSessionContextStopsAfterClusterGrantRemoved(t *testing.T) {
	st := openServerTestStore(t)
	clusterA := createServerTestCluster(t, st, "ws-grant-a")
	clusterB := createServerTestCluster(t, st, "ws-grant-b")
	user, err := st.CreateUser(store.CreateUserInput{
		Username: "ws-grant-user", Password: "secret", Role: store.UserRoleClusterAdmin,
		Status: store.UserStatusActive, PasswordChangeRequired: boolPtr(false), ClusterIDs: []uint{clusterA.ID},
	})
	if err != nil {
		t.Fatalf("创建集群管理员失败: %v", err)
	}
	token := createPodExecTestSession(t, st, user.ID)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)

	ctx, cancel := srv.newWebSocketSessionContext(
		context.Background(), token, clusterA.ID, store.PermissionClusterExec,
		webSocketSessionLimits{MaxDuration: time.Second, CheckInterval: 10 * time.Millisecond},
	)
	defer cancel(nil)
	newGrants := []uint{clusterB.ID}
	if _, err := st.UpdateUser(user.ID, store.UpdateUserInput{ClusterIDs: &newGrants}); err != nil {
		t.Fatalf("移除目标集群授权失败: %v", err)
	}
	if _, err := st.GetUserBySessionToken(token); err != nil {
		t.Fatalf("仅修改集群授权不应依赖会话撤销才能生效: %v", err)
	}

	select {
	case <-ctx.Done():
		if cause := context.Cause(ctx); cause == nil || !strings.Contains(cause.Error(), "集群授权") {
			t.Fatalf("集群授权移除后的取消原因不正确: %v", cause)
		}
	case <-time.After(time.Second):
		t.Fatal("集群授权移除后 WebSocket 未及时取消")
	}
}

func TestPodExecRejectsClusterWithoutGrant(t *testing.T) {
	st := openServerTestStore(t)
	allowedCluster := createServerTestCluster(t, st, "pod-exec-allowed")
	deniedCluster := createServerTestCluster(t, st, "pod-exec-denied")
	user, err := st.CreateUser(store.CreateUserInput{
		Username: "pod-exec-scoped", Password: "secret", Role: store.UserRoleClusterAdmin,
		Status: store.UserStatusActive, PasswordChangeRequired: boolPtr(false), ClusterIDs: []uint{allowedCluster.ID},
	})
	if err != nil {
		t.Fatalf("创建集群管理员失败: %v", err)
	}
	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{
		ClusterID: deniedCluster.ID,
		Collector: collector.New(fake.NewSimpleClientset()),
	}); err != nil {
		t.Fatalf("注册未授权集群运行时失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, manager)
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/clusters/%d/pods/default/web/exec", deniedCluster.ID), nil)
	attachServerTestSession(t, st, req, user.ID)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("没有目标集群授权时必须在 WebSocket 升级前拒绝: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestServePodExecWritesLifecycleAudits(t *testing.T) {
	st := openServerTestStore(t)
	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "终端审计测试集群",
		Slug: "pod-exec-audit-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	user := createServerTestUser(t, st, "ws-audit", "secret", store.UserRoleGlobalAdmin)
	token := createPodExecTestSession(t, st, user.ID)
	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, nil)
	executor := podExecutorFunc(func(context.Context, string, string, collector.ExecOptions) error {
		return nil
	})

	e := echo.New()
	done := make(chan struct{})
	e.GET("/exec", func(c echo.Context) error {
		defer close(done)
		c.Set(currentSessionTokenKey, token)
		return srv.servePodExec(c, executor, cluster.ID, "default", "web", "app",
			[]string{"/bin/sh"}, "ws-audit", "default/web:app", "命令: /bin/sh")
	})
	httpServer := httptest.NewServer(e)
	defer httpServer.Close()
	ws := dialPodExecTestWebSocket(t, httpServer.URL+"/exec")
	defer ws.Close()
	for {
		_, data, readErr := ws.ReadMessage()
		if readErr != nil || strings.Contains(string(data), `"type":"exit"`) {
			break
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("终端处理器未及时结束")
	}

	audits, err := st.ListAudits(cluster.ID, 10)
	if err != nil {
		t.Fatalf("查询终端审计失败: %v", err)
	}
	if len(audits) != 2 {
		t.Fatalf("成功终端会话应记录开始和结束两条审计: %+v", audits)
	}
	if audits[1].Result != "会话开始" || audits[0].Result != "会话正常结束" {
		t.Fatalf("终端生命周期审计不正确: %+v", audits)
	}
}

func TestServePodExecReleasesBlockedInputLoop(t *testing.T) {
	st := openServerTestStore(t)
	user := createServerTestUser(t, st, "ws-pipe", "secret", store.UserRoleGlobalAdmin)
	token := createPodExecTestSession(t, st, user.ID)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	executor := podExecutorFunc(func(context.Context, string, string, collector.ExecOptions) error {
		close(started)
		<-release
		return nil
	})

	e := echo.New()
	done := make(chan struct{})
	e.GET("/exec", func(c echo.Context) error {
		defer close(done)
		c.Set(currentSessionTokenKey, token)
		return srv.servePodExec(c, executor, 0, "default", "web", "",
			[]string{"/bin/sh"}, "ws-pipe", "default/web", "命令: /bin/sh")
	})
	httpServer := httptest.NewServer(e)
	defer httpServer.Close()
	ws := dialPodExecTestWebSocket(t, httpServer.URL+"/exec")
	defer ws.Close()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("测试执行器未启动")
	}
	if err := ws.WriteJSON(execClientMessage{Type: "stdin", Data: strings.Repeat("x", 64*1024)}); err != nil {
		t.Fatalf("写入测试 stdin 失败: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	close(release)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Exec 返回后阻塞的 stdin 写入协程未被回收")
	}
}

func TestServePodExecRejectsOversizedClientFrame(t *testing.T) {
	st := openServerTestStore(t)
	user := createServerTestUser(t, st, "ws-read-limit", "secret", store.UserRoleGlobalAdmin)
	token := createPodExecTestSession(t, st, user.ID)
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)
	executor := podExecutorFunc(func(ctx context.Context, _ string, _ string, _ collector.ExecOptions) error {
		<-ctx.Done()
		return ctx.Err()
	})

	e := echo.New()
	done := make(chan struct{})
	e.GET("/exec", func(c echo.Context) error {
		defer close(done)
		c.Set(currentSessionTokenKey, token)
		return srv.servePodExec(c, executor, 0, "default", "web", "",
			[]string{"/bin/sh"}, "ws-read-limit", "default/web", "命令: /bin/sh")
	})
	httpServer := httptest.NewServer(e)
	defer httpServer.Close()
	ws := dialPodExecTestWebSocket(t, httpServer.URL+"/exec")
	oversized := bytes.Repeat([]byte{'x'}, (1<<20)+1024)
	if err := ws.WriteMessage(websocket.TextMessage, oversized); err != nil {
		_ = ws.Close()
		t.Fatalf("发送超大测试帧失败: %v", err)
	}

	select {
	case <-done:
		_ = ws.Close()
	case <-time.After(time.Second):
		_ = ws.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatal("超大客户端帧未触发连接收尾，存在无界内存占用风险")
	}
}

type podExecutorFunc func(context.Context, string, string, collector.ExecOptions) error

func (f podExecutorFunc) ExecInPod(ctx context.Context, namespace, pod string, opts collector.ExecOptions) error {
	return f(ctx, namespace, pod, opts)
}

func createPodExecTestSession(t *testing.T, st *store.Store, userID uint) string {
	t.Helper()
	user, err := st.GetUser(userID)
	if err != nil {
		t.Fatalf("读取测试会话用户失败: %v", err)
	}
	token, _, err := st.CreateAuthSession(user, time.Hour)
	if err != nil {
		t.Fatalf("创建测试会话失败: %v", err)
	}
	return token
}

func dialPodExecTestWebSocket(t *testing.T, rawURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(rawURL, "http")
	header := http.Header{}
	header.Set("Origin", rawURL[:strings.LastIndex(rawURL, "/")])
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if response != nil {
			t.Fatalf("建立测试 WebSocket 失败: %v status=%d", err, response.StatusCode)
		}
		t.Fatalf("建立测试 WebSocket 失败: %v", err)
	}
	return conn
}
