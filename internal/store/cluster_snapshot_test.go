package store

import (
	"errors"
	"fmt"
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

func TestListClusterResourceSnapshotsWithOffset(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cluster, err := st.CreateCluster(CreateClusterInput{
		Name: "快照分页测试集群",
		Slug: "snapshot-pagination-test",
		Mode: ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	rows := make([]ClusterResourceSnapshot, 0, 5)
	for i := 0; i < 5; i++ {
		rows = append(rows, ClusterResourceSnapshot{
			ResourceName: "pods",
			APIVersion:   "v1",
			Kind:         "Pod",
			Namespace:    "default",
			Name:         fmt.Sprintf("pod-%d", i),
			ContentHash:  fmt.Sprintf("hash-%d", i),
		})
	}
	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, rows); err != nil {
		t.Fatalf("写入资源快照失败: %v", err)
	}

	page, err := st.ListClusterResourceSnapshotsWithOffset(cluster.ID, "pods", 2, 2)
	if err != nil {
		t.Fatalf("分页查询资源快照失败: %v", err)
	}
	if len(page) != 2 || page[0].Name != "pod-2" || page[1].Name != "pod-3" {
		t.Fatalf("资源快照 offset/limit 应用不正确: %+v", page)
	}
	firstPage, err := st.ListClusterResourceSnapshots(cluster.ID, "pods", 2)
	if err != nil {
		t.Fatalf("兼容查询资源快照失败: %v", err)
	}
	if len(firstPage) != 2 || firstPage[0].Name != "pod-0" {
		t.Fatalf("原查询方法应继续从首行返回: %+v", firstPage)
	}
}

func TestListClusterResourceSnapshotsPageRejectsChangedGeneration(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cluster, err := st.CreateCluster(CreateClusterInput{
		Name: "快照代次测试集群",
		Slug: "snapshot-generation-test",
		Mode: ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	initialRows := []ClusterResourceSnapshot{
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a", ContentHash: "hash-a"},
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-b", ContentHash: "hash-b"},
	}
	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, initialRows); err != nil {
		t.Fatalf("写入首批资源快照失败: %v", err)
	}

	firstPage, err := st.ListClusterResourceSnapshotsPage(cluster.ID, "pods", "", 0, 1)
	if err != nil {
		t.Fatalf("查询首批第一页失败: %v", err)
	}
	if firstPage.Generation == "" || len(firstPage.Snapshots) != 1 || firstPage.Snapshots[0].Name != "pod-a" {
		t.Fatalf("首批分页响应不正确: %+v", firstPage)
	}

	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, []ClusterResourceSnapshot{
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-0", ContentHash: "hash-0"},
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a", ContentHash: "hash-a"},
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-b", ContentHash: "hash-b"},
	}); err != nil {
		t.Fatalf("写入新一代资源快照失败: %v", err)
	}

	_, err = st.ListClusterResourceSnapshotsPage(cluster.ID, "pods", firstPage.Generation, 1, 1)
	if !errors.Is(err, ErrClusterResourceSnapshotGenerationChanged) {
		t.Fatalf("旧代次的后续页应被拒绝: %v", err)
	}
}

func TestListClusterResourceSnapshotsPageSupportsLegacyGeneration(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cluster, err := st.CreateCluster(CreateClusterInput{
		Name: "历史快照代次测试集群",
		Slug: "legacy-snapshot-generation-test",
		Mode: ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, []ClusterResourceSnapshot{
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a", ContentHash: "hash-a"},
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-b", ContentHash: "hash-b"},
	}); err != nil {
		t.Fatalf("写入资源快照失败: %v", err)
	}
	if err := st.db.Model(&Cluster{}).Where("id = ?", cluster.ID).UpdateColumn("resource_snapshot_generation", "").Error; err != nil {
		t.Fatalf("模拟历史数据库失败: %v", err)
	}

	firstPage, err := st.ListClusterResourceSnapshotsPage(cluster.ID, "pods", "", 0, 1)
	if err != nil {
		t.Fatalf("查询历史快照第一页失败: %v", err)
	}
	if firstPage.Generation != legacyClusterResourceSnapshotGeneration {
		t.Fatalf("历史快照应返回稳定的兼容代次: %+v", firstPage)
	}
	secondPage, err := st.ListClusterResourceSnapshotsPage(cluster.ID, "pods", firstPage.Generation, 1, 1)
	if err != nil {
		t.Fatalf("历史快照后续页不应被拒绝: %v", err)
	}
	if len(secondPage.Snapshots) != 1 || secondPage.Snapshots[0].Name != "pod-b" {
		t.Fatalf("历史快照第二页不正确: %+v", secondPage)
	}
}
