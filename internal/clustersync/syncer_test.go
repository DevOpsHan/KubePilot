package clustersync

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/store"
)

func TestSyncClusterStoresResourcesAndMarksReady(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "同步测试集群",
		Slug: "sync-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}

	kubeClient := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			Status: corev1.NodeStatus{
				NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.34.0"},
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "default"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "deploy-a", Namespace: "default"}},
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ds-a", Namespace: "default"}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts-a", Namespace: "default"}},
	)
	result, err := SyncCluster(context.Background(), st, &clusterruntime.Runtime{
		ClusterID: cluster.ID,
		Collector: collector.New(kubeClient),
	})
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if result.Status != store.ClusterStatusReady {
		t.Fatalf("同步后状态不正确: %+v", result)
	}

	updated, err := st.GetCluster(cluster.ID)
	if err != nil {
		t.Fatalf("查询集群失败: %v", err)
	}
	if updated.Status != store.ClusterStatusReady {
		t.Fatalf("集群状态未更新为 ready: %+v", updated)
	}
	if updated.Version != "v1.34.0" {
		t.Fatalf("集群版本未同步: got %q", updated.Version)
	}

	rows, err := st.ListClusterSyncResources(cluster.ID)
	if err != nil {
		t.Fatalf("查询同步资源失败: %v", err)
	}
	if len(rows) < 16 {
		t.Fatalf("同步资源数量不正确: got %d want at least 16", len(rows))
	}
	if row := findSyncRow(rows, "nodes"); row == nil || row.ItemCount != 1 || row.FullStatus != store.SyncStatusSuccess {
		t.Fatalf("nodes 同步状态不正确: %+v", row)
	}
	if row := findSyncRow(rows, "services"); row == nil || row.FullStatus != store.SyncStatusSuccess {
		t.Fatalf("services 同步状态不正确: %+v", row)
	}
	if row := findSyncRow(rows, "customresourcedefinitions"); row == nil || row.FullStatus != store.SyncStatusNA {
		t.Fatalf("CRD 无动态客户端时应为 N/A: %+v", row)
	}
	tasks, err := st.ListClusterSyncTasks(cluster.ID, 5)
	if err != nil {
		t.Fatalf("查询同步任务失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != store.SyncStatusSuccess || tasks[0].ResourceCount != 6 || tasks[0].ChangedCount != 6 {
		t.Fatalf("同步任务记录不正确: %+v", tasks)
	}
}

func TestSyncClusterStoresSourceVersionsAndObjectSnapshots(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "来源测试集群",
		Slug: "source-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}

	kubeClient := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "pod-a",
				Namespace:       "default",
				UID:             "pod-uid",
				ResourceVersion: "10",
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "deploy-a",
				Namespace:       "default",
				UID:             "deploy-uid",
				ResourceVersion: "20",
			},
			Status: appsv1.DeploymentStatus{Replicas: 2, ReadyReplicas: 1, AvailableReplicas: 1},
		},
	)
	result, err := SyncClusterWithSource(context.Background(), st, &clusterruntime.Runtime{
		ClusterID: cluster.ID,
		Collector: collector.New(kubeClient),
	}, store.SyncSourceManual)
	if err != nil {
		t.Fatalf("同步失败: %v", err)
	}
	if result.Status != store.ClusterStatusReady {
		t.Fatalf("同步状态不正确: %+v", result)
	}

	rows, err := st.ListClusterSyncResources(cluster.ID)
	if err != nil {
		t.Fatalf("查询同步资源失败: %v", err)
	}
	podRow := findSyncRow(rows, "pods")
	if podRow == nil {
		t.Fatalf("缺少 pods 同步行: %+v", rows)
	}
	if podRow.LastSource != store.SyncSourceManual {
		t.Fatalf("同步来源未保存: %+v", podRow)
	}
	if podRow.LastChangeCount != 1 {
		t.Fatalf("首次同步应记录 Pod 快照新增变更: %+v", podRow)
	}

	pods, err := st.ListClusterResourceSnapshots(cluster.ID, "pods", 0)
	if err != nil {
		t.Fatalf("查询 Pod 快照失败: %v", err)
	}
	if len(pods) != 1 || pods[0].Name != "pod-a" || pods[0].Status != "Running" {
		t.Fatalf("Pod 快照内容不正确: %+v", pods)
	}
	deployments, err := st.ListClusterResourceSnapshots(cluster.ID, "deployments", 0)
	if err != nil {
		t.Fatalf("查询 Deployment 快照失败: %v", err)
	}
	if len(deployments) != 1 || deployments[0].Name != "deploy-a" || deployments[0].Ready != "1/2" {
		t.Fatalf("Deployment 快照内容不正确: %+v", deployments)
	}
}

func TestSyncClusterDisconnectPreservesLastSuccessfulResources(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "断连测试集群",
		Slug: "disconnect-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	lastSuccessAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := st.ReplaceClusterSyncResources(cluster.ID, []store.ClusterSyncResource{
		{
			APIVersion:        "v1",
			Kind:              "Pod",
			ResourceName:      "pods",
			ItemCount:         1,
			FullFinishedAt:    &lastSuccessAt,
			FullStatus:        store.SyncStatusSuccess,
			IncrementalStatus: store.SyncStatusSuccess,
			LastSource:        store.SyncSourceAuto,
		},
	}); err != nil {
		t.Fatalf("写入历史同步状态失败: %v", err)
	}
	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, []store.ClusterResourceSnapshot{
		{
			APIVersion:      "v1",
			Kind:            "Pod",
			ResourceName:    "pods",
			Namespace:       "default",
			Name:            "pod-a",
			UID:             "pod-a-uid",
			ResourceVersion: "10",
			Status:          "Running",
			ContentHash:     "pod-a-v1",
		},
	}); err != nil {
		t.Fatalf("写入历史资源快照失败: %v", err)
	}

	kubeClient := fake.NewSimpleClientset()
	kubeClient.Fake.PrependReactor("list", "*", func(clienttesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, errors.New("dial tcp 10.0.0.1:6443: connectex: network unreachable")
	})
	result, err := SyncClusterWithSource(context.Background(), st, &clusterruntime.Runtime{
		ClusterID: cluster.ID,
		Collector: collector.New(kubeClient),
	}, store.SyncSourceAuto)
	if err != nil {
		t.Fatalf("网络断连应返回结构化同步结果: %v", err)
	}
	if result == nil || result.Status != store.ClusterStatusDisconnected {
		t.Fatalf("网络断连应标记为 disconnected: %+v", result)
	}

	updated, err := st.GetCluster(cluster.ID)
	if err != nil {
		t.Fatalf("查询集群失败: %v", err)
	}
	if updated.Status != store.ClusterStatusDisconnected || updated.LastError == "" {
		t.Fatalf("集群断连状态未正确保存: %+v", updated)
	}

	rows, err := st.ListClusterSyncResources(cluster.ID)
	if err != nil {
		t.Fatalf("查询历史同步状态失败: %v", err)
	}
	if len(rows) != 1 || rows[0].ResourceName != "pods" || rows[0].FullStatus != store.SyncStatusSuccess || rows[0].ItemCount != 1 {
		t.Fatalf("断连不应覆盖上次成功同步状态: %+v", rows)
	}
	snapshots, err := st.ListClusterResourceSnapshots(cluster.ID, "pods", 10)
	if err != nil {
		t.Fatalf("查询历史资源快照失败: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Name != "pod-a" || snapshots[0].ResourceVersion != "10" {
		t.Fatalf("断连不应删除上次成功资源快照: %+v", snapshots)
	}

	tasks, err := st.ListClusterSyncTasks(cluster.ID, 1)
	if err != nil {
		t.Fatalf("查询同步任务失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != store.SyncStatusFailed || tasks[0].ResourceCount != 0 || tasks[0].Error == "" {
		t.Fatalf("断连任务应记录为失败且没有本轮资源: %+v", tasks)
	}
}

func TestSyncClusterDisconnectAfterPingPreservesLastSuccessfulResources(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "同步中断连测试集群",
		Slug: "disconnect-after-ping-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	if err := st.ReplaceClusterSyncResources(cluster.ID, []store.ClusterSyncResource{
		{
			APIVersion:        "v1",
			Kind:              "Pod",
			ResourceName:      "pods",
			ItemCount:         1,
			FullStatus:        store.SyncStatusSuccess,
			IncrementalStatus: store.SyncStatusSuccess,
			LastSource:        store.SyncSourceAuto,
		},
	}); err != nil {
		t.Fatalf("写入历史同步状态失败: %v", err)
	}

	kubeClient := fake.NewSimpleClientset()
	pingPassed := false
	kubeClient.Fake.PrependReactor("list", "*", func(action clienttesting.Action) (bool, k8sruntime.Object, error) {
		if !pingPassed && action.GetResource().Resource == "namespaces" {
			pingPassed = true
			return false, nil, nil
		}
		return true, nil, errors.New("dial tcp 10.0.0.1:6443: connectex: network unreachable")
	})
	result, err := SyncClusterWithSource(context.Background(), st, &clusterruntime.Runtime{
		ClusterID: cluster.ID,
		Collector: collector.New(kubeClient),
	}, store.SyncSourceAuto)
	if err != nil {
		t.Fatalf("同步中断连应返回结构化同步结果: %v", err)
	}
	if result == nil || result.Status != store.ClusterStatusDisconnected {
		t.Fatalf("同步中断连应标记为 disconnected: %+v", result)
	}

	rows, err := st.ListClusterSyncResources(cluster.ID)
	if err != nil {
		t.Fatalf("查询历史同步状态失败: %v", err)
	}
	if len(rows) != 1 || rows[0].ResourceName != "pods" || rows[0].FullStatus != store.SyncStatusSuccess || rows[0].ItemCount != 1 {
		t.Fatalf("同步中断连不应覆盖上次成功同步状态: %+v", rows)
	}
	tasks, err := st.ListClusterSyncTasks(cluster.ID, 1)
	if err != nil {
		t.Fatalf("查询同步任务失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != store.SyncStatusFailed || tasks[0].ResourceCount != 0 {
		t.Fatalf("同步中断连任务应记录为失败且没有本轮资源: %+v", tasks)
	}
}

func findSyncRow(rows []store.ClusterSyncResource, resource string) *store.ClusterSyncResource {
	for i := range rows {
		if rows[i].ResourceName == resource {
			return &rows[i]
		}
	}
	return nil
}
