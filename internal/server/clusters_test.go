package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"aiops/internal/clusterruntime"
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
