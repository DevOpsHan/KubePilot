package inspect

import (
	"context"
	"fmt"
	"strings"
)

// evalCustomMetricRules 评估用户自定义的 Prometheus 指标规则。
// 未注入查询器或无规则时返回空;单条规则查询失败/表达式非法静默跳过,不阻断其它规则与内置巡检。
func evalCustomMetricRules(ctx context.Context, querier PromQuerier, specs []CustomMetricRuleSpec) []Anomaly {
	if querier == nil || len(specs) == 0 {
		return nil
	}
	var out []Anomaly
	for _, spec := range specs {
		if strings.TrimSpace(spec.PromQL) == "" {
			continue
		}
		samples, err := querier.Query(ctx, spec.PromQL)
		if err != nil {
			continue
		}
		label := strings.TrimSpace(spec.ObjectLabel)
		if label == "" {
			label = "instance"
		}
		severity := Severity(spec.Severity)
		if severity != SeverityCritical && severity != SeverityWarning {
			severity = SeverityWarning
		}
		for _, sample := range samples {
			if !compareThreshold(sample.Value, spec.Operator, spec.Threshold) {
				continue
			}
			name := sample.Labels[label]
			if name == "" {
				name = sample.Labels["instance"]
			}
			if name == "" {
				name = spec.Name // 无法定位对象时以规则名兜底,保证告警可去重
			}
			out = append(out, Anomaly{
				Severity: severity,
				Kind:     "Metric",
				Name:     name,
				Rule:     spec.Name,
				Reason:   "CustomMetric",
				Message: fmt.Sprintf("自定义指标规则「%s」命中:%s 当前值 %.4g %s 阈值 %g",
					spec.Name, name, sample.Value, spec.Operator, spec.Threshold),
			})
		}
	}
	return out
}

// compareThreshold 按运算符比较样本值与阈值;未知运算符返回 false(不告警)。
func compareThreshold(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	default:
		return false
	}
}
