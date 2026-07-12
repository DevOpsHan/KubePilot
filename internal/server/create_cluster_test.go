package server

import (
	"bytes"
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

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/store"
)

func TestCreateClusterStoresEncryptedKubeconfigAndRegistersRuntime(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", strings.Repeat("test", 8))

	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	manager := clusterruntime.NewManager()
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, manager)
	srv.runtimeBuilder = func(ctx context.Context, cluster *store.Cluster, policy *store.ClusterPolicy, kubeconfigData string) (*clusterruntime.Runtime, error) {
		if !strings.Contains(kubeconfigData, "kind: Config") {
			t.Fatalf("运行时构建应收到 kubeconfig 原文")
		}
		kubeClient := fake.NewSimpleClientset(&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-from-added-cluster"},
		})
		col := collector.New(kubeClient)
		return &clusterruntime.Runtime{ClusterID: cluster.ID, Collector: col}, nil
	}

	body := map[string]any{
		"name":       "测试集群",
		"slug":       "test-added",
		"kubeconfig": "apiVersion: v1\nkind: Config\nclusters: []\n",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/clusters", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建集群失败: code=%d body=%s", rec.Code, rec.Body.String())
	}

	clusters, err := st.ListClusters()
	if err != nil {
		t.Fatalf("查询集群失败: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("集群数量不正确: got %d want 1", len(clusters))
	}
	credential, err := st.GetClusterCredential(clusters[0].ID)
	if err != nil {
		t.Fatalf("查询凭据失败: %v", err)
	}
	if credential.EncryptedKubeconfig == "" || strings.Contains(credential.EncryptedKubeconfig, "apiVersion") {
		t.Fatalf("kubeconfig 未加密保存: %+v", credential)
	}
	if _, err := manager.Get(clusters[0].ID); err != nil {
		t.Fatalf("新增集群运行时未注册: %v", err)
	}
}

func TestCreateClusterRejectsMissingEncryptionKey(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", "")

	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := NewServer(nil, nil, nil, st, nil, nil, 0, clusterruntime.NewManager())
	raw := []byte(`{"name":"测试集群","kubeconfig":"apiVersion: v1\nkind: Config\n"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/clusters", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺少加密密钥时应拒绝请求: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateClusterRejectsWhenClusterLimitReached(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	for i := 1; i <= 5; i++ {
		if _, err := st.CreateCluster(store.CreateClusterInput{
			Name: "集群",
			Slug: "cluster-limit-" + strconv.Itoa(i),
			Mode: store.ClusterModeKubeconfigData,
		}); err != nil {
			t.Fatalf("准备测试集群失败: %v", err)
		}
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, 0, clusterruntime.NewManager())
	raw := []byte(`{"name":"第六个集群","kubeconfig":"apiVersion: v1\nkind: Config\n"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/clusters", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("超过集群上限时应拒绝请求: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "最多只能接入 5 个集群") {
		t.Fatalf("错误信息不正确: %s", rec.Body.String())
	}
}

func TestUpdateClusterEditsMetadataAndRefreshesKubeconfigRuntime(t *testing.T) {
	t.Setenv("AIOPS_SECRET_KEY", strings.Repeat("test", 8))

	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name:                "旧集群",
		Slug:                "old-cluster",
		Description:         "旧描述",
		Mode:                store.ClusterModeKubeconfigData,
		ContextName:         "old-context",
		EncryptedKubeconfig: "old-encrypted",
		KubeconfigHash:      "old-hash",
	})
	if err != nil {
		t.Fatalf("准备测试集群失败: %v", err)
	}

	manager := clusterruntime.NewManager()
	srv := NewServer(nil, nil, nil, st, nil, nil, 0, manager)
	srv.runtimeBuilder = func(ctx context.Context, cluster *store.Cluster, policy *store.ClusterPolicy, kubeconfigData string) (*clusterruntime.Runtime, error) {
		if !strings.Contains(kubeconfigData, "new-config") {
			t.Fatalf("运行时构建应收到新的 kubeconfig 原文: %s", kubeconfigData)
		}
		kubeClient := fake.NewSimpleClientset(&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-from-updated-cluster"},
		})
		return &clusterruntime.Runtime{ClusterID: cluster.ID, Collector: collector.New(kubeClient)}, nil
	}

	body := map[string]any{
		"name":         "生产 ACK",
		"slug":         "prod-ack",
		"description":  "生产环境",
		"context_name": "prod-context",
		"kubeconfig":   "apiVersion: v1\nkind: Config\nnew-config: true\n",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/clusters/"+strconv.Itoa(int(cluster.ID)), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("编辑集群失败: code=%d body=%s", rec.Code, rec.Body.String())
	}

	updated, err := st.GetCluster(cluster.ID)
	if err != nil {
		t.Fatalf("查询更新后集群失败: %v", err)
	}
	if updated.Name != "生产 ACK" || updated.Slug != "prod-ack" || updated.Description != "生产环境" || updated.ContextName != "prod-context" {
		t.Fatalf("集群信息未更新: %+v", updated)
	}
	credential, err := st.GetClusterCredential(cluster.ID)
	if err != nil {
		t.Fatalf("查询凭据失败: %v", err)
	}
	if credential.EncryptedKubeconfig == "old-encrypted" || credential.KubeconfigHash == "old-hash" || credential.Version != 2 {
		t.Fatalf("凭据未更新或版本未递增: %+v", credential)
	}
	if _, err := manager.Get(cluster.ID); err != nil {
		t.Fatalf("更新 kubeconfig 后运行时应重新注册: %v", err)
	}
}
