package inspect

import (
	"context"
	"sync"

	"aiops/internal/prom"
)

// RuleSetting 单条巡检规则的可调设置。
type RuleSetting struct {
	Enabled   bool    `json:"enabled"`
	Threshold float64 `json:"threshold"` // 语义随规则:HighRestarts=重启次数,NodeHighCPU/Memory=百分比;无阈值规则为 0
}

// RuleConfig 规则名 → 设置。缺失的规则按 DefaultRuleConfig 补全。
type RuleConfig map[string]RuleSetting

// RuleMeta 规则元信息(供设置页展示,不可由用户修改)。
type RuleMeta struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	Severity      string `json:"severity"`
	Source        string `json:"source"` // builtin=K8S 状态规则;prometheus=指标规则
	HasThreshold  bool   `json:"has_threshold"`
	ThresholdUnit string `json:"threshold_unit,omitempty"`
}

// 规则名常量(内置 + Prometheus 指标规则)。
const (
	RulePodCrashLoopBackOff = "PodCrashLoopBackOff"
	RulePodImagePullError   = "PodImagePullError"
	RulePodOOMKilled        = "PodOOMKilled"
	RulePodPending          = "PodPending"
	RulePodFailed           = "PodFailed"
	RulePodHighRestarts     = "PodHighRestarts"
	RulePodNotReady         = "PodNotReady"
	RuleNodeNotReady        = "NodeNotReady"
	RuleNodeHighCPU         = "NodeHighCPU"
	RuleNodeHighMemory      = "NodeHighMemory"
)

// RuleMetas 返回全部规则元信息(有序,供设置页渲染)。
func RuleMetas() []RuleMeta {
	return []RuleMeta{
		{Name: RulePodCrashLoopBackOff, Description: "Pod 反复崩溃重启(CrashLoopBackOff)", Severity: "critical", Source: "builtin"},
		{Name: RulePodImagePullError, Description: "Pod 镜像拉取失败(ImagePullBackOff / ErrImagePull)", Severity: "critical", Source: "builtin"},
		{Name: RulePodOOMKilled, Description: "Pod 因内存超限被杀(OOMKilled)", Severity: "critical", Source: "builtin"},
		{Name: RulePodFailed, Description: "Pod 处于 Failed 状态", Severity: "critical", Source: "builtin"},
		{Name: RulePodPending, Description: "Pod 长期 Pending(资源不足或调度失败)", Severity: "warning", Source: "builtin"},
		{Name: RulePodHighRestarts, Description: "Pod 重启次数超过阈值", Severity: "warning", Source: "builtin", HasThreshold: true, ThresholdUnit: "次"},
		{Name: RulePodNotReady, Description: "Pod Running 但容器未就绪", Severity: "warning", Source: "builtin"},
		{Name: RuleNodeNotReady, Description: "节点处于 NotReady 状态", Severity: "critical", Source: "builtin"},
		{Name: RuleNodeHighCPU, Description: "节点 CPU 使用率 5 分钟均值超过阈值(需接入 Prometheus)", Severity: "warning", Source: "prometheus", HasThreshold: true, ThresholdUnit: "%"},
		{Name: RuleNodeHighMemory, Description: "节点内存使用率超过阈值(需接入 Prometheus)", Severity: "warning", Source: "prometheus", HasThreshold: true, ThresholdUnit: "%"},
	}
}

// DefaultRuleConfig 内置默认配置:K8S 状态规则全部启用;Prometheus 指标规则默认关闭(需先接入)。
func DefaultRuleConfig() RuleConfig {
	return RuleConfig{
		RulePodCrashLoopBackOff: {Enabled: true},
		RulePodImagePullError:   {Enabled: true},
		RulePodOOMKilled:        {Enabled: true},
		RulePodPending:          {Enabled: true},
		RulePodFailed:           {Enabled: true},
		RulePodHighRestarts:     {Enabled: true, Threshold: 5},
		RulePodNotReady:         {Enabled: true},
		RuleNodeNotReady:        {Enabled: true},
		RuleNodeHighCPU:         {Enabled: false, Threshold: 85},
		RuleNodeHighMemory:      {Enabled: false, Threshold: 90},
	}
}

// setting 取规则当前设置;未知规则回退默认(默认里也没有则视为禁用)。
func (c RuleConfig) setting(rule string) RuleSetting {
	if s, ok := c[rule]; ok {
		return s
	}
	if s, ok := DefaultRuleConfig()[rule]; ok {
		return s
	}
	return RuleSetting{}
}

// enabled 规则是否启用。
func (c RuleConfig) enabled(rule string) bool { return c.setting(rule).Enabled }

// threshold 规则阈值(<=0 时回退默认阈值,防止误存 0 导致规则失效)。
func (c RuleConfig) threshold(rule string) float64 {
	v := c.setting(rule).Threshold
	if v <= 0 {
		v = DefaultRuleConfig()[rule].Threshold
	}
	return v
}

// ---- 包级注入:规则配置提供者 ----
// Engine 创建点分散在多处(默认集群、恢复集群、动态接入),规则配置来源(数据库)是全局一处,
// 故 provider 用包级注入(带 clusterID 参数支持集群覆盖);每轮巡检读取,天然热生效。
// Prometheus 查询器与集群一一对应,作为 Engine 实例字段注入(见 engine.go)。

// PromQuerier Prometheus 即时查询接口,由 prom 包实现。
type PromQuerier interface {
	// QueryVector 执行即时查询,返回 instance 标签 → 数值(内置指标规则用)。
	QueryVector(ctx context.Context, promql string) (map[string]float64, error)
	// Query 执行即时查询,返回带完整标签的样本列表(自定义指标规则用)。
	Query(ctx context.Context, promql string) ([]prom.Sample, error)
}

// CustomMetricRuleSpec 自定义指标规则的评估规格(由 server 从数据库转换而来,避免 inspect 依赖 store)。
type CustomMetricRuleSpec struct {
	Name        string
	PromQL      string
	Operator    string // > >= < <= ==
	Threshold   float64
	Severity    string // critical|warning
	ObjectLabel string // 取哪个标签作对象名,空则默认 instance
}

var (
	injectMu           sync.RWMutex
	ruleConfigProvider func(clusterID uint) RuleConfig
	customRuleProvider func(clusterID uint) []CustomMetricRuleSpec
)

// SetRuleConfigProvider 注入规则配置提供者(通常为读数据库的三层 merge 闭包);nil 表示使用默认配置。
func SetRuleConfigProvider(provider func(clusterID uint) RuleConfig) {
	injectMu.Lock()
	defer injectMu.Unlock()
	ruleConfigProvider = provider
}

// currentRuleConfig 取指定集群当前生效规则配置(provider 缺失或返回 nil 时回退默认)。
func currentRuleConfig(clusterID uint) RuleConfig {
	injectMu.RLock()
	provider := ruleConfigProvider
	injectMu.RUnlock()
	if provider != nil {
		if cfg := provider(clusterID); cfg != nil {
			return cfg
		}
	}
	return DefaultRuleConfig()
}

// SetCustomRuleProvider 注入自定义指标规则提供者(读数据库:全局 + 该集群、仅启用);nil 表示无自定义规则。
func SetCustomRuleProvider(provider func(clusterID uint) []CustomMetricRuleSpec) {
	injectMu.Lock()
	defer injectMu.Unlock()
	customRuleProvider = provider
}

// currentCustomRules 取指定集群当前生效的自定义指标规则。
func currentCustomRules(clusterID uint) []CustomMetricRuleSpec {
	injectMu.RLock()
	provider := customRuleProvider
	injectMu.RUnlock()
	if provider != nil {
		return provider(clusterID)
	}
	return nil
}
