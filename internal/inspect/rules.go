package inspect

import (
	"fmt"
	"strings"

	"aiops/internal/collector"
)

// checkPod 对单个 Pod 应用所有启用的规则,返回命中的异常(通常 0 或 1 条)。
func checkPod(p collector.PodInfo, cfg RuleConfig) []Anomaly {
	var out []Anomaly
	add := func(sev Severity, rule, msg string) {
		out = append(out, Anomaly{
			Severity:  sev,
			Kind:      "Pod",
			Namespace: p.Namespace,
			Name:      p.Name,
			Rule:      rule,
			Reason:    p.Reason,
			Message:   msg,
		})
	}

	switch {
	case p.Reason == "CrashLoopBackOff":
		if cfg.enabled(RulePodCrashLoopBackOff) {
			add(SeverityCritical, RulePodCrashLoopBackOff,
				fmt.Sprintf("Pod %s/%s 反复崩溃重启(CrashLoopBackOff),已重启 %d 次", p.Namespace, p.Name, p.Restarts))
		}
	case strings.Contains(p.Reason, "ImagePull") || p.Reason == "ErrImagePull":
		if cfg.enabled(RulePodImagePullError) {
			add(SeverityCritical, RulePodImagePullError,
				fmt.Sprintf("Pod %s/%s 镜像拉取失败(%s)", p.Namespace, p.Name, p.Reason))
		}
	case p.Reason == "OOMKilled":
		if cfg.enabled(RulePodOOMKilled) {
			add(SeverityCritical, RulePodOOMKilled,
				fmt.Sprintf("Pod %s/%s 因内存超限被杀(OOMKilled)", p.Namespace, p.Name))
		}
	case p.Phase == "Pending":
		if cfg.enabled(RulePodPending) {
			add(SeverityWarning, RulePodPending,
				fmt.Sprintf("Pod %s/%s 处于 Pending,可能资源不足或调度失败", p.Namespace, p.Name))
		}
	case p.Phase == "Failed":
		if cfg.enabled(RulePodFailed) {
			add(SeverityCritical, RulePodFailed,
				fmt.Sprintf("Pod %s/%s 处于 Failed 状态", p.Namespace, p.Name))
		}
	}

	// 重启过多(已是 CrashLoopBackOff 则不重复告警);阈值可配置
	restartThreshold := int32(cfg.threshold(RulePodHighRestarts))
	if cfg.enabled(RulePodHighRestarts) && p.Restarts > restartThreshold && p.Reason != "CrashLoopBackOff" {
		add(SeverityWarning, RulePodHighRestarts,
			fmt.Sprintf("Pod %s/%s 重启次数过多(%d 次,阈值 %d 次)", p.Namespace, p.Name, p.Restarts, restartThreshold))
	}

	// Running 但容器未就绪(且无其他明确异常原因)
	if cfg.enabled(RulePodNotReady) && p.Phase == "Running" && isNotReady(p.Ready) && p.Reason == "" {
		add(SeverityWarning, RulePodNotReady,
			fmt.Sprintf("Pod %s/%s 处于 Running 但容器未就绪(%s)", p.Namespace, p.Name, p.Ready))
	}
	return out
}

// checkNode 对单个节点应用规则。
func checkNode(n collector.NodeInfo, cfg RuleConfig) []Anomaly {
	if cfg.enabled(RuleNodeNotReady) && !n.Ready {
		return []Anomaly{{
			Severity: SeverityCritical,
			Kind:     "Node",
			Name:     n.Name,
			Rule:     RuleNodeNotReady,
			Reason:   "NotReady",
			Message:  fmt.Sprintf("节点 %s 处于 NotReady 状态,其上 Pod 可能受影响", n.Name),
		}}
	}
	return nil
}

// isNotReady 判断 "x/y" 形式的就绪串是否未全就绪(如 "0/1")。
func isNotReady(ready string) bool {
	parts := strings.SplitN(ready, "/", 2)
	if len(parts) != 2 {
		return false
	}
	return parts[0] != parts[1]
}
