package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aiops/internal/store"
)

func TestDashboardSummaryCountsScopedOperationalState(t *testing.T) {
	st := openServerTestStore(t)
	clusterA := createServerTestCluster(t, st, "dashboard-a")
	clusterB := createServerTestCluster(t, st, "dashboard-b")
	if err := st.UpdateClusterStatus(clusterA.ID, store.ClusterStatusReady, ""); err != nil {
		t.Fatalf("更新集群 A 状态失败: %v", err)
	}
	if err := st.UpdateClusterStatus(clusterB.ID, store.ClusterStatusError, "连接失败"); err != nil {
		t.Fatalf("更新集群 B 状态失败: %v", err)
	}
	if _, err := st.CreateTicket(clusterB.ID, &store.ApprovalTicket{
		Action:    "delete_pod",
		Namespace: "default",
		Target:    "pod-b",
		Reason:    "待审批统计",
		Risk:      "low",
	}); err != nil {
		t.Fatalf("创建审批工单失败: %v", err)
	}
	if _, err := st.SaveReport(clusterA.ID, serverTestReport("pod-a")); err != nil {
		t.Fatalf("保存巡检报告失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, clusterA.ID, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询看板摘要失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body dashboardSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析看板摘要失败: %v", err)
	}
	if body.ClusterCount != 2 || body.ReadyClusters != 1 || body.ProblemClusters != 1 {
		t.Fatalf("集群统计不正确: %+v", body)
	}
	if body.PendingApprovals != 1 || body.RecentReports != 1 || body.RecentAnomalies != 1 {
		t.Fatalf("审批和巡检摘要不正确: %+v", body)
	}
}

func TestDashboardSummaryRespectsClusterScope(t *testing.T) {
	st := openServerTestStore(t)
	clusterA := createServerTestCluster(t, st, "dashboard-scope-a")
	clusterB := createServerTestCluster(t, st, "dashboard-scope-b")
	if _, err := st.SaveReport(clusterA.ID, serverTestReport("pod-a")); err != nil {
		t.Fatalf("保存集群 A 巡检报告失败: %v", err)
	}
	if _, err := st.SaveReport(clusterB.ID, serverTestReport("pod-b")); err != nil {
		t.Fatalf("保存集群 B 巡检报告失败: %v", err)
	}
	inspector, err := st.CreateUser(store.CreateUserInput{
		Username:               "dashboard-inspector",
		Password:               "inspector-secret",
		Role:                   store.UserRoleClusterAdmin,
		Status:                 store.UserStatusActive,
		ClusterIDs:             []uint{clusterA.ID},
		PasswordChangeRequired: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("创建巡检执行者失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, clusterA.ID, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/summary", nil)
	attachServerTestSession(t, st, req, inspector.ID)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询授权看板摘要失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body dashboardSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析看板摘要失败: %v", err)
	}
	if body.ClusterCount != 1 || body.RecentReports != 1 || body.PendingApprovals != 0 {
		t.Fatalf("看板摘要应遵守用户集群范围和角色权限: %+v", body)
	}
}
