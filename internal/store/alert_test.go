package store

import (
	"path/filepath"
	"testing"
	"time"

	"aiops/internal/inspect"
)

func anomaly(sev inspect.Severity, kind, ns, name, rule string) inspect.Anomaly {
	return inspect.Anomaly{
		Severity:  sev,
		Kind:      kind,
		Namespace: ns,
		Name:      name,
		Rule:      rule,
		Reason:    rule,
		Message:   name + " " + rule,
	}
}

// TestSyncAlertsForClusterLifecycle 验证告警对账的完整生命周期:
// 新建 → 计数累加 → 异常消失自动恢复 → 复发建新告警 → 全部健康时全部恢复。
func TestSyncAlertsForClusterLifecycle(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	const cid = uint(1)

	// 第 1 轮:两个异常 → 两条活跃告警
	round1 := []inspect.Anomaly{
		anomaly(inspect.SeverityCritical, "Pod", "default", "web-1", "PodCrashLoopBackOff"),
		anomaly(inspect.SeverityWarning, "Pod", "default", "web-2", "PodHighRestarts"),
	}
	if _, _, err := st.SyncAlertsForCluster(cid, round1); err != nil {
		t.Fatalf("round1: %v", err)
	}
	active, err := st.ListAllAlerts(AlertStatusActive, "", 0)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("round1 期望 2 条活跃告警,实际 %d", len(active))
	}

	// 第 2 轮:web-1 仍在(计数 +1),web-2 消失(自动恢复)
	round2 := []inspect.Anomaly{
		anomaly(inspect.SeverityCritical, "Pod", "default", "web-1", "PodCrashLoopBackOff"),
	}
	if _, _, err := st.SyncAlertsForCluster(cid, round2); err != nil {
		t.Fatalf("round2: %v", err)
	}
	active, _ = st.ListAllAlerts(AlertStatusActive, "", 0)
	if len(active) != 1 {
		t.Fatalf("round2 期望 1 条活跃告警,实际 %d", len(active))
	}
	if active[0].Name != "web-1" || active[0].Count != 2 {
		t.Fatalf("round2 期望 web-1 计数=2,实际 name=%s count=%d", active[0].Name, active[0].Count)
	}
	resolved, _ := st.ListAllAlerts(AlertStatusResolved, "", 0)
	if len(resolved) != 1 || resolved[0].Name != "web-2" || resolved[0].ResolvedBy != "system" {
		t.Fatalf("round2 期望 web-2 被系统自动恢复,实际 %+v", resolved)
	}

	// 第 3 轮:web-2 复发 → 应是一条新的活跃告警(不复用已恢复的)
	if _, _, err := st.SyncAlertsForCluster(cid, round1); err != nil {
		t.Fatalf("round3: %v", err)
	}
	active, _ = st.ListAllAlerts(AlertStatusActive, "", 0)
	if len(active) != 2 {
		t.Fatalf("round3 期望 2 条活跃告警(web-2 复发),实际 %d", len(active))
	}

	// 第 4 轮:全部健康(空异常)→ 所有活跃告警自动恢复
	if _, _, err := st.SyncAlertsForCluster(cid, nil); err != nil {
		t.Fatalf("round4: %v", err)
	}
	active, _ = st.ListAllAlerts(AlertStatusActive, "", 0)
	if len(active) != 0 {
		t.Fatalf("round4 期望 0 条活跃告警,实际 %d", len(active))
	}
}

// TestAlertAcknowledgeKeepsStatus 验证已确认的告警在异常仍存在时不会被自动恢复,且计数继续累加。
func TestAlertAcknowledgeKeepsStatus(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	const cid = uint(1)
	node := []inspect.Anomaly{anomaly(inspect.SeverityCritical, "Node", "", "node-a", "NodeNotReady")}
	if _, _, err := st.SyncAlertsForCluster(cid, node); err != nil {
		t.Fatalf("sync: %v", err)
	}
	active, _ := st.ListAllAlerts(AlertStatusActive, "", 0)
	if len(active) != 1 {
		t.Fatalf("期望 1 条活跃告警,实际 %d", len(active))
	}

	a := active[0]
	a.Status = AlertStatusAcknowledged
	a.AcknowledgedBy = "tester"
	if err := st.UpdateAlert(&a); err != nil {
		t.Fatalf("update: %v", err)
	}

	// 异常仍在,下一轮:保持已确认,计数 +1,不被恢复
	if _, _, err := st.SyncAlertsForCluster(cid, node); err != nil {
		t.Fatalf("sync2: %v", err)
	}
	got, err := st.GetAlertByID(a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != AlertStatusAcknowledged {
		t.Fatalf("期望已确认状态保持,实际 %s", got.Status)
	}
	if got.Count != 2 {
		t.Fatalf("期望计数=2,实际 %d", got.Count)
	}
}

// TestSilenceMatchAndExpiry 验证静默匹配语义与过期惰性清理。
func TestSilenceMatchAndExpiry(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cid := uint(1)
	if err := st.CreateSilence(&AlertSilence{
		ClusterID: cid, Rule: "PodCrashLoopBackOff", Namespace: "default",
		CreatedBy: "tester", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create silence: %v", err)
	}
	// 已过期的静默:应被 ListActiveSilences 惰性清理
	if err := st.CreateSilence(&AlertSilence{
		ClusterID: cid, Rule: "PodFailed",
		CreatedBy: "tester", ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("create expired silence: %v", err)
	}

	silences, err := st.ListActiveSilences(nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(silences) != 1 || silences[0].Rule != "PodCrashLoopBackOff" {
		t.Fatalf("期望仅剩 1 条未过期静默,实际 %+v", silences)
	}

	mkAlert := func(cluster uint, rule, ns, name string) Alert {
		return Alert{ClusterID: &cluster, Rule: rule, Namespace: ns, Name: name}
	}
	// 命中:同集群 + rule + namespace 匹配(name 未限定)
	if !MatchSilence(silences, mkAlert(cid, "PodCrashLoopBackOff", "default", "web-1")) {
		t.Fatal("期望命中静默")
	}
	// 不命中:namespace 不同 / rule 不同 / 集群不同
	if MatchSilence(silences, mkAlert(cid, "PodCrashLoopBackOff", "prod", "web-1")) {
		t.Fatal("namespace 不同不应命中")
	}
	if MatchSilence(silences, mkAlert(cid, "PodFailed", "default", "web-1")) {
		t.Fatal("rule 不同不应命中")
	}
	if MatchSilence(silences, mkAlert(uint(2), "PodCrashLoopBackOff", "default", "web-1")) {
		t.Fatal("集群不同不应命中")
	}
}

// TestNotificationSettingUpsert 验证通知设置单行 upsert 与读取。
func TestNotificationSettingUpsert(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	got, err := st.GetNotificationSetting()
	if err != nil || got != nil {
		t.Fatalf("初始应无设置,got=%+v err=%v", got, err)
	}
	if err := st.SaveNotificationSetting(&NotificationSetting{
		Enabled: true, MinSeverity: "warning", NotifyResolved: true, WeComWebhook: "http://a",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := st.SaveNotificationSetting(&NotificationSetting{
		Enabled: true, MinSeverity: "critical", NotifyResolved: false, WeComWebhook: "http://b",
	}); err != nil {
		t.Fatalf("save2: %v", err)
	}
	got, err = st.GetNotificationSetting()
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != 1 || got.MinSeverity != "critical" || got.WeComWebhook != "http://b" || got.NotifyResolved {
		t.Fatalf("upsert 结果不符: %+v", got)
	}
}

// TestSyncAlertsReturnsDelta 验证对账返回的「新增/恢复」增量,供外部通知消费。
func TestSyncAlertsReturnsDelta(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	const cid = uint(1)

	created, resolved, err := st.SyncAlertsForCluster(cid, []inspect.Anomaly{
		anomaly(inspect.SeverityCritical, "Pod", "default", "web-1", "PodCrashLoopBackOff"),
	})
	if err != nil {
		t.Fatalf("round1: %v", err)
	}
	if len(created) != 1 || len(resolved) != 0 {
		t.Fatalf("首轮期望 created=1 resolved=0,实际 created=%d resolved=%d", len(created), len(resolved))
	}

	// 同一异常再来一轮:无新增、无恢复
	created, resolved, err = st.SyncAlertsForCluster(cid, []inspect.Anomaly{
		anomaly(inspect.SeverityCritical, "Pod", "default", "web-1", "PodCrashLoopBackOff"),
	})
	if err != nil {
		t.Fatalf("round2: %v", err)
	}
	if len(created) != 0 || len(resolved) != 0 {
		t.Fatalf("次轮期望无增量,实际 created=%d resolved=%d", len(created), len(resolved))
	}

	// 异常消失:应恢复 1 条
	created, resolved, err = st.SyncAlertsForCluster(cid, nil)
	if err != nil {
		t.Fatalf("round3: %v", err)
	}
	if len(created) != 0 || len(resolved) != 1 {
		t.Fatalf("末轮期望 created=0 resolved=1,实际 created=%d resolved=%d", len(created), len(resolved))
	}
}
