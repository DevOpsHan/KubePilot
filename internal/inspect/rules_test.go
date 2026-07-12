package inspect

import (
	"context"
	"testing"

	"aiops/internal/collector"
	"aiops/internal/prom"
)

// TestRuleConfigDisable 验证禁用规则后不再产出异常。
func TestRuleConfigDisable(t *testing.T) {
	pod := collector.PodInfo{Namespace: "default", Name: "web-1", Phase: "Running", Reason: "CrashLoopBackOff", Restarts: 3}

	if got := checkPod(pod, DefaultRuleConfig()); len(got) != 1 || got[0].Rule != RulePodCrashLoopBackOff {
		t.Fatalf("默认配置期望命中 CrashLoop,实际 %+v", got)
	}

	cfg := DefaultRuleConfig()
	cfg[RulePodCrashLoopBackOff] = RuleSetting{Enabled: false}
	if got := checkPod(pod, cfg); len(got) != 0 {
		t.Fatalf("禁用后期望无异常,实际 %+v", got)
	}
}

// TestHighRestartsThreshold 验证重启阈值可调,且阈值<=0 回退默认。
func TestHighRestartsThreshold(t *testing.T) {
	pod := collector.PodInfo{Namespace: "default", Name: "web-1", Phase: "Running", Ready: "1/1", Restarts: 3}

	// 默认阈值 5:3 次不告警
	if got := checkPod(pod, DefaultRuleConfig()); len(got) != 0 {
		t.Fatalf("默认阈值下期望无异常,实际 %+v", got)
	}
	// 阈值降到 2:3 次应告警
	cfg := DefaultRuleConfig()
	cfg[RulePodHighRestarts] = RuleSetting{Enabled: true, Threshold: 2}
	got := checkPod(pod, cfg)
	if len(got) != 1 || got[0].Rule != RulePodHighRestarts {
		t.Fatalf("阈值 2 期望命中 HighRestarts,实际 %+v", got)
	}
	// 阈值误存 0:回退默认 5,不告警
	cfg[RulePodHighRestarts] = RuleSetting{Enabled: true, Threshold: 0}
	if got := checkPod(pod, cfg); len(got) != 0 {
		t.Fatalf("阈值 0 应回退默认,实际 %+v", got)
	}
}

// fakeQuerier 固定返回值的 Prometheus 查询器。
type fakeQuerier struct{ values map[string]float64 }

func (f *fakeQuerier) QueryVector(_ context.Context, _ string) (map[string]float64, error) {
	return f.values, nil
}

func (f *fakeQuerier) Query(_ context.Context, _ string) ([]prom.Sample, error) {
	samples := make([]prom.Sample, 0, len(f.values))
	for instance, v := range f.values {
		samples = append(samples, prom.Sample{Labels: map[string]string{"instance": instance}, Value: v})
	}
	return samples, nil
}

// TestEvalCustomMetricRules 验证自定义指标规则:运算符比较、对象标签取值、查询器缺失。
func TestEvalCustomMetricRules(t *testing.T) {
	querier := &fakeQuerier{values: map[string]float64{"node-a:9100": 0.9, "node-b:9100": 0.2}}

	// 未注入查询器:空
	if got := evalCustomMetricRules(context.Background(), nil, []CustomMetricRuleSpec{{Name: "x", PromQL: "up", Operator: ">", Threshold: 0.5}}); len(got) != 0 {
		t.Fatalf("未注入查询器期望空,实际 %+v", got)
	}

	// > 0.5:仅 node-a 命中
	specs := []CustomMetricRuleSpec{{Name: "HighLoad", PromQL: "node_load1", Operator: ">", Threshold: 0.5, Severity: "warning"}}
	got := evalCustomMetricRules(context.Background(), querier, specs)
	if len(got) != 1 || got[0].Rule != "HighLoad" || got[0].Name != "node-a:9100" || got[0].Kind != "Metric" {
		t.Fatalf("期望仅 node-a 命中 HighLoad,实际 %+v", got)
	}

	// < 0.5:仅 node-b 命中
	specs = []CustomMetricRuleSpec{{Name: "LowLoad", PromQL: "node_load1", Operator: "<", Threshold: 0.5}}
	got = evalCustomMetricRules(context.Background(), querier, specs)
	if len(got) != 1 || got[0].Name != "node-b:9100" {
		t.Fatalf("期望仅 node-b 命中 LowLoad,实际 %+v", got)
	}

	// 未知运算符:不告警
	specs = []CustomMetricRuleSpec{{Name: "Bad", PromQL: "x", Operator: "~", Threshold: 0}}
	if got := evalCustomMetricRules(context.Background(), querier, specs); len(got) != 0 {
		t.Fatalf("未知运算符应不告警,实际 %+v", got)
	}
}

// TestEvalPromRules 验证指标规则:启用+超阈值才产出异常;未注入查询器返回空。
func TestEvalPromRules(t *testing.T) {
	// 未注入查询器:空
	if got := evalPromRules(context.Background(), nil, DefaultRuleConfig()); len(got) != 0 {
		t.Fatalf("未注入查询器期望空,实际 %+v", got)
	}

	querier := &fakeQuerier{values: map[string]float64{"node-a:9100": 95, "node-b:9100": 40}}

	// 默认配置下指标规则禁用:空
	if got := evalPromRules(context.Background(), querier, DefaultRuleConfig()); len(got) != 0 {
		t.Fatalf("规则默认禁用期望空,实际 %+v", got)
	}

	// 启用 NodeHighCPU(阈值 85):仅 node-a 超标(两条指标查询共用 fake 值,memory 规则未启用)
	cfg := DefaultRuleConfig()
	cfg[RuleNodeHighCPU] = RuleSetting{Enabled: true, Threshold: 85}
	got := evalPromRules(context.Background(), querier, cfg)
	if len(got) != 1 || got[0].Rule != RuleNodeHighCPU || got[0].Name != "node-a:9100" {
		t.Fatalf("期望仅 node-a 命中 NodeHighCPU,实际 %+v", got)
	}
}
