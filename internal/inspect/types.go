// Package inspect 实现巡检引擎:采集集群数据并应用规则,识别异常。
package inspect

import (
	"time"

	"aiops/internal/collector"
)

// Severity 异常严重级别。
type Severity string

const (
	SeverityCritical Severity = "critical" // 严重:影响服务可用性
	SeverityWarning  Severity = "warning"  // 警告:需关注但未必影响可用性
)

// Anomaly 巡检发现的单条异常。
type Anomaly struct {
	Severity  Severity `json:"severity"`
	Kind      string   `json:"kind"`      // Pod / Node / Deployment
	Namespace string   `json:"namespace"` // 节点类异常为空
	Name      string   `json:"name"`
	Rule      string   `json:"rule"`   // 命中的规则名
	Reason    string   `json:"reason"` // K8S 原始原因,如 CrashLoopBackOff
	Message   string   `json:"message"`
	// Evidence 现场证据(容器状态详情/关联事件/崩溃日志),诊断前采集,供 AI 引用定位具体根因。
	Evidence string `json:"evidence,omitempty"`
	// Diagnosis 由大模型填充(Task#5),阶段四前可能为空。
	Diagnosis string `json:"diagnosis,omitempty"`
}

// Report 一次巡检的完整报告。
type Report struct {
	StartedAt     time.Time                  `json:"started_at"`
	FinishedAt    time.Time                  `json:"finished_at"`
	Overview      *collector.ClusterOverview `json:"overview"`
	ResourceCache []ResourceCacheSummary     `json:"resource_cache,omitempty"`
	Anomalies     []Anomaly                  `json:"anomalies"`
	AnomalyCount  int                        `json:"anomaly_count"`
}

// ResourceCacheSummary 描述巡检前最近一次资源缓存同步摘要。
type ResourceCacheSummary struct {
	APIVersion        string     `json:"api_version"`
	Kind              string     `json:"kind"`
	ResourceName      string     `json:"resource_name"`
	ItemCount         int        `json:"item_count"`
	LastChangeCount   int        `json:"last_change_count"`
	LastSource        string     `json:"last_source"`
	FullStatus        string     `json:"full_status"`
	IncrementalStatus string     `json:"incremental_status"`
	LastSyncedAt      *time.Time `json:"last_synced_at,omitempty"`
}
