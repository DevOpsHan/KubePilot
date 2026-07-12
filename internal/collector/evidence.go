package collector

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// 证据采集限额:防止把过长日志喂给大模型导致 token 爆炸。
const (
	evidenceLogTailLines = 50
	evidenceMaxEvents    = 10
	evidenceMaxBytes     = 4000
)

// PodEvidence 采集 Pod 的现场证据:容器状态详情 + 关联事件 + 崩溃日志。
// 三段各自独立容错(某段失败不影响其它段),供 AI 诊断引用定位具体根因。
func (c *Collector) PodEvidence(ctx context.Context, namespace, name string) string {
	if c == nil || c.clientset == nil {
		return ""
	}
	var sections []string
	if detail := c.podStatusDetail(ctx, namespace, name); detail != "" {
		sections = append(sections, "== 容器状态 ==\n"+detail)
	}
	if events := c.objectEvents(ctx, namespace, name); events != "" {
		sections = append(sections, "== 相关事件 ==\n"+events)
	}
	if logs := c.podCrashLogs(ctx, namespace, name); logs != "" {
		sections = append(sections, "== 容器日志(最近 "+fmt.Sprint(evidenceLogTailLines)+" 行)==\n"+logs)
	}
	return truncateEvidence(strings.Join(sections, "\n\n"))
}

// NodeEvidence 采集节点现场证据:conditions + 关联事件。
func (c *Collector) NodeEvidence(ctx context.Context, name string) string {
	if c == nil || c.clientset == nil {
		return ""
	}
	var sections []string
	node, err := c.clientset.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		sections = append(sections, "== 节点状态 ==\n(采集失败: "+err.Error()+")")
	} else {
		var lines []string
		for _, cond := range node.Status.Conditions {
			lines = append(lines, fmt.Sprintf("- %s=%s reason=%s message=%s", cond.Type, cond.Status, cond.Reason, cond.Message))
		}
		sections = append(sections, "== 节点状态 ==\n"+strings.Join(lines, "\n"))
	}
	if events := c.objectEvents(ctx, "", name); events != "" {
		sections = append(sections, "== 相关事件 ==\n"+events)
	}
	return truncateEvidence(strings.Join(sections, "\n\n"))
}

// podStatusDetail 提取容器状态详情(waiting/terminated 原因、退出码、重启次数、上次终止状态)。
func (c *Collector) podStatusDetail(ctx context.Context, namespace, name string) string {
	pod, err := c.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "(采集失败: " + err.Error() + ")"
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Phase=%s 节点=%s", pod.Status.Phase, pod.Spec.NodeName))
	if pod.Status.Reason != "" || pod.Status.Message != "" {
		lines = append(lines, fmt.Sprintf("Pod 级原因: %s %s", pod.Status.Reason, pod.Status.Message))
	}
	describe := func(prefix string, cs corev1.ContainerStatus) {
		state := "running"
		detail := ""
		switch {
		case cs.State.Waiting != nil:
			state = "waiting"
			detail = fmt.Sprintf("reason=%s message=%s", cs.State.Waiting.Reason, cs.State.Waiting.Message)
		case cs.State.Terminated != nil:
			state = "terminated"
			detail = fmt.Sprintf("reason=%s exitCode=%d message=%s", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode, cs.State.Terminated.Message)
		}
		line := fmt.Sprintf("- %s%s: 状态=%s 重启=%d次 %s", prefix, cs.Name, state, cs.RestartCount, detail)
		if cs.LastTerminationState.Terminated != nil {
			t := cs.LastTerminationState.Terminated
			line += fmt.Sprintf(" | 上次终止: reason=%s exitCode=%d", t.Reason, t.ExitCode)
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		describe("init容器 ", cs)
	}
	for _, cs := range pod.Status.ContainerStatuses {
		describe("容器 ", cs)
	}
	return strings.Join(lines, "\n")
}

// objectEvents 取对象关联事件(按时间倒序,最多 evidenceMaxEvents 条)。namespace 为空用于节点等集群级对象。
func (c *Collector) objectEvents(ctx context.Context, namespace, name string) string {
	list, err := c.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name,
	})
	if err != nil {
		return "(采集失败: " + err.Error() + ")"
	}
	items := list.Items
	sort.Slice(items, func(i, j int) bool {
		return eventTime(&items[i]).Time.After(eventTime(&items[j]).Time)
	})
	if len(items) > evidenceMaxEvents {
		items = items[:evidenceMaxEvents]
	}
	var lines []string
	for i := range items {
		ev := &items[i]
		lines = append(lines, fmt.Sprintf("- [%s] %s x%d: %s", ev.Type, ev.Reason, maxCount(ev), ev.Message))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func eventTime(ev *corev1.Event) metav1.Time {
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp
	}
	return ev.CreationTimestamp
}

func maxCount(ev *corev1.Event) int32 {
	if ev.Count > 0 {
		return ev.Count
	}
	return 1
}

// podCrashLogs 取崩溃日志:优先上一次终止的容器日志(--previous),失败回退当前容器日志。
func (c *Collector) podCrashLogs(ctx context.Context, namespace, name string) string {
	tail := int64(evidenceLogTailLines)
	read := func(previous bool) (string, error) {
		req := c.clientset.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{
			Previous:  previous,
			TailLines: &tail,
		})
		stream, err := req.Stream(ctx)
		if err != nil {
			return "", err
		}
		defer stream.Close()
		data, err := io.ReadAll(io.LimitReader(stream, evidenceMaxBytes))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	if logs, err := read(true); err == nil && logs != "" {
		return logs
	}
	logs, err := read(false)
	if err != nil {
		return "(日志采集失败: " + err.Error() + ")"
	}
	if logs == "" {
		return "(容器暂无日志输出)"
	}
	return logs
}

// truncateEvidence 限制证据总长(保留头部结构与日志尾部关键信息)。
func truncateEvidence(s string) string {
	if len(s) <= evidenceMaxBytes {
		return s
	}
	// 头部保结构(状态+事件),尾部保日志最后的报错
	head := s[:evidenceMaxBytes*3/5]
	tail := s[len(s)-evidenceMaxBytes*1/5:]
	return head + "\n…(证据过长已截断)…\n" + tail
}
