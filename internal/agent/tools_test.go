package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/store"
)

func TestToolExecutorUsesRequestedClusterRuntime(t *testing.T) {
	manager := clusterruntime.NewManager()
	registerRuntime(t, manager, 1, "node-a")
	registerRuntime(t, manager, 2, "node-b")

	executor := NewToolExecutor(nil, nil, nil, nil, 1, manager)
	got := executor.Execute(context.Background(), 2, unrestrictedChatAccess(), "list_nodes", "{}")
	if !strings.Contains(got, "node-b") {
		t.Fatalf("应查询集群 2 的节点: %s", got)
	}
	if strings.Contains(got, "node-a") {
		t.Fatalf("不应混入默认集群节点: %s", got)
	}
}

func TestPlanShortcutMapsCommonQuickActions(t *testing.T) {
	st := openAgentTestStore(t)
	cluster := createAgentTestCluster(t, st, "生产集群")
	executor := NewToolExecutor(nil, nil, st, nil, cluster.ID, nil)

	cases := []struct {
		input string
		tool  string
	}{
		{input: "给我看下集群概览", tool: "cluster_overview"},
		{input: "帮我巡检一下", tool: "run_inspection"},
		{input: "所有异常 Pod 帮我排查一下", tool: "run_inspection"},
		{input: "列出节点状态", tool: "list_nodes"},
		{input: "看一下 CPU 和内存性能监控", tool: "node_metrics"},
	}
	for _, tt := range cases {
		plan, ok := executor.PlanShortcut(tt.input, 0, unrestrictedChatAccess())
		if !ok {
			t.Fatalf("输入 %q 应命中快捷意图", tt.input)
		}
		if plan.Name != tt.tool {
			t.Fatalf("输入 %q 工具不正确: got %q want %q", tt.input, plan.Name, tt.tool)
		}
	}
}

func TestPlanShortcutDoesNotExposeUnauthorizedClusterID(t *testing.T) {
	st := openAgentTestStore(t)
	allowedCluster := createAgentTestCluster(t, st, "快捷授权集群")
	deniedCluster := createAgentTestCluster(t, st, "快捷机密集群")
	executor := NewToolExecutor(nil, nil, st, nil, 0, nil)
	access := ChatAccessScope{
		ClusterIDs:  []uint{allowedCluster.ID},
		Permissions: []string{store.PermissionRunInspect},
	}
	if access.AllowsCluster(deniedCluster.ID) {
		t.Fatalf("测试访问范围不应包含机密集群")
	}

	plan, ok := executor.PlanShortcut("只巡检快捷机密集群", 0, access)
	if ok {
		t.Fatalf("未授权集群名称不应生成快捷工具计划: denied_id=%d plan=%+v", deniedCluster.ID, plan)
	}
}

func TestToolExecutorNodeMetricsDefaultsToAllClusters(t *testing.T) {
	manager := clusterruntime.NewManager()
	registerMetricsRuntime(t, manager, 1, "node-a", "500m", "1Gi")
	registerMetricsRuntime(t, manager, 2, "node-b", "250m", "512Mi")

	executor := NewToolExecutor(nil, nil, nil, nil, 1, manager)
	raw := executor.Execute(context.Background(), 0, unrestrictedChatAccess(), "node_metrics", "{}")
	var body struct {
		Count    int `json:"count"`
		Clusters []struct {
			ClusterID uint                       `json:"cluster_id"`
			Metrics   []collector.NodeMetricInfo `json:"metrics"`
			Error     string                     `json:"error"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("解析节点指标响应失败: %v raw=%s", err, raw)
	}
	if body.Count != 2 || len(body.Clusters) != 2 {
		t.Fatalf("默认应返回所有集群节点指标: %+v", body)
	}
	for _, item := range body.Clusters {
		if item.Error != "" || len(item.Metrics) != 1 {
			t.Fatalf("节点指标结果不正确: %+v", item)
		}
	}
}

func TestToolExecutorListClustersOnlyReturnsAuthorizedTargets(t *testing.T) {
	st := openAgentTestStore(t)
	allowedCluster := createAgentTestCluster(t, st, "授权列表集群")
	deniedCluster := createAgentTestCluster(t, st, "未授权列表集群")
	executor := NewToolExecutor(nil, nil, st, nil, 0, nil)
	access := ChatAccessScope{
		ClusterIDs:  []uint{allowedCluster.ID},
		Permissions: []string{store.PermissionViewClusters},
	}

	raw := executor.Execute(context.Background(), 0, access, "list_clusters", "{}")
	var body struct {
		Count    int `json:"count"`
		Clusters []struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("解析集群列表失败: %v raw=%s", err, raw)
	}
	if body.Count != 1 || len(body.Clusters) != 1 || body.Clusters[0].ID != allowedCluster.ID {
		t.Fatalf("集群列表未按授权范围过滤: %+v", body)
	}
	if strings.Contains(raw, deniedCluster.Name) || strings.Contains(raw, deniedCluster.Slug) {
		t.Fatalf("集群列表泄露未授权集群: %s", raw)
	}
}

func TestToolExecutorRejectsToolWithoutRolePermission(t *testing.T) {
	executor := NewToolExecutor(nil, nil, nil, nil, 0, nil)
	access := ChatAccessScope{
		AllClusters: true,
		Permissions: []string{store.PermissionViewClusters},
	}

	got := executor.Execute(context.Background(), 0, access, "run_inspection", "{}")
	if !strings.Contains(got, "没有权限") {
		t.Fatalf("缺少巡检权限时应拒绝执行: %s", got)
	}
}

func TestToolExecutorRejectsUnmappedToolByDefault(t *testing.T) {
	executor := NewToolExecutor(nil, nil, nil, nil, 0, nil)
	got := executor.Execute(context.Background(), 0, unrestrictedChatAccess(), "future_unmapped_tool", "{}")
	if !strings.Contains(got, "没有权限") {
		t.Fatalf("未登记权限映射的工具应默认拒绝: %s", got)
	}
}

func registerRuntime(t *testing.T, manager *clusterruntime.Manager, clusterID uint, nodeName string) {
	t.Helper()
	kubeClient := fake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
	})
	col := collector.New(kubeClient)
	if err := manager.Register(&clusterruntime.Runtime{ClusterID: clusterID, Collector: col}); err != nil {
		t.Fatalf("注册运行时失败: %v", err)
	}
}

func registerMetricsRuntime(t *testing.T, manager *clusterruntime.Manager, clusterID uint, nodeName, cpuUsage, memoryUsage string) {
	t.Helper()
	kubeClient := fake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("2"),
				corev1.ResourceMemory: resource.MustParse("4Gi"),
			},
		},
	})
	metricsClient := metricsfake.NewSimpleClientset()
	metricsClient.Fake.PrependReactor("list", "nodes", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.NodeMetricsList{
			Items: []metricsv1beta1.NodeMetrics{
				{
					ObjectMeta: metav1.ObjectMeta{Name: nodeName},
					Usage: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpuUsage),
						corev1.ResourceMemory: resource.MustParse(memoryUsage),
					},
				},
			},
		}, nil
	})
	col := collector.NewWithMetrics(kubeClient, metricsClient)
	if err := manager.Register(&clusterruntime.Runtime{ClusterID: clusterID, Collector: col}); err != nil {
		t.Fatalf("注册指标运行时失败: %v", err)
	}
}
