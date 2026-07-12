package collector

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func TestListNodeMetricsCalculatesUsagePercent(t *testing.T) {
	kubeClient := fake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
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
					ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
					Usage: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			},
		}, nil
	})
	col := NewWithMetrics(kubeClient, metricsClient)

	items, err := col.ListNodeMetrics(context.Background())
	if err != nil {
		t.Fatalf("查询节点指标失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("指标数量不正确: got %d want 1", len(items))
	}
	got := items[0]
	if got.Name != "node-a" {
		t.Fatalf("节点名称不正确: %q", got.Name)
	}
	if got.CPUUsageMilli != 500 || got.CPUUsagePercent != 25 {
		t.Fatalf("CPU 指标不正确: %+v", got)
	}
	if got.MemoryUsageBytes != 1073741824 || got.MemoryUsagePercent != 25 {
		t.Fatalf("内存指标不正确: %+v", got)
	}
}

func TestListNodeMetricsRequiresMetricsClient(t *testing.T) {
	col := New(fake.NewSimpleClientset())
	if _, err := col.ListNodeMetrics(context.Background()); err == nil {
		t.Fatal("未配置 metrics client 时应返回错误")
	}
}
