package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"k8s.io/client-go/tools/remotecommand"

	"aiops/internal/collector"
	"aiops/internal/store"
)

const (
	webSocketSessionMaxDuration = 30 * time.Minute
	webSocketSessionCheckPeriod = 15 * time.Second
	webSocketWriteTimeout       = 10 * time.Second
	webSocketMaxClientFrameSize = 1 << 20
	podLogDefaultTailLines      = int64(500)
	podLogMaxTailLines          = int64(5000)
	podLogOnceTimeout           = 30 * time.Second
	podLogOnceMaxBytes          = int64(4 << 20)
	podLogTruncatedHeader       = "X-KubePilot-Log-Truncated"
	podLogTruncatedMarker       = "\n…(日志过长，已截断 / log output truncated)…\n"
)

var defaultWebSocketSessionLimits = webSocketSessionLimits{
	MaxDuration:   webSocketSessionMaxDuration,
	CheckInterval: webSocketSessionCheckPeriod,
}

// podExecUpgrader 负责把 Pod 终端 / 日志跟随请求从 HTTP 升级为 WebSocket。
// CheckOrigin 仅放行严格同源请求,避免跨站脚本借用浏览器 cookie 发起 exec。
var podExecUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     checkWebSocketOrigin,
}

type webSocketSessionLimits struct {
	MaxDuration   time.Duration
	CheckInterval time.Duration
}

// checkWebSocketOrigin 校验 WebSocket 握手来源:无 Origin 的非浏览器客户端放行;
// 浏览器请求必须与代理保留的请求协议、主机和有效端口完全一致。
func checkWebSocketOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	originKey, err := canonicalOrigin(parsed.Scheme, parsed.Host)
	if err != nil {
		return false
	}
	requestKey, err := canonicalOrigin(requestScheme(r), r.Host)
	if err != nil {
		return false
	}
	return originKey == requestKey
}

func requestScheme(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		return strings.ToLower(strings.TrimSpace(forwarded))
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func canonicalOrigin(scheme, hostPort string) (string, error) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("不支持的 Origin 协议: %s", scheme)
	}
	parsed, err := url.Parse(scheme + "://" + strings.TrimSpace(hostPort))
	if err != nil || parsed.Hostname() == "" || parsed.Path != "" || parsed.User != nil {
		return "", fmt.Errorf("无效的 Origin 主机: %s", hostPort)
	}
	port := parsed.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(parsed.Hostname()), port), nil
}

// execClientMessage 是前端通过 WebSocket 发给服务端的控制帧(文本 JSON)。
type execClientMessage struct {
	Type string `json:"type"`           // stdin | resize
	Data string `json:"data,omitempty"` // stdin 数据(UTF-8 文本)
	Cols uint16 `json:"cols,omitempty"` // resize:列数
	Rows uint16 `json:"rows,omitempty"` // resize:行数
}

// wsBinaryWriter 把 exec 的 stdout/stderr 原始字节以二进制帧写回 WebSocket。
// gorilla 的写操作非并发安全,用 mutex 串行化。
type wsBinaryWriter struct {
	conn *websocket.Conn
	mu   *sync.Mutex
}

func (w *wsBinaryWriter) Write(p []byte) (int, error) {
	if err := writeWebSocketMessage(w.conn, w.mu, websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func writeWebSocketMessage(conn *websocket.Conn, mu *sync.Mutex, messageType int, data []byte) error {
	mu.Lock()
	defer mu.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(webSocketWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteMessage(messageType, data)
}

// terminalSizeQueue 实现 remotecommand.TerminalSizeQueue,把前端 resize 帧转成尺寸事件。
type terminalSizeQueue struct {
	ch chan remotecommand.TerminalSize
}

func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-q.ch
	if !ok {
		return nil
	}
	return &size
}

// podExecutor 抽象 Pod exec 能力,让 WebSocket 生命周期可以脱离真实集群做回归测试。
type podExecutor interface {
	ExecInPod(context.Context, string, string, collector.ExecOptions) error
}

// newWebSocketSessionContext 为长连接设置最大时长,并周期复查登录会话与授权。
func (s *Server) newWebSocketSessionContext(parent context.Context, token string, clusterID uint, permission string, limits webSocketSessionLimits) (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	if limits.MaxDuration <= 0 {
		limits.MaxDuration = webSocketSessionMaxDuration
	}
	if limits.CheckInterval <= 0 {
		limits.CheckInterval = webSocketSessionCheckPeriod
	}
	go func() {
		timer := time.NewTimer(limits.MaxDuration)
		defer timer.Stop()
		ticker := time.NewTicker(limits.CheckInterval)
		defer ticker.Stop()

		if err := s.validateWebSocketSession(token, clusterID, permission); err != nil {
			cancel(err)
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				cancel(fmt.Errorf("WebSocket 会话已达到最大时长"))
				return
			case <-ticker.C:
				if err := s.validateWebSocketSession(token, clusterID, permission); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	return ctx, cancel
}

func (s *Server) validateWebSocketSession(token string, clusterID uint, permission string) error {
	if s.store == nil {
		return fmt.Errorf("登录会话校验失败:数据库未就绪")
	}
	user, err := s.store.GetUserBySessionToken(token)
	if err != nil {
		return fmt.Errorf("登录会话已失效: %w", err)
	}
	if !store.RoleHasPermission(user.Role, permission) {
		return fmt.Errorf("登录用户已失去当前操作权限")
	}
	if user.Role == store.UserRoleGlobalAdmin {
		return nil
	}
	allowed, err := s.store.UserHasClusterAccess(user.ID, clusterID)
	if err != nil {
		return fmt.Errorf("复查集群授权失败: %w", err)
	}
	if !allowed {
		return fmt.Errorf("登录用户已失去目标集群授权")
	}
	return nil
}

func webSocketSessionResult(ctx context.Context, fallback string) string {
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, context.Canceled) {
		return fallback
	}
	return "会话终止: " + cause.Error()
}

// handleListPodContainers 返回指定 Pod 的容器名列表,供前端选择目标容器。
func (s *Server) handleListPodContainers(c echo.Context) error {
	clusterID, err := clusterIDFromPath(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionViewClusters, clusterID); err != nil {
		return err
	}
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	names, err := runtime.Collector.ListPodContainers(c.Request().Context(), c.Param("namespace"), c.Param("name"))
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"containers": names})
}

// handlePodLogs 查看 Pod 日志。follow=false 时返回一次性文本;follow=true 时升级为
// WebSocket 持续推送日志行,客户端断开即停止。
func (s *Server) handlePodLogs(c echo.Context) error {
	clusterID, err := clusterIDFromPath(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionViewClusters, clusterID); err != nil {
		return err
	}
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}

	namespace := c.Param("namespace")
	pod := c.Param("name")
	opts := collector.LogStreamOptions{
		Container:  c.QueryParam("container"),
		Follow:     c.QueryParam("follow") == "true",
		Timestamps: c.QueryParam("timestamps") == "true",
		Previous:   c.QueryParam("previous") == "true",
	}
	tailLines := podLogDefaultTailLines
	if tail := strings.TrimSpace(c.QueryParam("tail")); tail != "" {
		n, convErr := strconv.ParseInt(tail, 10, 64)
		if convErr != nil || n < 0 || n > podLogMaxTailLines {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("tail 必须是 0 到 %d 之间的整数", podLogMaxTailLines),
			})
		}
		tailLines = n
	}
	opts.TailLines = &tailLines
	operator := currentUsername(c)
	target := namespace + "/" + pod
	if opts.Container != "" {
		target += ":" + opts.Container
	}

	if !opts.Follow {
		return s.servePodLogsOnce(c, runtime.Collector, clusterID, namespace, pod, opts, operator, target)
	}
	return s.servePodLogsFollow(c, runtime.Collector, clusterID, namespace, pod, opts, operator, target)
}

// servePodLogsOnce 一次性读取日志并以纯文本返回。
func (s *Server) servePodLogsOnce(c echo.Context, col *collector.Collector, clusterID uint, namespace, pod string, opts collector.LogStreamOptions, operator, target string) error {
	return s.servePodLogsOnceWithLimits(
		c, col, clusterID, namespace, pod, opts, operator, target,
		podLogOnceTimeout, podLogOnceMaxBytes,
	)
}

func (s *Server) servePodLogsOnceWithLimits(c echo.Context, col *collector.Collector, clusterID uint, namespace, pod string, opts collector.LogStreamOptions, operator, target string, timeout time.Duration, maxBytes int64) error {
	if timeout <= 0 {
		timeout = podLogOnceTimeout
	}
	if maxBytes <= 0 {
		maxBytes = podLogOnceMaxBytes
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), timeout)
	defer cancel()
	opts.Follow = false
	s.auditPodAccess(clusterID, "pod:logs", operator, target, "查看日志", "开始")
	stream, err := col.StreamPodLogs(ctx, namespace, pod, opts)
	if err != nil {
		result := "建立日志流失败: " + err.Error()
		status := http.StatusServiceUnavailable
		message := err.Error()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result = fmt.Sprintf("建立日志流超时:超过 %s", timeout)
			status = http.StatusGatewayTimeout
			message = fmt.Sprintf("建立 Pod 日志流超过 %s 的安全上限", timeout)
		}
		s.auditPodAccess(clusterID, "pod:logs", operator, target, "查看日志", result)
		return c.JSON(status, map[string]string{"error": message})
	}
	defer stream.Close()
	data, readErr := io.ReadAll(io.LimitReader(stream, maxBytes+1))
	if readErr != nil {
		result := "读取中断: " + readErr.Error()
		status := http.StatusServiceUnavailable
		message := readErr.Error()
		if errors.Is(readErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result = fmt.Sprintf("读取超时:超过 %s", timeout)
			status = http.StatusGatewayTimeout
			message = fmt.Sprintf("读取 Pod 日志超过 %s 的安全上限", timeout)
		}
		s.auditPodAccess(clusterID, "pod:logs", operator, target, "查看日志", result)
		return c.JSON(status, map[string]string{"error": message})
	}

	result := "完成"
	if int64(len(data)) > maxBytes {
		data = append(data[:maxBytes], []byte(podLogTruncatedMarker)...)
		c.Response().Header().Set(podLogTruncatedHeader, "true")
		result = fmt.Sprintf("完成:输出超过 %d 字节已截断", maxBytes)
	}
	writeErr := c.Blob(http.StatusOK, "text/plain; charset=utf-8", data)
	if writeErr != nil {
		result = "响应写入失败: " + writeErr.Error()
	}
	s.auditPodAccess(clusterID, "pod:logs", operator, target, "查看日志", result)
	return writeErr
}

// servePodLogsFollow 升级 WebSocket,持续把日志推送给前端,直到日志结束或客户端断开。
func (s *Server) servePodLogsFollow(c echo.Context, col *collector.Collector, clusterID uint, namespace, pod string, opts collector.LogStreamOptions, operator, target string) error {
	// 在访问 Kubernetes API 前先拒绝跨站握手，避免攻击者借浏览器 Cookie
	// 触发后端创建无用的日志流连接。
	if !checkWebSocketOrigin(c.Request()) {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "WebSocket Origin 与请求来源不一致"})
	}
	ctx, cancel := s.newWebSocketSessionContext(
		c.Request().Context(), currentSessionToken(c), clusterID, store.PermissionViewClusters,
		defaultWebSocketSessionLimits,
	)
	defer cancel(nil)

	stream, err := col.StreamPodLogs(ctx, namespace, pod, opts)
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	}
	defer stream.Close()

	ws, err := podExecUpgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return nil // 升级失败时 upgrader 已写响应
	}
	defer ws.Close()
	ws.SetReadLimit(webSocketMaxClientFrameSize)
	s.auditPodAccess(clusterID, "pod:logs", operator, target, "查看日志(跟随)", "会话开始")

	var writeMu sync.Mutex
	readDone := make(chan struct{})
	// 读协程:感知客户端关闭,触发 cancel 停止日志流。
	go func() {
		defer close(readDone)
		for {
			if _, _, readErr := ws.ReadMessage(); readErr != nil {
				cancel(nil)
				return
			}
		}
	}()

	buf := make([]byte, 4096)
	for {
		n, readErr := stream.Read(buf)
		if n > 0 {
			writeErr := writeWebSocketMessage(ws, &writeMu, websocket.BinaryMessage, buf[:n])
			if writeErr != nil {
				break
			}
		}
		if readErr != nil {
			break
		}
	}
	result := webSocketSessionResult(ctx, "会话结束")
	cancel(nil)
	_ = stream.Close()
	_ = ws.Close()
	<-readDone
	s.auditPodAccess(clusterID, "pod:logs", operator, target, "查看日志(跟随)", result)
	return nil
}

// handlePodExec 在 Pod 容器内开交互式终端(WebSocket)。需要 clusters:exec 权限。
func (s *Server) handlePodExec(c echo.Context) error {
	clusterID, err := clusterIDFromPath(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	// exec 是高危写操作:强制 clusters:exec 权限 + 该集群访问权。
	if err := s.requireClusterPermission(c, store.PermissionClusterExec, clusterID); err != nil {
		return err
	}
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}

	namespace := c.Param("namespace")
	pod := c.Param("name")
	container := c.QueryParam("container")
	command := c.QueryParams()["command"]
	if len(command) == 0 {
		command = []string{"/bin/sh"}
	}
	operator := currentUsername(c)
	target := namespace + "/" + pod
	if container != "" {
		target += ":" + container
	}
	detail := "命令: " + strings.Join(command, " ")
	return s.servePodExec(c, runtime.Collector, clusterID, namespace, pod, container, command, operator, target, detail)
}

// servePodExec 管理一次已授权终端的 WebSocket、会话复查、输入输出与审计生命周期。
func (s *Server) servePodExec(c echo.Context, executor podExecutor, clusterID uint, namespace, pod, container string, command []string, operator, target, detail string) error {
	ws, err := podExecUpgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return nil
	}
	defer ws.Close()
	ws.SetReadLimit(webSocketMaxClientFrameSize)

	ctx, cancel := s.newWebSocketSessionContext(
		c.Request().Context(), currentSessionToken(c), clusterID, store.PermissionClusterExec,
		defaultWebSocketSessionLimits,
	)
	defer cancel(nil)

	stdinR, stdinW := io.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()
	resizeQueue := &terminalSizeQueue{ch: make(chan remotecommand.TerminalSize, 4)}
	var writeMu sync.Mutex
	var closeResizeOnce sync.Once
	closeResize := func() { closeResizeOnce.Do(func() { close(resizeQueue.ch) }) }
	readDone := make(chan struct{})

	// 读协程:解析前端控制帧(stdin / resize),客户端断开时收尾。
	go func() {
		defer func() {
			_ = stdinW.Close()
			closeResize()
			cancel(nil)
			close(readDone)
		}()
		for {
			_, data, readErr := ws.ReadMessage()
			if readErr != nil {
				return
			}
			var msg execClientMessage
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "stdin":
				if _, writeErr := stdinW.Write([]byte(msg.Data)); writeErr != nil {
					return
				}
			case "resize":
				select {
				case resizeQueue.ch <- remotecommand.TerminalSize{Width: msg.Cols, Height: msg.Rows}:
				default:
				}
			}
		}
	}()

	s.auditPodAccess(clusterID, "pod:exec", operator, target, detail, "会话开始")
	stdout := &wsBinaryWriter{conn: ws, mu: &writeMu}
	execErr := executor.ExecInPod(ctx, namespace, pod, collector.ExecOptions{
		Container: container,
		Command:   command,
		TTY:       true,
		Stdin:     stdinR,
		Stdout:    stdout,
		Resize:    resizeQueue,
	})

	result := webSocketSessionResult(ctx, "会话正常结束")
	if execErr != nil && result == "会话正常结束" && !errors.Is(execErr, context.Canceled) {
		result = "会话异常结束: " + execErr.Error()
	}
	connectionWritable := true
	if execErr != nil {
		message := execErr.Error()
		if sessionResult := webSocketSessionResult(ctx, ""); sessionResult != "" {
			message = sessionResult
		}
		if writeErr := writeWebSocketMessage(ws, &writeMu, websocket.TextMessage, mustJSON(map[string]string{"type": "error", "message": message})); writeErr != nil {
			connectionWritable = false
		}
	}
	// 通知前端会话结束,便于终端提示。
	if connectionWritable {
		_ = writeWebSocketMessage(ws, &writeMu, websocket.TextMessage, mustJSON(map[string]string{"type": "exit"}))
	}

	pipeErr := execErr
	if pipeErr == nil {
		pipeErr = io.EOF
	}
	_ = stdinR.CloseWithError(pipeErr)
	cancel(nil)
	_ = ws.Close()
	<-readDone
	s.auditPodAccess(clusterID, "pod:exec", operator, target, detail, result)
	return nil
}

// auditPodAccess 记录一条 Pod 访问审计(日志 / exec),集群 ID 为 0 时记为全局。
func (s *Server) auditPodAccess(clusterID uint, action, operator, target, detail, result string) {
	if s.store == nil {
		return
	}
	_ = s.store.AddAudit(clusterID, &store.AuditLog{
		Action:   action,
		Operator: operator,
		Target:   target,
		Detail:   detail,
		Result:   result,
	})
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte(fmt.Sprintf("{\"type\":\"error\",\"message\":%q}", err.Error()))
	}
	return data
}
