package store

import (
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/collector"
	"aiops/internal/inspect"
)

func TestReportsAreScopedToCluster(t *testing.T) {
	st := openTestStore(t)

	clusterA := createTestCluster(t, st, "cluster-a")
	clusterB := createTestCluster(t, st, "cluster-b")

	reportA, err := st.SaveReport(clusterA.ID, newTestReport("pod-a"))
	if err != nil {
		t.Fatalf("保存集群 A 报告失败: %v", err)
	}
	reportB, err := st.SaveReport(clusterB.ID, newTestReport("pod-b"))
	if err != nil {
		t.Fatalf("保存集群 B 报告失败: %v", err)
	}

	reports, err := st.ListReports(clusterA.ID, 50)
	if err != nil {
		t.Fatalf("查询集群 A 报告失败: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("集群 A 报告数量不正确: got %d want 1", len(reports))
	}
	if reports[0].ID != reportA.ID || reports[0].ClusterID == nil || *reports[0].ClusterID != clusterA.ID {
		t.Fatalf("集群 A 报告归属不正确: %+v", reports[0])
	}

	got, err := st.GetReport(clusterA.ID, reportA.ID)
	if err != nil {
		t.Fatalf("查询集群 A 报告详情失败: %v", err)
	}
	if len(got.Anomalies) != 1 || got.Anomalies[0].ClusterID == nil || *got.Anomalies[0].ClusterID != clusterA.ID {
		t.Fatalf("异常记录归属不正确: %+v", got.Anomalies)
	}
	if _, err := st.GetReport(clusterA.ID, reportB.ID); err == nil {
		t.Fatal("不应跨集群读取报告详情")
	}

	allReports, err := st.ListAllReports(50)
	if err != nil {
		t.Fatalf("查询全部报告失败: %v", err)
	}
	if len(allReports) != 2 {
		t.Fatalf("全部报告数量不正确: got %d want 2", len(allReports))
	}
}

func TestTicketsAndAuditsAreScopedToCluster(t *testing.T) {
	st := openTestStore(t)

	clusterA := createTestCluster(t, st, "cluster-a")
	clusterB := createTestCluster(t, st, "cluster-b")

	ticket, err := st.CreateTicket(clusterA.ID, &ApprovalTicket{
		Action:    "restart_pod",
		Namespace: "default",
		Target:    "pod-a",
		Reason:    "测试",
		Risk:      "low",
	})
	if err != nil {
		t.Fatalf("创建工单失败: %v", err)
	}
	if ticket.ClusterID == nil || *ticket.ClusterID != clusterA.ID {
		t.Fatalf("工单归属不正确: %+v", ticket)
	}

	tickets, err := st.ListTickets(clusterA.ID, TicketPending, 100)
	if err != nil {
		t.Fatalf("查询集群 A 工单失败: %v", err)
	}
	if len(tickets) != 1 || tickets[0].ID != ticket.ID {
		t.Fatalf("集群 A 工单列表不正确: %+v", tickets)
	}
	otherTickets, err := st.ListTickets(clusterB.ID, "", 100)
	if err != nil {
		t.Fatalf("查询集群 B 工单失败: %v", err)
	}
	if len(otherTickets) != 0 {
		t.Fatalf("不应跨集群返回工单: %+v", otherTickets)
	}
	if _, err := st.GetTicket(clusterB.ID, ticket.ID); err == nil {
		t.Fatal("不应跨集群读取工单详情")
	}
	if got, err := st.GetTicketByID(ticket.ID); err != nil || got.ClusterID == nil || *got.ClusterID != clusterA.ID {
		t.Fatalf("应能按工单 ID 读取其固化集群归属: ticket=%+v err=%v", got, err)
	}

	allTickets, err := st.ListAllTickets(TicketPending, 100)
	if err != nil {
		t.Fatalf("查询全部工单失败: %v", err)
	}
	if len(allTickets) != 1 || allTickets[0].ID != ticket.ID {
		t.Fatalf("全部工单列表不正确: %+v", allTickets)
	}

	if err := st.AddAudit(clusterA.ID, &AuditLog{
		Action:   "restart_pod",
		Operator: "tester",
		Target:   "default/pod-a",
		Result:   "ok",
	}); err != nil {
		t.Fatalf("写入审计失败: %v", err)
	}
	audits, err := st.ListAudits(clusterA.ID, 100)
	if err != nil {
		t.Fatalf("查询集群 A 审计失败: %v", err)
	}
	if len(audits) != 1 || audits[0].ClusterID == nil || *audits[0].ClusterID != clusterA.ID {
		t.Fatalf("集群 A 审计归属不正确: %+v", audits)
	}
	otherAudits, err := st.ListAudits(clusterB.ID, 100)
	if err != nil {
		t.Fatalf("查询集群 B 审计失败: %v", err)
	}
	if len(otherAudits) != 0 {
		t.Fatalf("不应跨集群返回审计: %+v", otherAudits)
	}

	allAudits, err := st.ListAllAudits(100)
	if err != nil {
		t.Fatalf("查询全部审计失败: %v", err)
	}
	if len(allAudits) != 1 || allAudits[0].ClusterID == nil || *allAudits[0].ClusterID != clusterA.ID {
		t.Fatalf("全部审计列表不正确: %+v", allAudits)
	}
}

func TestBackfillDefaultClusterAssignsLegacyRows(t *testing.T) {
	st := openTestStore(t)
	cluster := createTestCluster(t, st, "default-legacy")

	legacyReport := &ReportRecord{
		StartedAt:    time.Now(),
		FinishedAt:   time.Now(),
		NodeCount:    1,
		PodCount:     1,
		AnomalyCount: 1,
		Anomalies: []AnomalyRecord{
			{Severity: string(inspect.SeverityWarning), Kind: "Pod", Namespace: "default", Name: "legacy-pod"},
		},
	}
	if err := st.db.Create(legacyReport).Error; err != nil {
		t.Fatalf("写入历史报告失败: %v", err)
	}
	if err := st.db.Create(&ApprovalTicket{Action: "restart_pod", Namespace: "default", Target: "legacy-pod", Status: TicketPending}).Error; err != nil {
		t.Fatalf("写入历史工单失败: %v", err)
	}
	if err := st.db.Create(&AuditLog{Action: "legacy", Operator: "tester", Target: "default/legacy-pod", Result: "ok"}).Error; err != nil {
		t.Fatalf("写入历史审计失败: %v", err)
	}

	if err := st.BackfillDefaultCluster(cluster.ID); err != nil {
		t.Fatalf("回填默认集群失败: %v", err)
	}

	reports, err := st.ListReports(cluster.ID, 50)
	if err != nil {
		t.Fatalf("查询回填报告失败: %v", err)
	}
	if len(reports) != 1 || reports[0].ClusterID == nil || *reports[0].ClusterID != cluster.ID {
		t.Fatalf("报告回填不正确: %+v", reports)
	}
	got, err := st.GetReport(cluster.ID, legacyReport.ID)
	if err != nil {
		t.Fatalf("查询回填报告详情失败: %v", err)
	}
	if len(got.Anomalies) != 1 || got.Anomalies[0].ClusterID == nil || *got.Anomalies[0].ClusterID != cluster.ID {
		t.Fatalf("异常记录回填不正确: %+v", got.Anomalies)
	}
	tickets, err := st.ListTickets(cluster.ID, "", 100)
	if err != nil {
		t.Fatalf("查询回填工单失败: %v", err)
	}
	if len(tickets) != 1 || tickets[0].ClusterID == nil || *tickets[0].ClusterID != cluster.ID {
		t.Fatalf("工单回填不正确: %+v", tickets)
	}
	audits, err := st.ListAudits(cluster.ID, 100)
	if err != nil {
		t.Fatalf("查询回填审计失败: %v", err)
	}
	if len(audits) != 1 || audits[0].ClusterID == nil || *audits[0].ClusterID != cluster.ID {
		t.Fatalf("审计回填不正确: %+v", audits)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func createTestCluster(t *testing.T, st *Store, slug string) *Cluster {
	t.Helper()
	cluster, err := st.CreateCluster(CreateClusterInput{
		Name: slug,
		Slug: slug,
		Mode: ClusterModeKubeconfig,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	return cluster
}

func newTestReport(podName string) *inspect.Report {
	now := time.Now()
	return &inspect.Report{
		StartedAt:  now.Add(-time.Second),
		FinishedAt: now,
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
