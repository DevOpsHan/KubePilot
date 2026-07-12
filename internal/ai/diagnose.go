package ai

import (
	"context"
	"fmt"

	"aiops/internal/inspect"
)

// Diagnoser 用大模型对巡检异常做根因诊断,实现 inspect.Diagnoser 接口。
type Diagnoser struct {
	client *Client
}

// NewDiagnoser 创建诊断器。
func NewDiagnoser(client *Client) *Diagnoser {
	return &Diagnoser{client: client}
}

const diagnoseSystemPrompt = `你是一位资深的 Kubernetes 运维专家。针对给定的集群异常,用中文给出诊断,严格包含三部分:
【根因分析】这个异常的具体原因。若提供了「现场证据」(容器状态/事件/日志),必须引用其中的关键报错原文来定位根因,不要泛泛而谈;仅在无证据时才给常见原因。
【影响评估】对服务的影响。
【修复建议】针对根因的可执行修复步骤(给出 kubectl 命令或需要修改的具体配置)。
三部分之后,必须另起一行,只输出一行机器可读的建议动作,格式严格为:
建议动作: <值>
其中 <值> 只能取以下之一:
- restart_pod:重启该 Pod 即可自愈(如偶发崩溃、句柄泄漏)
- restart_deployment:需滚动重启整个 Deployment(如坏模板导致所有副本同样故障)
- delete_pod:删除该 Pod 让控制器重建(如单副本卡死)
- none:不宜自动修复(如镜像/配置错误、资源不足、节点故障,需人工介入)
总字数控制在 300 字以内(不含建议动作行),直接给结论,不要寒暄。`

// Diagnose 对单条异常生成诊断文本。异常携带现场证据时一并喂给模型,以定位具体根因。
func (d *Diagnoser) Diagnose(ctx context.Context, a *inspect.Anomaly) (string, error) {
	userPrompt := fmt.Sprintf(
		"请诊断以下 Kubernetes 集群异常:\n- 资源类型: %s\n- 命名空间: %s\n- 名称: %s\n- 严重级别: %s\n- 命中规则: %s\n- K8S 原因: %s\n- 描述: %s",
		a.Kind, a.Namespace, a.Name, a.Severity, a.Rule, a.Reason, a.Message)
	if a.Evidence != "" {
		userPrompt += "\n\n【现场证据】\n" + a.Evidence
	}
	return d.client.Chat(ctx, diagnoseSystemPrompt, userPrompt)
}
