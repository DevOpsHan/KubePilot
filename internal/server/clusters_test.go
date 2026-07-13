package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/store"
)

func TestListClustersIncludesRuntimeReadiness(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	clusterA, err := st.CreateCluster(store.CreateClusterInput{Name: "集群 A", Slug: "cluster-a", Mode: store.ClusterModeKubeconfig})
	if err != nil {
		t.Fatalf("创建集群 A 失败: %v", err)
	}
	_, err = st.CreateCluster(store.CreateClusterInput{Name: "集群 B", Slug: "cluster-b", Mode: store.ClusterModeKubeconfig})
	if err != nil {
		t.Fatalf("创建集群 B 失败: %v", err)
	}
	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{ClusterID: clusterA.ID}); err != nil {
		t.Fatalf("注册运行时失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, clusterA.ID, manager)
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询集群列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Count    int `json:"count"`
		Clusters []struct {
			ID           uint   `json:"id"`
			Name         string `json:"name"`
			RuntimeReady bool   `json:"runtime_ready"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Count != 2 || len(body.Clusters) != 2 {
		t.Fatalf("集群数量不正确: %+v", body)
	}
	if !body.Clusters[0].RuntimeReady {
		t.Fatalf("集群 A 应显示运行时就绪: %+v", body.Clusters[0])
	}
	if body.Clusters[1].RuntimeReady {
		t.Fatalf("集群 B 不应显示运行时就绪: %+v", body.Clusters[1])
	}
}

// TestListClustersDowngradesUnreachableCluster 验证列表接口的实时连通性探测:
// DB 里状态仍为 ready 的集群,若 API Server 当前已连不通,列表应即时下调为 disconnected,
// 而不必等待下一个同步周期。
func TestListClustersDowngradesUnreachableCluster(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{Name: "断连集群", Slug: "unreachable", Mode: store.ClusterModeKubeconfig})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	// 模拟上次同步成功后集群被标记为 ready
	if err := st.UpdateClusterStatus(cluster.ID, store.ClusterStatusReady, ""); err != nil {
		t.Fatalf("更新集群状态失败: %v", err)
	}

	// 让所有 list 请求失败(Ping 回退到 Namespaces().List),模拟 API Server 不可达
	kubeClient := fake.NewSimpleClientset()
	kubeClient.Fake.PrependReactor("list", "*", func(clienttesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, errors.New("dial tcp 10.0.0.1:6443: connectex: network unreachable")
	})
	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{ClusterID: cluster.ID, Collector: collector.New(kubeClient)}); err != nil {
		t.Fatalf("注册运行时失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, manager)
	req := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询集群列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Clusters []struct {
			ID           uint   `json:"id"`
			Status       string `json:"status"`
			RuntimeReady bool   `json:"runtime_ready"`
			LastError    string `json:"last_error"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(body.Clusters) != 1 {
		t.Fatalf("集群数量不正确: %+v", body)
	}
	if body.Clusters[0].Status != store.ClusterStatusDisconnected {
		t.Fatalf("连不通的集群应即时下调为 disconnected,实际: %+v", body.Clusters[0])
	}
	if body.Clusters[0].RuntimeReady {
		t.Fatalf("连不通的集群 runtime_ready 应为 false: %+v", body.Clusters[0])
	}
	if body.Clusters[0].LastError == "" {
		t.Fatalf("连不通的集群应带连接失败原因: %+v", body.Clusters[0])
	}

	// DB 里的状态不应被列表探测写回(写库仍由同步流程负责)
	stored, err := st.GetCluster(cluster.ID)
	if err != nil {
		t.Fatalf("查询集群失败: %v", err)
	}
	if stored.Status != store.ClusterStatusReady {
		t.Fatalf("列表实时探测不应改写数据库状态,DB 仍应为 ready,实际: %s", stored.Status)
	}
}
