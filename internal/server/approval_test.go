package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/remediate"
	"aiops/internal/store"
)

func TestApproveTicketUsesTicketClusterWhenRequestHasNoClusterID(t *testing.T) {
	st := openServerTestStore(t)
	clusterA := createServerTestCluster(t, st, "approval-a")
	clusterB := createServerTestCluster(t, st, "approval-b")
	ticket, err := st.CreateTicket(clusterB.ID, &store.ApprovalTicket{
		Action:    "delete_pod",
		Namespace: "default",
		Target:    "target-pod",
		Reason:    "测试跨集群审批",
		Risk:      "low",
	})
	if err != nil {
		t.Fatalf("创建审批工单失败: %v", err)
	}

	kubeClient := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "target-pod", Namespace: "default"},
	})
	manager := clusterruntime.NewManager()
	if err := manager.Register(&clusterruntime.Runtime{
		ClusterID:  clusterB.ID,
		Collector:  collector.New(kubeClient),
		Remediator: remediate.New(kubeClient, true),
		Cluster:    clusterB,
	}); err != nil {
		t.Fatalf("注册集群 B 运行时失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, clusterA.ID, manager)
	req := httptest.NewRequest(http.MethodPost, "/api/approvals/"+strconvUint(ticket.ID)+"/approve", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("批准工单失败: code=%d body=%s", rec.Code, rec.Body.String())
	}

	updated, err := st.GetTicketByID(ticket.ID)
	if err != nil {
		t.Fatalf("读取审批后工单失败: %v", err)
	}
	if updated.Status != store.TicketExecuted || updated.ClusterID == nil || *updated.ClusterID != clusterB.ID {
		t.Fatalf("工单应按自身集群执行成功: %+v", updated)
	}
	audits, err := st.ListAllAudits(100)
	if err != nil {
		t.Fatalf("查询审计失败: %v", err)
	}
	if len(audits) != 1 || audits[0].ClusterID == nil || *audits[0].ClusterID != clusterB.ID {
		t.Fatalf("审计应写入工单所属集群: %+v", audits)
	}
}

func TestListApprovalsWithoutClusterIDReturnsAllClusters(t *testing.T) {
	st := openServerTestStore(t)
	clusterA := createServerTestCluster(t, st, "approval-list-a")
	clusterB := createServerTestCluster(t, st, "approval-list-b")
	if _, err := st.CreateTicket(clusterB.ID, &store.ApprovalTicket{
		Action:    "restart_pod",
		Namespace: "default",
		Target:    "pod-b",
		Reason:    "测试全局列表",
		Risk:      "low",
	}); err != nil {
		t.Fatalf("创建审批工单失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, clusterA.ID, clusterruntime.NewManager())
	req := httptest.NewRequest(http.MethodGet, "/api/approvals?status="+store.TicketPending, nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询审批列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Count   int                    `json:"count"`
		Tickets []store.ApprovalTicket `json:"tickets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析审批列表失败: %v", err)
	}
	if body.Count != 1 || len(body.Tickets) != 1 || body.Tickets[0].ClusterID == nil || *body.Tickets[0].ClusterID != clusterB.ID {
		t.Fatalf("未指定集群时应返回全部集群工单: %+v", body)
	}
}

func TestListApprovalsCanFilterByApprover(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "approval-filter")
	if _, err := st.CreateTicket(cluster.ID, &store.ApprovalTicket{
		Action:    "delete_pod",
		Namespace: "default",
		Target:    "pod-a",
		Reason:    "审批人过滤 A",
		Risk:      "low",
		Status:    store.TicketExecuted,
		Approver:  "ops-a",
	}); err != nil {
		t.Fatalf("创建审批工单 A 失败: %v", err)
	}
	if _, err := st.CreateTicket(cluster.ID, &store.ApprovalTicket{
		Action:    "delete_pod",
		Namespace: "default",
		Target:    "pod-b",
		Reason:    "审批人过滤 B",
		Risk:      "low",
		Status:    store.TicketExecuted,
		Approver:  "ops-b",
	}); err != nil {
		t.Fatalf("创建审批工单 B 失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, clusterruntime.NewManager())
	req := httptest.NewRequest(http.MethodGet, "/api/approvals?approver=ops-a", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("按审批人查询审批列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Count   int                    `json:"count"`
		Tickets []store.ApprovalTicket `json:"tickets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析审批列表失败: %v", err)
	}
	if body.Count != 1 || len(body.Tickets) != 1 || body.Tickets[0].Approver != "ops-a" {
		t.Fatalf("审批人过滤结果不正确: %+v", body)
	}
}

func TestListApprovalsReturnsApproverDisplayName(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "approval-display")
	approver, err := st.CreateUser(store.CreateUserInput{
		Username:               "ops-admin",
		DisplayName:            "运维管理员",
		Password:               "admin-secret",
		Role:                   store.UserRoleGlobalAdmin,
		Status:                 store.UserStatusActive,
		PasswordChangeRequired: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("创建审批用户失败: %v", err)
	}
	if _, err := st.CreateTicket(cluster.ID, &store.ApprovalTicket{
		Action:    "delete_pod",
		Namespace: "default",
		Target:    "pod-a",
		Reason:    "审批人展示名",
		Risk:      "low",
		Status:    store.TicketExecuted,
		Approver:  approver.Username,
	}); err != nil {
		t.Fatalf("创建审批工单失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, clusterruntime.NewManager())
	req := httptest.NewRequest(http.MethodGet, "/api/approvals", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询审批列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Tickets []struct {
			Approver            string `json:"approver"`
			ApproverDisplayName string `json:"approver_display_name"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析审批列表失败: %v", err)
	}
	if len(body.Tickets) != 1 || body.Tickets[0].Approver != "ops-admin" || body.Tickets[0].ApproverDisplayName != "运维管理员" {
		t.Fatalf("审批人展示名不正确: %+v", body)
	}
}

func TestListAuditsReturnsClusterAndOperatorDisplayName(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "audit-display")
	operator, err := st.CreateUser(store.CreateUserInput{
		Username:               "audit-admin",
		DisplayName:            "审计管理员",
		Password:               "admin-secret",
		Role:                   store.UserRoleGlobalAdmin,
		Status:                 store.UserStatusActive,
		PasswordChangeRequired: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("创建审计用户失败: %v", err)
	}
	if err := st.AddAudit(cluster.ID, &store.AuditLog{
		Action:   "delete_pod",
		Operator: operator.Username,
		Target:   "default/pod-a",
		Detail:   "审计展示名",
		Result:   "ok",
	}); err != nil {
		t.Fatalf("创建审计日志失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, clusterruntime.NewManager())
	req := httptest.NewRequest(http.MethodGet, "/api/audits", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询审计列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Audits []struct {
			ClusterName         string `json:"cluster_name"`
			Operator            string `json:"operator"`
			OperatorDisplayName string `json:"operator_display_name"`
		} `json:"audits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析审计列表失败: %v", err)
	}
	if len(body.Audits) != 1 || body.Audits[0].ClusterName != cluster.Name || body.Audits[0].Operator != "audit-admin" || body.Audits[0].OperatorDisplayName != "审计管理员" {
		t.Fatalf("审计展示信息不正确: %+v", body)
	}
}

func TestListAuditsCanFilterByOperatorAndAction(t *testing.T) {
	st := openServerTestStore(t)
	cluster := createServerTestCluster(t, st, "audit-filter")
	if err := st.AddAudit(cluster.ID, &store.AuditLog{
		Action:   "delete_pod",
		Operator: "ops-a",
		Target:   "default/pod-a",
		Result:   "ok",
	}); err != nil {
		t.Fatalf("创建审计日志 A 失败: %v", err)
	}
	if err := st.AddAudit(cluster.ID, &store.AuditLog{
		Action:   "restart_pod",
		Operator: "ops-b",
		Target:   "default/pod-b",
		Result:   "ok",
	}); err != nil {
		t.Fatalf("创建审计日志 B 失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, clusterruntime.NewManager())
	req := httptest.NewRequest(http.MethodGet, "/api/audits?operator=ops-a&action=delete_pod", nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("按操作者和动作查询审计失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Count  int              `json:"count"`
		Audits []store.AuditLog `json:"audits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析审计列表失败: %v", err)
	}
	if body.Count != 1 || len(body.Audits) != 1 || body.Audits[0].Operator != "ops-a" || body.Audits[0].Action != "delete_pod" {
		t.Fatalf("审计过滤结果不正确: %+v", body)
	}
}
