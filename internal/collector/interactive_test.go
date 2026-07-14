package collector

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestListPodContainersPrefersRegularContainers(t *testing.T) {
	col := New(fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "demo"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "init-db"}},
			Containers:     []corev1.Container{{Name: "app"}, {Name: "sidecar"}},
			EphemeralContainers: []corev1.EphemeralContainer{
				{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debugger"}},
			},
		},
	}))

	names, err := col.ListPodContainers(context.Background(), "default", "demo")
	if err != nil {
		t.Fatalf("读取容器列表失败: %v", err)
	}
	want := []string{"app", "sidecar", "init-db", "debugger"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("普通容器应排在 init 容器前供终端默认选择: got=%v want=%v", names, want)
	}
}

func TestStreamPodLogsDoesNotInheritShortRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/pods/demo/log") {
			http.NotFound(w, r)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("测试服务不支持流式刷新")
		}
		_, _ = io.WriteString(w, "first\n")
		flusher.Flush()
		time.Sleep(80 * time.Millisecond)
		_, _ = io.WriteString(w, "second\n")
	}))
	defer server.Close()

	cfg := &rest.Config{Host: server.URL, Timeout: 20 * time.Millisecond}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("构建测试客户端失败: %v", err)
	}
	col := NewWithMetricsDynamicAndConfig(clientset, nil, nil, cfg)
	tail := int64(10)
	stream, err := col.StreamPodLogs(context.Background(), "default", "demo", LogStreamOptions{Follow: true, TailLines: &tail})
	if err != nil {
		t.Fatalf("建立日志流失败: %v", err)
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("日志流不应被普通请求超时中断: %v", err)
	}
	if string(data) != "first\nsecond\n" {
		t.Fatalf("日志流内容不完整: %q", data)
	}
}

func TestStreamPodLogsStopsWhenContextIsCancelled(t *testing.T) {
	requestDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(requestDone)
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "ready\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	cfg := &rest.Config{Host: server.URL, Timeout: 20 * time.Millisecond}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("构建测试客户端失败: %v", err)
	}
	col := NewWithMetricsDynamicAndConfig(clientset, nil, nil, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := col.StreamPodLogs(ctx, "default", "demo", LogStreamOptions{Follow: true})
	if err != nil {
		t.Fatalf("建立日志流失败: %v", err)
	}
	defer stream.Close()
	buf := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("读取首段日志失败: %v", err)
	}
	cancel()
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, stream)
		readDone <- err
	}()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("取消 Context 后日志读取仍未结束")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("取消 Context 后服务端请求未被回收")
	}
}
