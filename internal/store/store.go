package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"aiops/internal/inspect"
)

func clusterIDPtr(clusterID uint) (*uint, error) {
	if clusterID == 0 {
		return nil, fmt.Errorf("clusterID 不能为空")
	}
	return &clusterID, nil
}

// Store 封装数据库访问。
type Store struct {
	db *gorm.DB
}

// Open 打开数据库并自动建表。driver 支持 sqlite(纯 Go,默认) 与 mysql。
func Open(driver, dsn string) (*Store, error) {
	var dialector gorm.Dialector
	switch driver {
	case "mysql":
		dialector = mysql.Open(dsn)
	default:
		dialector = sqlite.Open(dsn)
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if err := db.AutoMigrate(
		&Cluster{},
		&ClusterCredential{},
		&ClusterPolicy{},
		&ClusterSyncTask{},
		&ClusterSyncResource{},
		&ClusterResourceSnapshot{},
		&ClusterDeletedResource{},
		&ModelProfile{},
		&User{},
		&UserClusterGrant{},
		&AuthSession{},
		&ReportRecord{},
		&AnomalyRecord{},
		&ApprovalTicket{},
		&AuditLog{},
		&Alert{},
		&AlertSilence{},
		&NotificationSetting{},
		&InspectionRuleSetting{},
		&GlobalPrometheusSetting{},
		&CustomMetricRule{},
	); err != nil {
		return nil, fmt.Errorf("建表失败: %w", err)
	}
	// 迁移:巡检规则设置由单列唯一(rule_name)升级为复合唯一(rule_name+cluster_id),
	// 需清理历史单列唯一索引,否则同一规则无法建立集群覆盖行。新库无此索引,失败忽略。
	_ = db.Exec("DROP INDEX IF EXISTS idx_inspection_rule_settings_rule_name").Error                   // SQLite
	_ = db.Exec("DROP INDEX idx_inspection_rule_settings_rule_name ON inspection_rule_settings").Error // MySQL 方言
	return &Store{db: db}, nil
}

// Close 关闭底层数据库连接,主要用于测试和优雅退出。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// ---- 巡检报告 ----

// SaveReport 持久化一次巡检报告(含异常与 AI 诊断)。
func (s *Store) SaveReport(clusterID uint, r *inspect.Report) (*ReportRecord, error) {
	clusterIDValue, err := clusterIDPtr(clusterID)
	if err != nil {
		return nil, err
	}
	rec := &ReportRecord{
		ClusterID:    clusterIDValue,
		StartedAt:    r.StartedAt,
		FinishedAt:   r.FinishedAt,
		AnomalyCount: r.AnomalyCount,
	}
	if r.Overview != nil {
		rec.NodeCount = r.Overview.Nodes
		rec.PodCount = r.Overview.Pods
	}
	for _, a := range r.Anomalies {
		rec.Anomalies = append(rec.Anomalies, AnomalyRecord{
			ClusterID: clusterIDValue,
			Severity:  string(a.Severity),
			Kind:      a.Kind,
			Namespace: a.Namespace,
			Name:      a.Name,
			Rule:      a.Rule,
			Reason:    a.Reason,
			Message:   a.Message,
			Diagnosis: a.Diagnosis,
		})
	}
	if err := s.db.Create(rec).Error; err != nil {
		return nil, err
	}
	return rec, nil
}

// ListReports 返回最近的报告列表(不含异常明细)。
func (s *Store) ListReports(clusterID uint, limit int) ([]ReportRecord, error) {
	var recs []ReportRecord
	q := s.db.Where("cluster_id = ?", clusterID).Order("id desc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&recs).Error
	return recs, err
}

// ListAllReports 返回所有集群最近的报告列表(不含异常明细)。
func (s *Store) ListAllReports(limit int) ([]ReportRecord, error) {
	var recs []ReportRecord
	q := s.db.Order("id desc")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&recs).Error
	return recs, err
}

// GetReport 返回单个报告(含异常明细)。
func (s *Store) GetReport(clusterID uint, id uint) (*ReportRecord, error) {
	var rec ReportRecord
	if err := s.db.Preload("Anomalies", "cluster_id = ?", clusterID).
		Where("id = ? AND cluster_id = ?", id, clusterID).
		First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// GetReportByID 按报告 ID 读取报告,用于全局报告列表的详情跳转。
func (s *Store) GetReportByID(id uint) (*ReportRecord, error) {
	var rec ReportRecord
	if err := s.db.Preload("Anomalies").Where("id = ?", id).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// ---- 审批工单 ----

// CreateTicket 创建审批工单(状态默认 pending)。
func (s *Store) CreateTicket(clusterID uint, t *ApprovalTicket) (*ApprovalTicket, error) {
	clusterIDValue, err := clusterIDPtr(clusterID)
	if err != nil {
		return nil, err
	}
	t.ClusterID = clusterIDValue
	if t.Status == "" {
		t.Status = TicketPending
	}
	if err := s.db.Create(t).Error; err != nil {
		return nil, err
	}
	return t, nil
}

// ListTickets 返回工单列表。status 为空表示全部,否则按状态过滤。
func (s *Store) ListTickets(clusterID uint, status string, limit int) ([]ApprovalTicket, error) {
	var ts []ApprovalTicket
	q := s.db.Where("cluster_id = ?", clusterID).Order("id desc")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&ts).Error
	return ts, err
}

// ListAllTickets 返回所有集群的工单列表。status 为空表示全部。
func (s *Store) ListAllTickets(status string, limit int) ([]ApprovalTicket, error) {
	var ts []ApprovalTicket
	q := s.db.Order("id desc")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&ts).Error
	return ts, err
}

// GetTicket 返回单个工单。
func (s *Store) GetTicket(clusterID uint, id uint) (*ApprovalTicket, error) {
	var t ApprovalTicket
	if err := s.db.Where("id = ? AND cluster_id = ?", id, clusterID).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTicketByID 按工单 ID 读取工单,用于审批执行时以工单固化的集群归属为准。
func (s *Store) GetTicketByID(id uint) (*ApprovalTicket, error) {
	var t ApprovalTicket
	if err := s.db.Where("id = ?", id).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTicket 更新工单。
func (s *Store) UpdateTicket(t *ApprovalTicket) error {
	return s.db.Save(t).Error
}

// ---- 审计日志 ----

// AddAudit 写入一条审计日志。
func (s *Store) AddAudit(clusterID uint, a *AuditLog) error {
	clusterIDValue, err := clusterIDPtr(clusterID)
	if err != nil {
		return err
	}
	a.ClusterID = clusterIDValue
	return s.db.Create(a).Error
}

// ListAudits 返回最近的审计日志。
func (s *Store) ListAudits(clusterID uint, limit int) ([]AuditLog, error) {
	return s.ListAuditsFiltered(clusterID, "", "", limit)
}

// ListAuditsFiltered 返回指定集群的审计日志,可按操作者与动作过滤。
func (s *Store) ListAuditsFiltered(clusterID uint, operator, action string, limit int) ([]AuditLog, error) {
	var as []AuditLog
	q := s.db.Where("cluster_id = ?", clusterID).Order("id desc")
	q = applyAuditFilters(q, operator, action)
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&as).Error
	return as, err
}

// ListAllAudits 返回所有集群最近的审计日志。
func (s *Store) ListAllAudits(limit int) ([]AuditLog, error) {
	return s.ListAllAuditsFiltered("", "", limit)
}

// ListAllAuditsFiltered 返回所有集群的审计日志,可按操作者与动作过滤。
func (s *Store) ListAllAuditsFiltered(operator, action string, limit int) ([]AuditLog, error) {
	var as []AuditLog
	q := s.db.Order("id desc")
	q = applyAuditFilters(q, operator, action)
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&as).Error
	return as, err
}

func applyAuditFilters(q *gorm.DB, operator, action string) *gorm.DB {
	if operator = strings.TrimSpace(operator); operator != "" {
		q = q.Where("operator = ?", operator)
	}
	if action = strings.TrimSpace(action); action != "" {
		q = q.Where("action = ?", action)
	}
	return q
}

// ---- 告警 ----

// alertFingerprint 生成告警去重指纹:集群 + 资源 + 规则。
func alertFingerprint(clusterID uint, a inspect.Anomaly) string {
	return fmt.Sprintf("%d|%s|%s|%s|%s", clusterID, a.Kind, a.Namespace, a.Name, a.Rule)
}

// SyncAlertsForCluster 用一轮巡检的异常对指定集群做对账式 upsert:
// 新指纹→新建告警;已存在→更新并计数;本轮缺失的活跃/已确认告警→自动置为已恢复。
// 注意:仅应在巡检成功(拿到完整异常快照)时调用,否则会误判恢复。
func (s *Store) SyncAlertsForCluster(clusterID uint, anomalies []inspect.Anomaly) (created, resolved []Alert, err error) {
	clusterIDValue, err := clusterIDPtr(clusterID)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	seen := make(map[string]struct{}, len(anomalies))

	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		created, resolved = nil, nil // 事务若重试则重置增量结果
		for _, a := range anomalies {
			fp := alertFingerprint(clusterID, a)
			seen[fp] = struct{}{}
			var existing Alert
			findErr := tx.Where("cluster_id = ? AND fingerprint = ? AND status <> ?", clusterID, fp, AlertStatusResolved).
				First(&existing).Error
			if findErr == nil {
				existing.Severity = string(a.Severity)
				existing.Reason = a.Reason
				existing.Message = a.Message
				existing.LastSeenAt = now
				existing.Count++
				if err := tx.Save(&existing).Error; err != nil {
					return err
				}
				continue
			}
			if !errors.Is(findErr, gorm.ErrRecordNotFound) {
				return findErr
			}
			alert := &Alert{
				ClusterID:   clusterIDValue,
				Fingerprint: fp,
				Severity:    string(a.Severity),
				Kind:        a.Kind,
				Namespace:   a.Namespace,
				Name:        a.Name,
				Rule:        a.Rule,
				Reason:      a.Reason,
				Message:     a.Message,
				Status:      AlertStatusActive,
				Count:       1,
				FirstSeenAt: now,
				LastSeenAt:  now,
			}
			if err := tx.Create(alert).Error; err != nil {
				return err
			}
			created = append(created, *alert)
		}
		// 本轮巡检未再出现的活跃/已确认告警 → 自动恢复
		var open []Alert
		if err := tx.Where("cluster_id = ? AND status IN ?", clusterID, []string{AlertStatusActive, AlertStatusAcknowledged}).
			Find(&open).Error; err != nil {
			return err
		}
		for i := range open {
			if _, ok := seen[open[i].Fingerprint]; ok {
				continue
			}
			open[i].Status = AlertStatusResolved
			open[i].ResolvedBy = "system"
			open[i].ResolvedAt = &now
			if err := tx.Save(&open[i]).Error; err != nil {
				return err
			}
			resolved = append(resolved, open[i])
		}
		return nil
	})
	if txErr != nil {
		return nil, nil, txErr
	}
	return created, resolved, nil
}

// ListAlerts 返回指定集群的告警。status/severity 为空表示不过滤。
func (s *Store) ListAlerts(clusterID uint, status, severity string, limit int) ([]Alert, error) {
	var alerts []Alert
	q := s.db.Where("cluster_id = ?", clusterID).Order("id desc")
	q = applyAlertFilters(q, status, severity)
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&alerts).Error
	return alerts, err
}

// ListAllAlerts 返回所有集群的告警。status/severity 为空表示不过滤。
func (s *Store) ListAllAlerts(status, severity string, limit int) ([]Alert, error) {
	var alerts []Alert
	q := s.db.Order("id desc")
	q = applyAlertFilters(q, status, severity)
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&alerts).Error
	return alerts, err
}

func applyAlertFilters(q *gorm.DB, status, severity string) *gorm.DB {
	if status = strings.TrimSpace(status); status != "" {
		q = q.Where("status = ?", status)
	}
	if severity = strings.TrimSpace(severity); severity != "" {
		q = q.Where("severity = ?", severity)
	}
	return q
}

// GetAlertByID 按 ID 读取告警。
func (s *Store) GetAlertByID(id uint) (*Alert, error) {
	var a Alert
	if err := s.db.Where("id = ?", id).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateAlert 更新告警。
func (s *Store) UpdateAlert(a *Alert) error {
	return s.db.Save(a).Error
}

// AlertTrendBucket 单日告警趋势桶。
type AlertTrendBucket struct {
	Date     string `json:"date"`     // 本地日期 YYYY-MM-DD
	Critical int    `json:"critical"` // 当日新增严重告警数
	Warning  int    `json:"warning"`  // 当日新增警告数
	Total    int    `json:"total"`    // 当日新增告警总数
	Resolved int    `json:"resolved"` // 当日恢复告警数
}

// AlertTrends 统计近 days 天(含今日)按天分桶的告警趋势。
// clusterIDs 为空表示统计所有集群;非空时仅统计其中集群。
// 新增按 CreatedAt、恢复按 ResolvedAt 归入所在本地日期。Go 侧聚合以规避 SQL 方言差异。
func (s *Store) AlertTrends(clusterIDs []uint, days int) ([]AlertTrendBucket, error) {
	if days <= 0 {
		days = 7
	}
	now := time.Now()
	loc := now.Location()
	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))

	buckets := make([]AlertTrendBucket, days)
	index := make(map[string]int, days)
	for i := 0; i < days; i++ {
		key := startDay.AddDate(0, 0, i).Format("2006-01-02")
		buckets[i] = AlertTrendBucket{Date: key}
		index[key] = i
	}

	var alerts []Alert
	q := s.db.Where("(created_at >= ? OR resolved_at >= ?)", startDay, startDay)
	if len(clusterIDs) > 0 {
		q = q.Where("cluster_id IN ?", clusterIDs)
	}
	if err := q.Find(&alerts).Error; err != nil {
		return nil, err
	}
	for _, a := range alerts {
		if !a.CreatedAt.Before(startDay) {
			if i, ok := index[a.CreatedAt.In(loc).Format("2006-01-02")]; ok {
				buckets[i].Total++
				switch a.Severity {
				case "critical":
					buckets[i].Critical++
				case "warning":
					buckets[i].Warning++
				}
			}
		}
		if a.ResolvedAt != nil && !a.ResolvedAt.Before(startDay) {
			if i, ok := index[a.ResolvedAt.In(loc).Format("2006-01-02")]; ok {
				buckets[i].Resolved++
			}
		}
	}
	return buckets, nil
}

// ---- 告警静默 ----

// CreateSilence 创建静默规则。
func (s *Store) CreateSilence(silence *AlertSilence) error {
	return s.db.Create(silence).Error
}

// GetSilenceByID 按 ID 读取静默规则。
func (s *Store) GetSilenceByID(id uint) (*AlertSilence, error) {
	var silence AlertSilence
	if err := s.db.Where("id = ?", id).First(&silence).Error; err != nil {
		return nil, err
	}
	return &silence, nil
}

// DeleteSilence 删除(提前解除)静默规则。
func (s *Store) DeleteSilence(id uint) error {
	return s.db.Delete(&AlertSilence{}, id).Error
}

// ListActiveSilences 返回未过期的静默规则,顺带惰性清理已过期行。
// clusterIDs 为空表示所有集群。
func (s *Store) ListActiveSilences(clusterIDs []uint) ([]AlertSilence, error) {
	now := time.Now()
	_ = s.db.Where("expires_at <= ?", now).Delete(&AlertSilence{}).Error
	var silences []AlertSilence
	q := s.db.Where("expires_at > ?", now).Order("id desc")
	if len(clusterIDs) > 0 {
		q = q.Where("cluster_id IN ?", clusterIDs)
	}
	err := q.Find(&silences).Error
	return silences, err
}

// MatchSilence 判断告警是否命中任一静默规则(调用方需保证 silences 均未过期且集群匹配已过滤)。
func MatchSilence(silences []AlertSilence, a Alert) bool {
	for _, sl := range silences {
		if a.ClusterID == nil || *a.ClusterID != sl.ClusterID {
			continue
		}
		if sl.Rule != "" && sl.Rule != a.Rule {
			continue
		}
		if sl.Namespace != "" && sl.Namespace != a.Namespace {
			continue
		}
		if sl.Name != "" && sl.Name != a.Name {
			continue
		}
		return true
	}
	return false
}

// ---- 通知设置 ----

// GetNotificationSetting 读取全局通知设置;未配置过返回 (nil, nil)。
func (s *Store) GetNotificationSetting() (*NotificationSetting, error) {
	var setting NotificationSetting
	err := s.db.Where("id = ?", 1).First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &setting, nil
}

// SaveNotificationSetting upsert 全局通知设置(单行,ID 恒为 1)。
func (s *Store) SaveNotificationSetting(setting *NotificationSetting) error {
	setting.ID = 1
	return s.db.Save(setting).Error
}

// ---- 巡检规则设置 ----

// ListInspectionRuleSettings 返回指定层(clusterID=0 为全局层)的规则设置。
func (s *Store) ListInspectionRuleSettings(clusterID uint) ([]InspectionRuleSetting, error) {
	var settings []InspectionRuleSetting
	err := s.db.Where("cluster_id = ?", clusterID).Find(&settings).Error
	return settings, err
}

// SaveInspectionRuleSetting 按 规则名+集群 upsert 设置。
func (s *Store) SaveInspectionRuleSetting(setting *InspectionRuleSetting) error {
	var existing InspectionRuleSetting
	err := s.db.Where("rule_name = ? AND cluster_id = ?", setting.RuleName, setting.ClusterID).First(&existing).Error
	if err == nil {
		setting.ID = existing.ID
		return s.db.Save(setting).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.db.Create(setting).Error
	}
	return err
}

// DeleteInspectionRuleSetting 删除某层的规则设置(集群层→恢复继承全局;全局层→恢复内置默认)。
func (s *Store) DeleteInspectionRuleSetting(ruleName string, clusterID uint) error {
	return s.db.Where("rule_name = ? AND cluster_id = ?", ruleName, clusterID).
		Delete(&InspectionRuleSetting{}).Error
}

// GetGlobalPrometheusURL 读取页面配置的全局 Prometheus 地址;未配置返回空串。
func (s *Store) GetGlobalPrometheusURL() (string, error) {
	var row GlobalPrometheusSetting
	err := s.db.Where("id = ?", 1).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return strings.TrimSpace(row.PrometheusURL), err
}

// SaveGlobalPrometheusURL upsert 全局 Prometheus 地址(空串=清除,回退 config 文件)。
func (s *Store) SaveGlobalPrometheusURL(url, operator string) error {
	return s.db.Save(&GlobalPrometheusSetting{
		ID: 1, PrometheusURL: url, UpdatedBy: operator, UpdatedAt: time.Now(),
	}).Error
}

// ---- 自定义指标规则 ----

// ListCustomMetricRules 返回精确某层(clusterID=0 全局)的自定义规则,供管理页展示。
func (s *Store) ListCustomMetricRules(clusterID uint) ([]CustomMetricRule, error) {
	var rules []CustomMetricRule
	err := s.db.Where("cluster_id = ?", clusterID).Order("id desc").Find(&rules).Error
	return rules, err
}

// ListActiveCustomMetricRules 返回对某集群生效(全局 + 该集群、仅启用)的自定义规则,供巡检引擎评估。
func (s *Store) ListActiveCustomMetricRules(clusterID uint) ([]CustomMetricRule, error) {
	var rules []CustomMetricRule
	q := s.db.Where("enabled = ?", true)
	if clusterID != 0 {
		q = q.Where("cluster_id = 0 OR cluster_id = ?", clusterID)
	} else {
		q = q.Where("cluster_id = 0")
	}
	err := q.Find(&rules).Error
	return rules, err
}

// GetCustomMetricRuleByID 按 ID 读取自定义规则。
func (s *Store) GetCustomMetricRuleByID(id uint) (*CustomMetricRule, error) {
	var rule CustomMetricRule
	if err := s.db.Where("id = ?", id).First(&rule).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

// CustomMetricRuleNameExists 判断某层内是否已存在同名规则(excludeID>0 时排除自身,用于更新校验)。
func (s *Store) CustomMetricRuleNameExists(clusterID uint, name string, excludeID uint) (bool, error) {
	var count int64
	q := s.db.Model(&CustomMetricRule{}).Where("cluster_id = ? AND name = ?", clusterID, name)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.Count(&count).Error
	return count > 0, err
}

// CreateCustomMetricRule 创建自定义规则。
func (s *Store) CreateCustomMetricRule(rule *CustomMetricRule) error {
	return s.db.Create(rule).Error
}

// UpdateCustomMetricRule 更新自定义规则。
func (s *Store) UpdateCustomMetricRule(rule *CustomMetricRule) error {
	return s.db.Save(rule).Error
}

// DeleteCustomMetricRule 删除自定义规则。
func (s *Store) DeleteCustomMetricRule(id uint) error {
	return s.db.Delete(&CustomMetricRule{}, id).Error
}
