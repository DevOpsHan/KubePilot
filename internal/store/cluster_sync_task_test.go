package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestClusterSyncTaskLifecycle(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(CreateClusterInput{
		Name: "任务测试集群",
		Slug: "task-test",
		Mode: ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}

	task, err := st.StartClusterSyncTask(cluster.ID, SyncSourceAuto)
	if err != nil {
		t.Fatalf("创建同步任务失败: %v", err)
	}
	if task.ID == 0 || task.Status != SyncStatusRunning || task.Source != SyncSourceAuto {
		t.Fatalf("同步任务初始状态不正确: %+v", task)
	}

	err = st.FinishClusterSyncTask(task.ID, SyncStatusSuccess, 12, 3, "")
	if err != nil {
		t.Fatalf("完成同步任务失败: %v", err)
	}
	tasks, err := st.ListClusterSyncTasks(cluster.ID, 10)
	if err != nil {
		t.Fatalf("查询同步任务失败: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("同步任务数量不正确: got %d want 1", len(tasks))
	}
	got := tasks[0]
	if got.Status != SyncStatusSuccess || got.ResourceCount != 12 || got.ChangedCount != 3 {
		t.Fatalf("同步任务完成状态不正确: %+v", got)
	}
	if got.FinishedAt == nil || got.FinishedAt.Before(got.StartedAt.Add(-time.Second)) {
		t.Fatalf("同步任务结束时间不正确: %+v", got)
	}
}
