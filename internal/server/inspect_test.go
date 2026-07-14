package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/inspect"
	"aiops/internal/store"
)

func TestInspectRunWithoutClusterIDRunsAllClustersAndStoresReports(t *testing.T) {
	st := openServerTestStore(t)
	clusterA := createServerTestCluster(t, st, "inspect-a")
	clusterB := createServerTestCluster(t, st, "inspect-b")

	manager := clusterruntime.NewManager()
	registerInspectRuntime(t, manager, clusterA.ID, "node-a")
	registerInspectRuntime(t, manager, clusterB.ID, "node-b")

	srv := NewServer(nil, nil, nil, st, nil, nil, clusterA.ID, manager)
	req := httptest.NewRequest(http.MethodPost, "/api/inspect/run", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("全量巡检请求失败: code=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Count    int `json:"count"`
		Clusters []struct {
			ClusterID   uint            `json:"cluster_id"`
			ClusterName string          `json:"cluster_name"`
			Report      *inspect.Report `json:"report"`
			Error       string          `json:"error"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析全量巡检响应失败: %v", err)
	}
	if body.Count != 2 || len(body.Clusters) != 2 {
		t.Fatalf("全量巡检应返回两个集群结果: %+v", body)
	}
	for _, item := range body.Clusters {
		if item.Error != "" || item.Report == nil {
			t.Fatalf("集群巡检结果不正确: %+v", item)
		}
		if len(item.Report.ResourceCache) == 0 {
			t.Fatalf("巡检报告应包含资源缓存摘要: %+v", item.Report)
		}
	}

	reports, err := st.ListAllReports(50)
	if err != nil {
		t.Fatalf("查询全部巡检报告失败: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("全量巡检应落库两个报告: got %d want 2", len(reports))
	}
}

func TestInspectReportsCanBeListedAcrossAllOrOneCluster(t *testing.T) {
	st := openServerTestStore(t)
	clusterA := createServerTestCluster(t, st, "reports-a")
	clusterB := createServerTestCluster(t, st, "reports-b")
	if _, err := st.SaveReport(clusterA.ID, serverTestReport("pod-a")); err != nil {
		t.Fatalf("保存集群 A 报告失败: %v", err)
	}
	if _, err := st.SaveReport(clusterB.ID, serverTestReport("pod-b")); err != nil {
		t.Fatalf("保存集群 B 报告失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, clusterA.ID, clusterruntime.NewManager())
	allReports := requestReportList(t, srv, "/api/inspect/reports")
	if len(allReports) != 2 {
		t.Fatalf("未指定集群时应返回全部报告: %+v", allReports)
	}
	clusterAReports := requestReportList(t, srv, "/api/inspect/reports?cluster_id="+strconvUint(clusterA.ID))
	if len(clusterAReports) != 1 || clusterAReports[0].ClusterID == nil || *clusterAReports[0].ClusterID != clusterA.ID {
		t.Fatalf("指定集群时应只返回该集群报告: %+v", clusterAReports)
	}
}

func TestInspectReportsRespectClusterAuthorization(t *testing.T) {
	st := openServerTestStore(t)
	clusterA := createServerTestCluster(t, st, "reports-auth-a")
	clusterB := createServerTestCluster(t, st, "reports-auth-b")
	reportA, err := st.SaveReport(clusterA.ID, serverTestReport("pod-a"))
	if err != nil {
		t.Fatalf("保存集群 A 报告失败: %v", err)
	}
	reportB, err := st.SaveReport(clusterB.ID, serverTestReport("pod-b"))
	if err != nil {
		t.Fatalf("保存集群 B 报告失败: %v", err)
	}
	inspector, err := st.CreateUser(store.CreateUserInput{
		Username:               "reports-inspector",
		Password:               "inspector-secret",
		Role:                   store.UserRoleClusterAdmin,
		Status:                 store.UserStatusActive,
		ClusterIDs:             []uint{clusterA.ID},
		PasswordChangeRequired: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("创建巡检执行者失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, clusterA.ID, clusterruntime.NewManager())
	req := httptest.NewRequest(http.MethodGet, "/api/inspect/reports", nil)
	attachServerTestSession(t, st, req, inspector.ID)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询授权报告列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Reports []store.ReportRecord `json:"reports"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析报告列表失败: %v", err)
	}
	if len(body.Reports) != 1 || body.Reports[0].ID != reportA.ID {
		t.Fatalf("巡检执行者只能看到授权集群报告: %+v", body.Reports)
	}

	deniedReq := httptest.NewRequest(http.MethodGet, "/api/inspect/reports/"+strconvUint(reportB.ID), nil)
	attachServerTestSession(t, st, deniedReq, inspector.ID)
	deniedRec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("巡检执行者不应读取未授权集群报告详情: code=%d body=%s", deniedRec.Code, deniedRec.Body.String())
	}
}

func openServerTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func createServerTestCluster(t *testing.T, st *store.Store, slug string) *store.Cluster {
	t.Helper()
	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: slug,
		Slug: slug,
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	return cluster
}

func registerInspectRuntime(t *testing.T, manager *clusterruntime.Manager, clusterID uint, nodeName string) {
	t.Helper()
	kubeClient := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		},
	)
	col := collector.New(kubeClient)
	if err := manager.Register(&clusterruntime.Runtime{
		ClusterID:  clusterID,
		Collector:  col,
		Engine:     inspect.NewEngine(col, nil),
		Remediator: nil,
	}); err != nil {
		t.Fatalf("注册巡检运行时失败: %v", err)
	}
}

func requestReportList(t *testing.T, srv *Server, target string) []store.ReportRecord {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	attachServerTestAdminSession(t, srv.store, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询报告列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Reports []store.ReportRecord `json:"reports"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析报告列表失败: %v", err)
	}
	return body.Reports
}

func serverTestReport(podName string) *inspect.Report {
	return &inspect.Report{
		Overview: &collector.ClusterOverview{
			Nodes:       1,
			NodesReady:  1,
			Pods:        1,
			Namespaces:  1,
			PodsByPhase: map[string]int{"Running": 1},
		},
		Anomalies: []inspect.Anomaly{
			{
				Severity:  inspect.SeverityWarning,
				Kind:      "Pod",
				Namespace: "default",
				Name:      podName,
				Rule:      "test",
				Reason:    "test",
				Message:   "测试异常",
			},
		},
		AnomalyCount: 1,
	}
}
