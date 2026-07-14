// Package store 负责巡检与修复数据的持久化(GORM)。
package store

import "time"

// ReportRecord 巡检报告(持久化)。
type ReportRecord struct {
	ID           uint            `gorm:"primaryKey" json:"id"`
	ClusterID    *uint           `gorm:"index" json:"cluster_id,omitempty"`
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   time.Time       `json:"finished_at"`
	NodeCount    int             `json:"node_count"`
	PodCount     int             `json:"pod_count"`
	AnomalyCount int             `json:"anomaly_count"`
	Anomalies    []AnomalyRecord `gorm:"foreignKey:ReportID" json:"anomalies,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// AnomalyRecord 异常记录(持久化,附带 AI 诊断)。
type AnomalyRecord struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	ReportID  uint   `gorm:"index" json:"report_id"`
	ClusterID *uint  `gorm:"index" json:"cluster_id,omitempty"`
	Severity  string `json:"severity"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Rule      string `json:"rule"`
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Diagnosis string `gorm:"type:text" json:"diagnosis"`
}

// 审批工单状态。
const (
	TicketPending  = "pending"  // 待审批
	TicketApproved = "approved" // 已批准(执行中)
	TicketRejected = "rejected" // 已拒绝
	TicketExecuted = "executed" // 已执行成功
	TicketFailed   = "failed"   // 执行失败
)

// 修复效果复检状态:执行成功后延时复检目标资源,回写此状态。
const (
	VerifyPending = "pending" // 复检中(已排期,尚未有结论)
	VerifyHealthy = "healthy" // 已修复(目标恢复,不再命中规则)
	VerifyFailing = "failing" // 未解决(目标仍异常,建议人工介入)
	VerifySkipped = "skipped" // 未复检(引擎不可用等,无法复检)
)

// ApprovalTicket 修复审批工单。AI 提议的写操作先建工单,人工审批后才执行。
type ApprovalTicket struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	ClusterID *uint  `gorm:"index" json:"cluster_id,omitempty"`
	AlertID   *uint  `gorm:"index" json:"alert_id,omitempty"` // 回链发起告警,用于修复复检后自动解决
	Action    string `json:"action"`                          // restart_pod | delete_pod | scale_deployment | restart_deployment
	Namespace string `json:"namespace"`                       // 目标命名空间
	Target    string `json:"target"`                          // 目标资源名(Pod/Deployment)
	Params    string `json:"params"`                          // 额外参数(JSON,如 {"replicas":3})
	Reason    string `json:"reason"`                          // AI 给出的修复理由
	Risk      string `json:"risk"`                            // 风险等级
	Status    string `json:"status"`                          // pending/approved/rejected/executed/failed
	Approver  string `json:"approver"`                        // 审批人
	Result    string `json:"result"`                          // 执行结果
	// 修复效果复检:执行成功后延时复检目标资源,回写下列字段
	VerifyState  string     `gorm:"size:32" json:"verify_state"`   // ""|pending|healthy|failing|skipped
	VerifiedAt   *time.Time `json:"verified_at,omitempty"`         // 复检出结论的时间
	VerifyDetail string     `gorm:"size:512" json:"verify_detail"` // 复检结论说明(耗时/仍存异常等)
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// AuditLog 审计日志:记录所有关键操作(谁、何时、做了什么、结果)。
type AuditLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ClusterID *uint     `gorm:"index" json:"cluster_id,omitempty"`
	Action    string    `json:"action"`
	Operator  string    `json:"operator"` // 操作者(人工审批人 / AI)
	Target    string    `json:"target"`
	Detail    string    `gorm:"type:text" json:"detail"`
	Result    string    `json:"result"`
	CreatedAt time.Time `json:"created_at"`
}

// 告警状态常量。
const (
	AlertStatusActive       = "active"       // 触发中
	AlertStatusAcknowledged = "acknowledged" // 已确认(仍在触发)
	AlertStatusResolved     = "resolved"     // 已恢复或人工解决
)

// Alert 告警:定时巡检发现的异常沉淀为可管理的告警(触发/确认/恢复)。
// 用 Fingerprint(集群+资源+规则)去重:同一异常跨轮巡检 upsert,而非重复新建;本轮缺失则自动恢复。
type Alert struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	ClusterID           *uint      `gorm:"index" json:"cluster_id,omitempty"`
	Fingerprint         string     `gorm:"size:512;index" json:"fingerprint"`
	Severity            string     `gorm:"size:32;index" json:"severity"`
	Kind                string     `gorm:"size:128" json:"kind"`
	Namespace           string     `gorm:"size:256" json:"namespace"`
	Name                string     `gorm:"size:256" json:"name"`
	Rule                string     `gorm:"size:128" json:"rule"`
	Reason              string     `gorm:"size:256" json:"reason"`
	Message             string     `gorm:"type:text" json:"message"`
	Status              string     `gorm:"size:32;index;not null;default:active" json:"status"`
	Count               int        `gorm:"not null;default:1" json:"count"`
	FirstSeenAt         time.Time  `json:"first_seen_at"`
	LastSeenAt          time.Time  `json:"last_seen_at"`
	AcknowledgedBy      string     `gorm:"size:128" json:"acknowledged_by"`
	AcknowledgedAt      *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedBy          string     `gorm:"size:128" json:"resolved_by"`
	ResolvedAt          *time.Time `json:"resolved_at,omitempty"`
	Diagnosis           string     `gorm:"type:text" json:"diagnosis"`
	SuggestedAction     string     `gorm:"size:64" json:"suggested_action"` // AI 诊断给出的建议修复动作(白名单;空=无)
	DiagnosedAt         *time.Time `json:"diagnosed_at,omitempty"`
	RemediationTicketID *uint      `gorm:"index" json:"remediation_ticket_id,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// AlertSilence 告警静默规则:命中的告警不推外部通知、UI 置灰标记、摘要计数排除。
// 匹配语义:Rule/Namespace/Name 非空字段全部相等才命中;到期(ExpiresAt)自动失效。
type AlertSilence struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ClusterID uint      `gorm:"index;not null" json:"cluster_id"`
	Rule      string    `gorm:"size:128" json:"rule"`
	Namespace string    `gorm:"size:256" json:"namespace"`
	Name      string    `gorm:"size:256" json:"name"`
	Comment   string    `gorm:"size:512" json:"comment"`
	CreatedBy string    `gorm:"size:128" json:"created_by"`
	ExpiresAt time.Time `gorm:"index" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// InspectionRuleSetting 巡检规则的用户自定义设置。
// 三层生效:内置默认 → 全局层(ClusterID=0)→ 集群覆盖层(ClusterID>0);缺行即继承上一层。
type InspectionRuleSetting struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	RuleName  string    `gorm:"size:128;uniqueIndex:idx_rule_cluster" json:"rule_name"`
	ClusterID uint      `gorm:"uniqueIndex:idx_rule_cluster;not null;default:0" json:"cluster_id"`
	Enabled   bool      `json:"enabled"`
	Threshold float64   `json:"threshold"`
	UpdatedBy string    `gorm:"size:128" json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CustomMetricRule 用户自定义的 Prometheus 指标告警规则。
// ClusterID=0 为全局(对所有集群生效);>0 仅对该集群生效。Name 同时作为告警 Rule 名。
type CustomMetricRule struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ClusterID   uint      `gorm:"index;not null;default:0" json:"cluster_id"`
	Name        string    `gorm:"size:128;not null" json:"name"`    // 显示名 + 告警 Rule 名,层内唯一
	PromQL      string    `gorm:"type:text;not null" json:"promql"` // 即时查询表达式
	Operator    string    `gorm:"size:8;not null" json:"operator"`  // > >= < <= ==
	Threshold   float64   `json:"threshold"`                        // 阈值
	Severity    string    `gorm:"size:32;not null" json:"severity"` // critical|warning
	ObjectLabel string    `gorm:"size:64" json:"object_label"`      // 取哪个标签作对象名,默认 instance
	Description string    `gorm:"size:512" json:"description"`
	Enabled     bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedBy   string    `gorm:"size:128" json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GlobalPrometheusSetting 页面可配的全局 Prometheus 地址(单行,ID 恒为 1)。
// 集群未单独配置时回退到此;此值又以 config.yaml 的 prometheus.url 作为最终兜底。保存即热生效。
type GlobalPrometheusSetting struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	PrometheusURL string    `gorm:"size:1024" json:"prometheus_url"`
	UpdatedBy     string    `gorm:"size:128" json:"updated_by"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NotificationSetting 外部通知设置(全局单行,ID 恒为 1)。
// webhook/secret 采用尽力加密存储:配置了 AIOPS_SECRET_KEY 时 AES-GCM 加密(enc: 前缀),否则明文。
type NotificationSetting struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Enabled         bool      `json:"enabled"`
	MinSeverity     string    `gorm:"size:32" json:"min_severity"`
	NotifyResolved  bool      `json:"notify_resolved"`
	DingTalkWebhook string    `gorm:"size:1024" json:"-"`
	DingTalkSecret  string    `gorm:"size:512" json:"-"`
	WeComWebhook    string    `gorm:"size:1024" json:"-"`
	UpdatedBy       string    `gorm:"size:128" json:"updated_by"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// 集群接入模式。
const (
	ClusterModeInCluster      = "in_cluster"
	ClusterModeKubeconfig     = "kubeconfig_path"
	ClusterModeKubeconfigData = "kubeconfig_data"
)

// 集群状态。
const (
	ClusterStatusUnknown      = "unknown"
	ClusterStatusImported     = "imported"
	ClusterStatusSyncing      = "syncing"
	ClusterStatusReady        = "ready"
	ClusterStatusDegraded     = "degraded"
	ClusterStatusDisconnected = "disconnected"
	ClusterStatusError        = "error"
)

// 默认集群标识,用于兼容历史单集群数据。
const DefaultClusterSlug = "default"

// Cluster 表示一个可被平台运维的 Kubernetes 集群。
type Cluster struct {
	ID                         uint       `gorm:"primaryKey" json:"id"`
	Name                       string     `gorm:"size:128;not null" json:"name"`
	Slug                       string     `gorm:"size:128;uniqueIndex;not null" json:"slug"`
	Description                string     `gorm:"size:512" json:"description"`
	Mode                       string     `gorm:"size:32;not null" json:"mode"`
	ContextName                string     `gorm:"size:256" json:"context_name"`
	APIServerHost              string     `gorm:"size:512" json:"api_server_host"`
	Version                    string     `gorm:"size:128" json:"version"`
	Status                     string     `gorm:"size:32;not null;default:unknown" json:"status"`
	Enabled                    bool       `gorm:"not null;default:true" json:"enabled"`
	LastValidatedAt            *time.Time `json:"last_validated_at,omitempty"`
	LastError                  string     `gorm:"type:text" json:"last_error"`
	ResourceSnapshotGeneration string     `gorm:"size:64" json:"-"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// ClusterCredential 保存集群访问凭据。敏感字段必须加密后再写入。
type ClusterCredential struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	ClusterID           uint      `gorm:"uniqueIndex;not null" json:"cluster_id"`
	KubeconfigPath      string    `gorm:"size:1024" json:"kubeconfig_path"`
	EncryptedKubeconfig string    `gorm:"type:text" json:"-"`
	KubeconfigHash      string    `gorm:"size:128;index" json:"kubeconfig_hash"`
	Version             int       `gorm:"not null;default:1" json:"version"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ClusterPolicy 保存每个集群的写操作策略与巡检数据源。
type ClusterPolicy struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	ClusterID          uint      `gorm:"uniqueIndex;not null" json:"cluster_id"`
	RemediationEnabled bool      `gorm:"not null;default:false" json:"remediation_enabled"`
	PrometheusURL      string    `gorm:"size:1024" json:"prometheus_url"` // 集群独立 Prometheus;空=回退全局配置
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// 同步状态常量。
const (
	SyncStatusSuccess = "success"
	SyncStatusFailed  = "failed"
	SyncStatusRunning = "running"
	SyncStatusNA      = "n/a"
	SyncStatusNotRun  = "not_run"
)

// 同步来源常量,用于区分自动同步、人工同步和巡检前同步。
const (
	SyncSourceManual  = "manual"
	SyncSourceAuto    = "auto"
	SyncSourceInspect = "inspect"
	SyncSourceImport  = "import"
)

// ClusterSyncResource 记录集群资源同步状态。
type ClusterSyncResource struct {
	ID                    uint       `gorm:"primaryKey" json:"id"`
	ClusterID             uint       `gorm:"uniqueIndex:idx_cluster_sync_resource;not null" json:"cluster_id"`
	APIVersion            string     `gorm:"size:128;not null" json:"api_version"`
	Kind                  string     `gorm:"size:128;not null" json:"kind"`
	ResourceName          string     `gorm:"size:192;uniqueIndex:idx_cluster_sync_resource;not null" json:"resource_name"`
	ItemCount             int        `json:"item_count"`
	FullStartedAt         *time.Time `json:"full_started_at,omitempty"`
	FullFinishedAt        *time.Time `json:"full_finished_at,omitempty"`
	FullStatus            string     `gorm:"size:32;not null" json:"full_status"`
	FullError             string     `gorm:"type:text" json:"full_error"`
	IncrementalStartedAt  *time.Time `json:"incremental_started_at,omitempty"`
	IncrementalFinishedAt *time.Time `json:"incremental_finished_at,omitempty"`
	IncrementalRounds     int        `json:"incremental_rounds"`
	IncrementalStatus     string     `gorm:"size:32;not null" json:"incremental_status"`
	LastSource            string     `gorm:"size:32;index" json:"last_source"`
	LastChangeCount       int        `json:"last_change_count"`
	LastResourceVersion   string     `gorm:"size:128" json:"last_resource_version"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// ClusterSyncTask 记录每一次集群同步任务。
type ClusterSyncTask struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	ClusterID     uint       `gorm:"index;not null" json:"cluster_id"`
	Source        string     `gorm:"size:32;index" json:"source"`
	Status        string     `gorm:"size:32;index" json:"status"`
	ResourceCount int        `json:"resource_count"`
	ChangedCount  int        `json:"changed_count"`
	Error         string     `gorm:"type:text" json:"error"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ClusterResourceSnapshot 保存最近一次同步得到的具体资源快照。
type ClusterResourceSnapshot struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	ClusterID       uint      `gorm:"index:idx_cluster_resource_snapshot;not null" json:"cluster_id"`
	APIVersion      string    `gorm:"size:128;not null" json:"api_version"`
	Kind            string    `gorm:"size:128;not null" json:"kind"`
	ResourceName    string    `gorm:"size:192;index:idx_cluster_resource_snapshot;not null" json:"resource_name"`
	Namespace       string    `gorm:"size:256;index:idx_cluster_resource_snapshot" json:"namespace"`
	Name            string    `gorm:"size:256;index:idx_cluster_resource_snapshot;not null" json:"name"`
	UID             string    `gorm:"size:128" json:"uid"`
	ResourceVersion string    `gorm:"size:128" json:"resource_version"`
	Status          string    `gorm:"size:128" json:"status"`
	Ready           string    `gorm:"size:64" json:"ready"`
	NodeName        string    `gorm:"size:256" json:"node_name"`
	Summary         string    `gorm:"type:text" json:"summary"`
	ContentHash     string    `gorm:"size:128;index" json:"content_hash"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ClusterDeletedResource 记录资源缓存中已消失的对象,用于追溯删除历史。
type ClusterDeletedResource struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	ClusterID       uint      `gorm:"index;not null" json:"cluster_id"`
	APIVersion      string    `gorm:"size:128;not null" json:"api_version"`
	Kind            string    `gorm:"size:128;not null" json:"kind"`
	ResourceName    string    `gorm:"size:192;index;not null" json:"resource_name"`
	Namespace       string    `gorm:"size:256;index" json:"namespace"`
	Name            string    `gorm:"size:256;index;not null" json:"name"`
	UID             string    `gorm:"size:128" json:"uid"`
	ResourceVersion string    `gorm:"size:128" json:"resource_version"`
	LastSnapshot    string    `gorm:"type:text" json:"last_snapshot"`
	DeletedAt       time.Time `json:"deleted_at"`
	CreatedAt       time.Time `json:"created_at"`
}

// ModelProfile 是可在平台中选择的大模型配置档案。
type ModelProfile struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	Name                string    `gorm:"size:128;not null" json:"name"`
	Provider            string    `gorm:"size:64;not null" json:"provider"`
	BaseURL             string    `gorm:"size:512" json:"base_url"`
	ModelName           string    `gorm:"size:128;not null" json:"model_name"`
	EncryptedAPIKey     string    `gorm:"type:text" json:"-"`
	MaxCompletionTokens int       `json:"max_completion_tokens"`
	TimeoutSeconds      int       `json:"timeout_seconds"`
	Temperature         float32   `json:"temperature"`
	IsDefault           bool      `gorm:"index" json:"is_default"`
	Enabled             bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// User 表示平台本地用户。非全局角色通过 ClusterGrants 限定可操作集群。
type User struct {
	ID                     uint               `gorm:"primaryKey" json:"id"`
	Username               string             `gorm:"size:64;uniqueIndex;not null" json:"username"`
	DisplayName            string             `gorm:"size:128" json:"display_name"`
	Email                  string             `gorm:"size:256" json:"email"`
	PasswordHash           string             `gorm:"type:text" json:"-"`
	Role                   string             `gorm:"size:32;not null;default:cluster_admin" json:"role"`
	Status                 string             `gorm:"size:32;not null;default:active" json:"status"`
	ExpiresAt              *time.Time         `json:"expires_at,omitempty"`
	PasswordChangeRequired bool               `gorm:"not null;default:false" json:"password_change_required"`
	PasswordAttemptedAt    *time.Time         `json:"password_attempted_at,omitempty"`
	PasswordAttemptCount   int                `json:"password_attempt_count"`
	LastLoginAt            *time.Time         `json:"last_login_at,omitempty"`
	DeletedAt              *time.Time         `gorm:"index" json:"deleted_at,omitempty"`
	ClusterGrants          []UserClusterGrant `gorm:"foreignKey:UserID" json:"cluster_grants,omitempty"`
	CreatedAt              time.Time          `json:"created_at"`
	UpdatedAt              time.Time          `json:"updated_at"`
}

// UserClusterGrant 保存用户可操作的集群范围。全局管理员不需要写入授权记录。
type UserClusterGrant struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"uniqueIndex:idx_user_cluster_grant;not null" json:"user_id"`
	ClusterID uint      `gorm:"uniqueIndex:idx_user_cluster_grant;not null" json:"cluster_id"`
	Cluster   *Cluster  `gorm:"foreignKey:ClusterID" json:"cluster,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AuthSession 保存用户登录会话。数据库只保存令牌哈希,不保存明文 token。
type AuthSession struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	TokenHash string     `gorm:"size:128;uniqueIndex;not null" json:"-"`
	ExpiresAt time.Time  `gorm:"index;not null" json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
