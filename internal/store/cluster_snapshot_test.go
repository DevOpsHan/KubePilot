package store

import (
	"path/filepath"
	"testing"
)

func TestReplaceClusterResourceSnapshotsDetectsCreateUpdateAndDelete(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(CreateClusterInput{
		Name: "快照测试集群",
		Slug: "snapshot-test",
		Mode: ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}

	first, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods", "deployments"}, []ClusterResourceSnapshot{
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a", ContentHash: "pod-v1"},
		{ResourceName: "deployments", APIVersion: "apps/v1", Kind: "Deployment", Namespace: "default", Name: "deploy-a", ContentHash: "deploy-v1"},
	})
	if err != nil {
		t.Fatalf("首次写入快照失败: %v", err)
	}
	if first["pods"] != 1 || first["deployments"] != 1 {
		t.Fatalf("首次写入应记录新增变更: %+v", first)
	}

	second, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods", "deployments"}, []ClusterResourceSnapshot{
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a", ContentHash: "pod-v1"},
		{ResourceName: "deployments", APIVersion: "apps/v1", Kind: "Deployment", Namespace: "default", Name: "deploy-a", ContentHash: "deploy-v1"},
	})
	if err != nil {
		t.Fatalf("第二次写入快照失败: %v", err)
	}
	if second["pods"] != 0 || second["deployments"] != 0 {
		t.Fatalf("内容未变化时不应记录变更: %+v", second)
	}

	third, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods", "deployments"}, []ClusterResourceSnapshot{
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a", ContentHash: "pod-v2"},
	})
	if err != nil {
		t.Fatalf("第三次写入快照失败: %v", err)
	}
	if third["pods"] != 1 || third["deployments"] != 1 {
		t.Fatalf("Pod 更新和 Deployment 删除都应记录变更: %+v", third)
	}

	pods, err := st.ListClusterResourceSnapshots(cluster.ID, "pods", 0)
	if err != nil {
		t.Fatalf("查询 Pod 快照失败: %v", err)
	}
	if len(pods) != 1 || pods[0].ContentHash != "pod-v2" {
		t.Fatalf("Pod 快照未替换为最新内容: %+v", pods)
	}
	deployments, err := st.ListClusterResourceSnapshots(cluster.ID, "deployments", 0)
	if err != nil {
		t.Fatalf("查询 Deployment 快照失败: %v", err)
	}
	if len(deployments) != 0 {
		t.Fatalf("已删除的 Deployment 快照不应保留: %+v", deployments)
	}

	deleted, err := st.ListClusterDeletedResources(cluster.ID, "deployments", 10)
	if err != nil {
		t.Fatalf("查询删除历史失败: %v", err)
	}
	if len(deleted) != 1 || deleted[0].Name != "deploy-a" || deleted[0].ResourceName != "deployments" {
		t.Fatalf("Deployment 删除历史不正确: %+v", deleted)
	}
}
