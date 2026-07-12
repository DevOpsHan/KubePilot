package remediate

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestResolveWorkloadDeployment 验证 Pod → ReplicaSet → Deployment 反查链能拿到顶层 Deployment。
func TestResolveWorkloadDeployment(t *testing.T) {
	ctrl := true
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-abc123",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "web", Controller: &ctrl},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-abc123-xyz",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "web-abc123", Controller: &ctrl},
			},
		},
	}
	e := New(fake.NewSimpleClientset(pod, rs), true)
	kind, name, err := e.ResolveWorkload(context.Background(), "default", "web-abc123-xyz")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if kind != "Deployment" || name != "web" {
		t.Fatalf("期望反查到 Deployment/web,实际 %s/%s", kind, name)
	}
}

// TestResolveWorkloadBarePod 验证裸 Pod(无控制器)不会被升级,返回空。
func TestResolveWorkloadBarePod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "solo", Namespace: "default"},
	}
	e := New(fake.NewSimpleClientset(pod), true)
	kind, name, err := e.ResolveWorkload(context.Background(), "default", "solo")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if kind != "" || name != "" {
		t.Fatalf("裸 Pod 期望返回空,实际 %s/%s", kind, name)
	}
}

// TestResolveWorkloadStatefulSet 验证直属 StatefulSet 的 Pod 不升级(仅 Deployment 体系升级)。
func TestResolveWorkloadStatefulSet(t *testing.T) {
	ctrl := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "db-0",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "StatefulSet", Name: "db", Controller: &ctrl},
			},
		},
	}
	e := New(fake.NewSimpleClientset(pod), true)
	kind, _, err := e.ResolveWorkload(context.Background(), "default", "db-0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if kind != "" {
		t.Fatalf("StatefulSet 直属 Pod 期望不升级,实际 kind=%s", kind)
	}
}
