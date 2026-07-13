package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"k8s.io/client-go/tools/remotecommand"

	"aiops/internal/collector"
	"aiops/internal/store"
)

// podExecUpgrader 负责把 Pod 终端 / 日志跟随请求从 HTTP 升级为 WebSocket。
// CheckOrigin 仅放行同源与本地开发来源,避免跨站脚本借用浏览器 cookie 发起 exec。
var podExecUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     checkWebSocketOrigin,
}

// checkWebSocketOrigin 校验 WebSocket 握手来源:无 Origin(非浏览器)放行;
// 否则要求 Origin 主机与请求 Host 相同,或为本地开发地址(vite 代理场景)。
func checkWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	host := originHost(origin)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, r.Host) {
		return true
	}
	// 剥离端口后再比对主机名,兼容反向代理改写端口的场景。
	if strings.EqualFold(hostname(host), hostname(r.Host)) {
		return true
	}
	// 本地开发:vite dev server 代理 WebSocket 时 Origin 为 localhost:5174。
	switch hostname(host) {
	case "localhost", "127.0.0.1":
		return true
	}
	return false
}

func originHost(origin string) string {
	if i := strings.Index(origin, "://"); i >= 0 {
		return origin[i+3:]
	}
	return origin
}

func hostname(hostPort string) string {
	if i := strings.IndexByte(hostPort, ':'); i >= 0 {
		return hostPort[:i]
	}
	return hostPort
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
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
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
	if tail := c.QueryParam("tail"); tail != "" {
		if n, convErr := strconv.ParseInt(tail, 10, 64); convErr == nil && n >= 0 {
			opts.TailLines = &n
		}
	}
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
	stream, err := col.StreamPodLogs(c.Request().Context(), namespace, pod, opts)
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	}
	defer stream.Close()
	c.Response().Header().Set(echo.HeaderContentType, "text/plain; charset=utf-8")
	c.Response().WriteHeader(http.StatusOK)
	_, copyErr := io.Copy(c.Response(), stream)
	s.auditPodAccess(clusterID, "pod:logs", operator, target, "查看日志", copyResult(copyErr))
	return nil
}

// servePodLogsFollow 升级 WebSocket,持续把日志推送给前端,直到日志结束或客户端断开。
func (s *Server) servePodLogsFollow(c echo.Context, col *collector.Collector, clusterID uint, namespace, pod string, opts collector.LogStreamOptions, operator, target string) error {
	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel()

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

	var writeMu sync.Mutex
	// 读协程:感知客户端关闭,触发 cancel 停止日志流。
	go func() {
		for {
			if _, _, readErr := ws.ReadMessage(); readErr != nil {
				cancel()
				return
			}
		}
	}()

	buf := make([]byte, 4096)
	for {
		n, readErr := stream.Read(buf)
		if n > 0 {
			writeMu.Lock()
			writeErr := ws.WriteMessage(websocket.BinaryMessage, buf[:n])
			writeMu.Unlock()
			if writeErr != nil {
				break
			}
		}
		if readErr != nil {
			break
		}
	}
	s.auditPodAccess(clusterID, "pod:logs", operator, target, "查看日志(跟随)", "会话结束")
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

	ws, err := podExecUpgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return nil
	}
	defer ws.Close()

	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel()

	stdinR, stdinW := io.Pipe()
	resizeQueue := &terminalSizeQueue{ch: make(chan remotecommand.TerminalSize, 4)}
	var writeMu sync.Mutex
	var closeResizeOnce sync.Once
	closeResize := func() { closeResizeOnce.Do(func() { close(resizeQueue.ch) }) }

	// 读协程:解析前端控制帧(stdin / resize),客户端断开时收尾。
	go func() {
		defer func() {
			_ = stdinW.Close()
			closeResize()
			cancel()
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

	stdout := &wsBinaryWriter{conn: ws, mu: &writeMu}
	execErr := runtime.Collector.ExecInPod(ctx, namespace, pod, collector.ExecOptions{
		Container: container,
		Command:   command,
		TTY:       true,
		Stdin:     stdinR,
		Stdout:    stdout,
		Resize:    resizeQueue,
	})

	result := "会话正常结束"
	if execErr != nil {
		result = "会话异常结束: " + execErr.Error()
		writeMu.Lock()
		_ = ws.WriteMessage(websocket.TextMessage, mustJSON(map[string]string{"type": "error", "message": execErr.Error()}))
		writeMu.Unlock()
	}
	// 通知前端会话结束,便于终端提示。
	writeMu.Lock()
	_ = ws.WriteMessage(websocket.TextMessage, mustJSON(map[string]string{"type": "exit"}))
	writeMu.Unlock()

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

func copyResult(err error) string {
	if err != nil {
		return "读取中断: " + err.Error()
	}
	return "完成"
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte(fmt.Sprintf("{\"type\":\"error\",\"message\":%q}", err.Error()))
	}
	return data
}
