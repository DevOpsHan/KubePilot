package store

import (
	"path/filepath"
	"testing"
)

func TestEnsureDefaultClusterCreatesAndReusesRecord(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer func() { _ = st.Close() }()

	first, err := st.EnsureDefaultCluster(DefaultClusterInput{
		Name:           "默认集群",
		Mode:           ClusterModeKubeconfig,
		KubeconfigPath: "/tmp/kubeconfig",
	})
	if err != nil {
		t.Fatalf("创建默认集群失败: %v", err)
	}
	second, err := st.EnsureDefaultCluster(DefaultClusterInput{
		Name:           "默认集群",
		Mode:           ClusterModeKubeconfig,
		KubeconfigPath: "/tmp/kubeconfig",
	})
	if err != nil {
		t.Fatalf("复用默认集群失败: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("默认集群应复用同一记录: first=%d second=%d", first.ID, second.ID)
	}

	clusters, err := st.ListClusters()
	if err != nil {
		t.Fatalf("查询集群列表失败: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("集群数量不正确: got %d want 1", len(clusters))
	}
	if clusters[0].Slug != DefaultClusterSlug {
		t.Fatalf("默认集群标识不正确: got %q want %q", clusters[0].Slug, DefaultClusterSlug)
	}
}

func TestCreateClusterStoresCredentialAndPolicy(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer func() { _ = st.Close() }()

	cluster, err := st.CreateCluster(CreateClusterInput{
		Name:                "测试集群",
		Slug:                "test-cluster",
		Mode:                ClusterModeKubeconfigData,
		ContextName:         "kind-test",
		EncryptedKubeconfig: "encrypted",
		KubeconfigHash:      "hash",
		RemediationEnabled:  true,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}

	credential, err := st.GetClusterCredential(cluster.ID)
	if err != nil {
		t.Fatalf("查询集群凭据失败: %v", err)
	}
	if credential.EncryptedKubeconfig != "encrypted" || credential.KubeconfigHash != "hash" {
		t.Fatalf("凭据保存不正确: %+v", credential)
	}

	policy, err := st.GetClusterPolicy(cluster.ID)
	if err != nil {
		t.Fatalf("查询集群策略失败: %v", err)
	}
	if !policy.RemediationEnabled {
		t.Fatal("修复开关应为启用")
	}
}

func TestCreateClusterPersistsExplicitDisabledFlag(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer func() { _ = st.Close() }()

	enabled := false
	cluster, err := st.CreateCluster(CreateClusterInput{
		Name:    "禁用集群",
		Slug:    "disabled-cluster",
		Mode:    ClusterModeKubeconfigData,
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("创建禁用集群失败: %v", err)
	}

	loaded, err := st.GetCluster(cluster.ID)
	if err != nil {
		t.Fatalf("查询禁用集群失败: %v", err)
	}
	if loaded.Enabled {
		t.Fatal("显式传入 Enabled=false 时必须持久化为禁用")
	}
}

func TestUpdateClusterMetadataAndCredential(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer func() { _ = st.Close() }()

	cluster, err := st.CreateCluster(CreateClusterInput{
		Name:                "旧集群",
		Slug:                "old-cluster",
		Description:         "旧描述",
		Mode:                ClusterModeKubeconfigData,
		ContextName:         "old-context",
		APIServerHost:       "https://old.example.com",
		EncryptedKubeconfig: "old-encrypted",
		KubeconfigHash:      "old-hash",
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}

	updated, err := st.UpdateCluster(cluster.ID, UpdateClusterInput{
		Name:                "生产 ACK",
		Slug:                "prod-ack",
		Description:         "生产环境",
		ContextName:         "prod-context",
		APIServerHost:       "https://prod.example.com",
		EncryptedKubeconfig: "new-encrypted",
		KubeconfigHash:      "new-hash",
	})
	if err != nil {
		t.Fatalf("更新集群失败: %v", err)
	}
	if updated.Name != "生产 ACK" || updated.Slug != "prod-ack" || updated.Description != "生产环境" {
		t.Fatalf("集群基础信息未正确更新: %+v", updated)
	}
	if updated.ContextName != "prod-context" || updated.APIServerHost != "https://prod.example.com" {
		t.Fatalf("集群连接信息未正确更新: %+v", updated)
	}

	credential, err := st.GetClusterCredential(cluster.ID)
	if err != nil {
		t.Fatalf("查询凭据失败: %v", err)
	}
	if credential.EncryptedKubeconfig != "new-encrypted" || credential.KubeconfigHash != "new-hash" || credential.Version != 2 {
		t.Fatalf("凭据未正确更新或版本未递增: %+v", credential)
	}
}
