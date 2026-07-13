package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/store"
)

// TestPodExecRejectsWithoutExecPermission 验证 exec 端点的权限门槛:
// 巡检员(inspector)角色不含 clusters:exec,应在 WebSocket 升级前被拒(403)。
func TestPodExecRejectsWithoutExecPermission(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{Name: "集群", Slug: "c1", Mode: store.ClusterModeKubeconfig})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{ClusterID: cluster.ID, Collector: collector.New(fake.NewSimpleClientset())}); err != nil {
		t.Fatalf("注册运行时失败: %v", err)
	}

	// 巡检员:绑定该集群(有 clusters:view + 集群访问权),但角色不含 clusters:exec
	inspector, err := st.CreateUser(store.CreateUserInput{
		Username:               fmt.Sprintf("inspector-%d", time.Now().UnixNano()),
		DisplayName:            "巡检员",
		Password:               "secret",
		Role:                   store.UserRoleInspector,
		Status:                 store.UserStatusActive,
		PasswordChangeRequired: boolPtr(false),
		ClusterIDs:             []uint{cluster.ID},
	})
	if err != nil {
		t.Fatalf("创建巡检员失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, manager)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/clusters/%d/pods/default/nginx/exec", cluster.ID), nil)
	attachServerTestSession(t, st, req, inspector.ID)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("无 clusters:exec 权限应被拒 403,实际: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestListPodContainersReturnsNames 验证容器列表接口:预置一个多容器 Pod,
// 管理员请求应返回 init 容器 + 业务容器名。
func TestListPodContainersReturnsNames(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{Name: "集群", Slug: "c1", Mode: store.ClusterModeKubeconfig})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "init-db"}},
			Containers:     []corev1.Container{{Name: "app"}, {Name: "sidecar"}},
		},
	}
	kubeClient := fake.NewSimpleClientset(pod)
	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{ClusterID: cluster.ID, Collector: collector.New(kubeClient)}); err != nil {
		t.Fatalf("注册运行时失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, manager)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/clusters/%d/pods/default/web/containers", cluster.ID), nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("查询容器列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Containers []string `json:"containers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	want := []string{"init-db", "app", "sidecar"}
	if len(body.Containers) != len(want) {
		t.Fatalf("容器数量不符,期望 %v,实际 %v", want, body.Containers)
	}
	for i, name := range want {
		if body.Containers[i] != name {
			t.Fatalf("容器顺序/名称不符,期望 %v,实际 %v", want, body.Containers)
		}
	}
}
