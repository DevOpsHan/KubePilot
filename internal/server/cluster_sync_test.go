package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/store"
)

func TestManualClusterSyncReturnsConflictWhenAnotherSyncIsRunning(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "并发同步测试集群",
		Slug: "sync-lock-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	kubeClient := fake.NewSimpleClientset()
	kubeClient.Fake.PrependReactor("list", "nodes", func(action clienttesting.Action) (bool, runtime.Object, error) {
		once.Do(func() { close(entered) })
		<-release
		return true, &corev1.NodeList{Items: []corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		}}, nil
	})

	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{
		ClusterID: cluster.ID,
		Collector: collector.New(kubeClient),
	}); err != nil {
		t.Fatalf("注册运行时失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, manager)
	firstReq := httptest.NewRequest(http.MethodPost, "/api/clusters/sync", nil)
	admin := attachServerTestAdminSession(t, st, firstReq)
	firstRec := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "/api/clusters/"+strconvUint(cluster.ID)+"/sync", nil)
	attachServerTestSession(t, st, secondReq, admin.ID)
	secondRec := httptest.NewRecorder()

	firstDone := make(chan int, 1)
	go func() {
		srv.Echo().ServeHTTP(firstRec, firstReq)
		firstDone <- firstRec.Code
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("首个同步请求未进入采集流程")
	}

	secondDone := make(chan int, 1)
	go func() {
		srv.Echo().ServeHTTP(secondRec, secondReq)
		secondDone <- secondRec.Code
	}()

	select {
	case code := <-secondDone:
		if code != http.StatusConflict {
			close(release)
			t.Fatalf("同步进行中应返回 409: got %d", code)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("同步进行中再次触发不应阻塞等待")
	}

	close(release)
	select {
	case code := <-firstDone:
		if code != http.StatusOK {
			t.Fatalf("首个同步请求应成功: got %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("首个同步请求未结束")
	}
}

func TestClusterSyncStatusEndpointShowsGlobalStateAndClusterSource(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "状态测试集群",
		Slug: "status-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	if err := st.ReplaceClusterSyncResources(cluster.ID, []store.ClusterSyncResource{
		{
			ResourceName: "pods",
			APIVersion:   "v1",
			Kind:         "Pod",
			ItemCount:    2,
			FullStatus:   store.SyncStatusSuccess,
			LastSource:   store.SyncSourceManual,
		},
	}); err != nil {
		t.Fatalf("写入同步状态失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, nil)
	nextRun := time.Date(2026, 6, 29, 10, 40, 0, 0, time.Local)
	srv.syncState.setNextRun(nextRun)
	srv.syncState.begin(store.SyncSourceAuto)

	req := httptest.NewRequest(http.MethodGet, "/api/clusters/sync/status", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("同步状态接口应返回 200: got %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Running   bool      `json:"running"`
		Source    string    `json:"source"`
		NextRunAt time.Time `json:"next_run_at"`
		Clusters  []struct {
			ClusterID  uint   `json:"cluster_id"`
			LastSource string `json:"last_source"`
			Status     string `json:"status"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if !body.Running || body.Source != store.SyncSourceAuto {
		t.Fatalf("全局运行状态不正确: %+v", body)
	}
	if !body.NextRunAt.Equal(nextRun) {
		t.Fatalf("下一次自动同步时间不正确: got %s want %s", body.NextRunAt, nextRun)
	}
	if len(body.Clusters) != 1 || body.Clusters[0].ClusterID != cluster.ID ||
		body.Clusters[0].LastSource != store.SyncSourceManual || body.Clusters[0].Status != store.SyncStatusSuccess {
		t.Fatalf("集群最近同步来源不正确: %+v", body.Clusters)
	}
}

func TestClusterSyncStatusMarksDisconnectedHistoryStale(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "断连历史状态集群",
		Slug: "stale-status-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	lastSuccessfulAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := st.ReplaceClusterSyncResources(cluster.ID, []store.ClusterSyncResource{
		{
			ResourceName:        "pods",
			APIVersion:          "v1",
			Kind:                "Pod",
			ItemCount:           2,
			FullFinishedAt:      &lastSuccessfulAt,
			FullStatus:          store.SyncStatusSuccess,
			IncrementalStatus:   store.SyncStatusSuccess,
			LastSource:          store.SyncSourceAuto,
			LastResourceVersion: "20",
		},
	}); err != nil {
		t.Fatalf("写入历史同步状态失败: %v", err)
	}
	task, err := st.StartClusterSyncTask(cluster.ID, store.SyncSourceAuto)
	if err != nil {
		t.Fatalf("创建失败同步任务失败: %v", err)
	}
	errorText := "集群 API Server 连接失败: dial tcp 10.0.0.1:6443: network unreachable"
	if err := st.FinishClusterSyncTask(task.ID, store.SyncStatusFailed, 0, 0, errorText); err != nil {
		t.Fatalf("结束失败同步任务失败: %v", err)
	}
	if err := st.UpdateClusterStatus(cluster.ID, store.ClusterStatusDisconnected, errorText); err != nil {
		t.Fatalf("更新集群断连状态失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/clusters/sync/status", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("同步状态接口应返回 200: got %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Clusters []struct {
			Status            string     `json:"status"`
			ClusterStatus     string     `json:"cluster_status"`
			IncrementalStatus string     `json:"incremental_status"`
			DataStale         bool       `json:"data_stale"`
			LastSuccessfulAt  *time.Time `json:"last_successful_at"`
			LastSyncedAt      *time.Time `json:"last_synced_at"`
			Error             string     `json:"error"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析同步状态响应失败: %v", err)
	}
	if len(body.Clusters) != 1 {
		t.Fatalf("集群同步状态数量不正确: %+v", body.Clusters)
	}
	got := body.Clusters[0]
	if got.Status != store.SyncStatusFailed || got.ClusterStatus != store.ClusterStatusDisconnected {
		t.Fatalf("最新失败任务和集群断连状态不正确: %+v", got)
	}
	if !got.DataStale || got.IncrementalStatus != "not_run" {
		t.Fatalf("断连时应标记历史过期且增量未执行: %+v", got)
	}
	if got.LastSuccessfulAt == nil || !got.LastSuccessfulAt.Equal(lastSuccessfulAt) {
		t.Fatalf("最后成功时间不正确: got %v want %v", got.LastSuccessfulAt, lastSuccessfulAt)
	}
	if got.LastSyncedAt == nil || !strings.Contains(got.Error, "network unreachable") {
		t.Fatalf("最新失败任务时间或原因缺失: %+v", got)
	}
}

func TestClusterSyncHistoryEndpoints(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "历史测试集群",
		Slug: "history-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}
	task, err := st.StartClusterSyncTask(cluster.ID, store.SyncSourceAuto)
	if err != nil {
		t.Fatalf("创建同步任务失败: %v", err)
	}
	if err := st.FinishClusterSyncTask(task.ID, store.SyncStatusSuccess, 1, 1, ""); err != nil {
		t.Fatalf("完成同步任务失败: %v", err)
	}
	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, []store.ClusterResourceSnapshot{
		{
			APIVersion:      "v1",
			Kind:            "Pod",
			ResourceName:    "pods",
			Namespace:       "default",
			Name:            "pod-a",
			UID:             "pod-uid",
			ResourceVersion: "10",
			Status:          "Running",
			ContentHash:     "pod-a-v1",
		},
	}); err != nil {
		t.Fatalf("写入资源快照失败: %v", err)
	}
	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, nil); err != nil {
		t.Fatalf("写入删除历史失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, nil)

	taskReq := httptest.NewRequest(http.MethodGet, "/api/clusters/"+strconvUint(cluster.ID)+"/sync-tasks", nil)
	attachServerTestAdminSession(t, st, taskReq)
	taskRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(taskRec, taskReq)
	if taskRec.Code != http.StatusOK {
		t.Fatalf("同步任务接口应返回 200: got %d body=%s", taskRec.Code, taskRec.Body.String())
	}
	var taskBody struct {
		Count int `json:"count"`
		Tasks []struct {
			Source        string `json:"source"`
			Status        string `json:"status"`
			ResourceCount int    `json:"resource_count"`
			ChangedCount  int    `json:"changed_count"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(taskRec.Body.Bytes(), &taskBody); err != nil {
		t.Fatalf("解析同步任务响应失败: %v", err)
	}
	if taskBody.Count != 1 || len(taskBody.Tasks) != 1 ||
		taskBody.Tasks[0].Source != store.SyncSourceAuto ||
		taskBody.Tasks[0].Status != store.SyncStatusSuccess ||
		taskBody.Tasks[0].ResourceCount != 1 ||
		taskBody.Tasks[0].ChangedCount != 1 {
		t.Fatalf("同步任务响应不正确: %+v", taskBody)
	}

	deletedReq := httptest.NewRequest(http.MethodGet, "/api/clusters/"+strconvUint(cluster.ID)+"/deleted-resources?resource=pods", nil)
	attachServerTestAdminSession(t, st, deletedReq)
	deletedRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(deletedRec, deletedReq)
	if deletedRec.Code != http.StatusOK {
		t.Fatalf("删除历史接口应返回 200: got %d body=%s", deletedRec.Code, deletedRec.Body.String())
	}
	var deletedBody struct {
		Count     int `json:"count"`
		Resources []struct {
			ResourceName string `json:"resource_name"`
			Namespace    string `json:"namespace"`
			Name         string `json:"name"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(deletedRec.Body.Bytes(), &deletedBody); err != nil {
		t.Fatalf("解析删除历史响应失败: %v", err)
	}
	if deletedBody.Count != 1 || len(deletedBody.Resources) != 1 ||
		deletedBody.Resources[0].ResourceName != "pods" ||
		deletedBody.Resources[0].Namespace != "default" ||
		deletedBody.Resources[0].Name != "pod-a" {
		t.Fatalf("删除历史响应不正确: %+v", deletedBody)
	}
}

func strconvUint(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
