package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"aiops/internal/ai"
	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/inspect"
	"aiops/internal/store"
)

func TestChatInspectionShortcutRunsAllClustersBeforeModelAnswer(t *testing.T) {
	st := openAgentTestStore(t)
	clusterA := createAgentTestCluster(t, st, "chat-a")
	clusterB := createAgentTestCluster(t, st, "chat-b")
	manager := clusterruntime.NewManager()
	registerChatRuntime(t, manager, clusterA, "node-a")
	registerChatRuntime(t, manager, clusterB, "node-b")

	modelServer := newShortcutModelServer(t, func(t *testing.T, request chatCompletionRequestForTest) {
		if !hasToolResult(request, "run_inspection") {
			t.Fatalf("模型回答前应已收到 run_inspection 工具结果: %+v", request.Messages)
		}
	})
	defer modelServer.Close()

	ag := New(ai.NewClient(modelServer.URL, "test-key", "test-model", 512, 5, 0),
		NewToolExecutor(nil, nil, st, nil, clusterA.ID, manager),
		nil,
	)
	var events []StreamEvent
	ag.ChatStream(context.Background(), "s-all", "帮我巡检一下", 0, UserIdentity{}, unrestrictedChatAccess(), func(ev StreamEvent) {
		events = append(events, ev)
	})

	if !hasStep(events, "run_inspection") {
		t.Fatalf("应先触发 run_inspection 快捷工具调用: %+v", events)
	}
	reportsA, err := st.ListReports(clusterA.ID, 50)
	if err != nil {
		t.Fatalf("查询集群 A 报告失败: %v", err)
	}
	reportsB, err := st.ListReports(clusterB.ID, 50)
	if err != nil {
		t.Fatalf("查询集群 B 报告失败: %v", err)
	}
	if len(reportsA) != 1 || len(reportsB) != 1 {
		t.Fatalf("默认巡检应覆盖所有集群: reportsA=%d reportsB=%d", len(reportsA), len(reportsB))
	}
}

func TestChatInspectionShortcutCanTargetClusterByName(t *testing.T) {
	st := openAgentTestStore(t)
	clusterA := createAgentTestCluster(t, st, "生产集群")
	clusterB := createAgentTestCluster(t, st, "测试集群")
	manager := clusterruntime.NewManager()
	registerChatRuntime(t, manager, clusterA, "node-a")
	registerChatRuntime(t, manager, clusterB, "node-b")

	modelServer := newShortcutModelServer(t, nil)
	defer modelServer.Close()

	ag := New(ai.NewClient(modelServer.URL, "test-key", "test-model", 512, 5, 0),
		NewToolExecutor(nil, nil, st, nil, clusterA.ID, manager),
		nil,
	)
	var events []StreamEvent
	ag.ChatStream(context.Background(), "s-one", "只巡检测试集群", 0, UserIdentity{}, unrestrictedChatAccess(), func(ev StreamEvent) {
		events = append(events, ev)
	})

	step := firstStep(events, "run_inspection")
	if step == nil || !strings.Contains(step.Args, `"cluster_id":`+strconv.FormatUint(uint64(clusterB.ID), 10)) {
		t.Fatalf("应将指定集群名称解析为对应 cluster_id: step=%+v events=%+v", step, events)
	}
	reportsA, err := st.ListReports(clusterA.ID, 50)
	if err != nil {
		t.Fatalf("查询集群 A 报告失败: %v", err)
	}
	reportsB, err := st.ListReports(clusterB.ID, 50)
	if err != nil {
		t.Fatalf("查询集群 B 报告失败: %v", err)
	}
	if len(reportsA) != 0 || len(reportsB) != 1 {
		t.Fatalf("指定集群巡检应只覆盖目标集群: reportsA=%d reportsB=%d", len(reportsA), len(reportsB))
	}
}

func unrestrictedChatAccess() ChatAccessScope {
	return ChatAccessScope{
		AllClusters: true,
		Permissions: []string{
			store.PermissionViewClusters,
			store.PermissionRunInspect,
			store.PermissionApproveRemediation,
		},
	}
}

type chatCompletionRequestForTest struct {
	Messages []ai.Message `json:"messages"`
}

func newShortcutModelServer(t *testing.T, assertRequest func(*testing.T, chatCompletionRequestForTest)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("模型请求路径不正确: %s", r.URL.Path)
		}
		var req chatCompletionRequestForTest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("解析模型请求失败: %v", err)
		}
		if assertRequest != nil {
			assertRequest(t, req)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"巡检完成\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
}

func hasToolResult(request chatCompletionRequestForTest, toolName string) bool {
	for _, msg := range request.Messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, call := range msg.ToolCalls {
			if call.Function.Name == toolName {
				return true
			}
		}
	}
	return false
}

func hasStep(events []StreamEvent, toolName string) bool {
	return firstStep(events, toolName) != nil
}

func firstStep(events []StreamEvent, toolName string) *StreamEvent {
	for i := range events {
		if events[i].Type == "step" && events[i].Tool == toolName {
			return &events[i]
		}
	}
	return nil
}

func openAgentTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func createAgentTestCluster(t *testing.T, st *store.Store, name string) *store.Cluster {
	t.Helper()
	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: name,
		Slug: strings.ToLower(strings.ReplaceAll(name, " ", "-")),
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	return cluster
}

func registerChatRuntime(t *testing.T, manager *clusterruntime.Manager, cluster *store.Cluster, nodeName string) {
	t.Helper()
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		},
	)
	col := collector.New(kubeClient)
	if err := manager.Register(&clusterruntime.Runtime{
		ClusterID: cluster.ID,
		Cluster:   cluster,
		Collector: col,
		Engine:    inspect.NewEngine(col, nil),
	}); err != nil {
		t.Fatalf("注册测试运行时失败: %v", err)
	}
}
