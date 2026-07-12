package inspect

import (
	"context"
	"sync"
	"time"

	"aiops/internal/collector"
)

// Diagnoser 对异常给出 AI 诊断。由 ai 包实现,可选注入(未配置大模型时为 nil)。
type Diagnoser interface {
	Diagnose(ctx context.Context, a *Anomaly) (string, error)
}

// Engine 巡检引擎:采集集群数据并应用规则,产出巡检报告。
type Engine struct {
	mu          sync.RWMutex
	collector   *collector.Collector
	diagnoser   Diagnoser
	clusterID   uint        // 所属集群,决定规则配置的集群覆盖层
	promQuerier PromQuerier // 集群独立 Prometheus 查询器;nil=未接入,指标规则跳过
}

// NewEngine 创建巡检引擎。diagnoser 可为 nil(此时跳过 AI 诊断)。
func NewEngine(col *collector.Collector, diagnoser Diagnoser) *Engine {
	return &Engine{collector: col, diagnoser: diagnoser}
}

// SetDiagnoser 热切换巡检引擎使用的 AI 诊断器。
func (e *Engine) SetDiagnoser(diagnoser Diagnoser) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.diagnoser = diagnoser
}

// SetClusterID 绑定引擎所属集群(用于读取该集群的规则覆盖配置)。
func (e *Engine) SetClusterID(clusterID uint) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clusterID = clusterID
}

// SetPromQuerier 注入/热替换本集群的 Prometheus 查询器;nil 表示未接入。
func (e *Engine) SetPromQuerier(q PromQuerier) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.promQuerier = q
}

func (e *Engine) diagnoserSnapshot() Diagnoser {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.diagnoser
}

// runtimeSnapshot 一次性读出巡检所需的集群 ID 与查询器,避免多次加锁。
func (e *Engine) runtimeSnapshot() (uint, PromQuerier) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.clusterID, e.promQuerier
}

// Run 执行完整巡检:采集 → 规则 → AI 诊断(若启用)。集群操作全程只读。
func (e *Engine) Run(ctx context.Context) (*Report, error) {
	return e.run(ctx, true)
}

// RunRulesOnly 只跑规则巡检,不做 AI 诊断。供 Agent 工具调用,避免"模型调工具又触发模型诊断"的嵌套。
func (e *Engine) RunRulesOnly(ctx context.Context) (*Report, error) {
	return e.run(ctx, false)
}

func (e *Engine) run(ctx context.Context, diagnose bool) (*Report, error) {
	started := time.Now()

	overview, err := e.collector.Overview(ctx)
	if err != nil {
		return nil, err
	}
	pods, err := e.collector.ListPods(ctx, "")
	if err != nil {
		return nil, err
	}
	nodes, err := e.collector.ListNodes(ctx)
	if err != nil {
		return nil, err
	}

	// 每轮读取本集群当前生效规则配置(默认→全局→集群覆盖,页面修改后热生效)
	clusterID, querier := e.runtimeSnapshot()
	cfg := currentRuleConfig(clusterID)
	anomalies := make([]Anomaly, 0)
	for _, p := range pods {
		anomalies = append(anomalies, checkPod(p, cfg)...)
	}
	for _, n := range nodes {
		anomalies = append(anomalies, checkNode(n, cfg)...)
	}
	// Prometheus 指标规则(本集群未接入或规则关闭时为空;查询失败不阻断状态规则)
	anomalies = append(anomalies, evalPromRules(ctx, querier, cfg)...)
	// 用户自定义指标规则(全局 + 本集群、仅启用)
	anomalies = append(anomalies, evalCustomMetricRules(ctx, querier, currentCustomRules(clusterID))...)

	// 对每条异常做 AI 诊断(若启用)。诊断前采集现场证据,单条失败不影响整体巡检。
	diagnoser := e.diagnoserSnapshot()
	if diagnose && diagnoser != nil {
		for i := range anomalies {
			e.collectEvidence(ctx, &anomalies[i])
			if diag, derr := diagnoser.Diagnose(ctx, &anomalies[i]); derr == nil {
				anomalies[i].Diagnosis = diag
			}
		}
	}

	return &Report{
		StartedAt:    started,
		FinishedAt:   time.Now(),
		Overview:     overview,
		Anomalies:    anomalies,
		AnomalyCount: len(anomalies),
	}, nil
}

// collectEvidence 为异常采集现场证据(日志/事件/状态详情),供 AI 诊断引用。尽力而为,失败留空。
func (e *Engine) collectEvidence(ctx context.Context, a *Anomaly) {
	if e.collector == nil || a.Evidence != "" {
		return
	}
	ectx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	switch a.Kind {
	case "Pod":
		a.Evidence = e.collector.PodEvidence(ectx, a.Namespace, a.Name)
	case "Node":
		a.Evidence = e.collector.NodeEvidence(ectx, a.Name)
	}
}
