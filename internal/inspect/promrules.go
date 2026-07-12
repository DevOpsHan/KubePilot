package inspect

import (
	"context"
	"fmt"
)

// node_exporter 标准指标的即时查询语句(5 分钟窗口)。
const (
	promQueryNodeCPU    = `100 - avg by(instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100`
	promQueryNodeMemory = `(1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) * 100`
)

// evalPromRules 评估 Prometheus 指标规则。未注入查询器或规则关闭时返回空;
// 单条查询失败静默跳过(Prometheus 短暂不可用不应阻断 K8S 状态巡检)。
func evalPromRules(ctx context.Context, querier PromQuerier, cfg RuleConfig) []Anomaly {
	if querier == nil {
		return nil
	}
	var out []Anomaly
	rules := []struct {
		name   string
		query  string
		reason string
		msgFmt string // 参数:节点、当前值、阈值
	}{
		{RuleNodeHighCPU, promQueryNodeCPU, "HighCPU", "节点 %s CPU 使用率 5 分钟均值 %.1f%%,超过阈值 %.0f%%"},
		{RuleNodeHighMemory, promQueryNodeMemory, "HighMemory", "节点 %s 内存使用率 %.1f%%,超过阈值 %.0f%%"},
	}
	for _, r := range rules {
		if !cfg.enabled(r.name) {
			continue
		}
		values, err := querier.QueryVector(ctx, r.query)
		if err != nil {
			continue
		}
		threshold := cfg.threshold(r.name)
		for instance, value := range values {
			if value <= threshold {
				continue
			}
			out = append(out, Anomaly{
				Severity: SeverityWarning,
				Kind:     "Node",
				Name:     instance,
				Rule:     r.name,
				Reason:   r.reason,
				Message:  fmt.Sprintf(r.msgFmt, instance, value, threshold),
			})
		}
	}
	return out
}
