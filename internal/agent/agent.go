package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"aiops/internal/ai"
	"aiops/internal/session"
)

// systemPrompt 设定助手身份与行为准则。
const systemPrompt = `你是"小智",一个企业级 Kubernetes 智能运维助手。
你可以调用工具查询集群状态(概览、Pod 列表、节点、巡检)来回答用户的运维问题。
行为准则:
- 需要集群实时数据时,主动调用相应工具获取,绝不凭空编造数据。
- 默认面向平台内所有已接入 Kubernetes 集群进行分析;只有用户明确指定某个集群名称或编号时,才把工具调用限制到该集群。
- 如果用户指定的是集群名称,先确认平台内的集群列表,再按名称或编号查询。
- 当工具结果包含多个集群时,必须按集群分段回答,分别说明每个集群的状态、异常和建议,最后给出整体结论。
- 回复语言只根据最新一条用户消息的主要自然语言决定,同一会话允许逐轮切换语言。
- 纯英文问题使用英文回复,纯中文问题使用中文回复。
- 中英文混合时,忽略 Kubernetes 名称、资源标识、命令、代码、日志、YAML、PromQL 和工具返回内容,按其余主要自然语言回复。
- 最新消息无法判断自然语言时,沿用最近一条可判断的用户消息语言;仍无法判断时默认使用中文。
- 工具返回内容保持原文;工具调用前后的最终回答必须遵循同一回复语言规则。
- 回答简洁专业;涉及故障时给出根因与可执行的修复建议(kubectl 命令)。
- 排查故障时按此顺序取证:先用 pod_evidence 获取 Pod 现场全貌(容器状态/事件/崩溃日志),必要时用 read_pod_logs 查看更多应用日志,再用 exec_diagnostic 在容器内跑只读命令进一步定位(如查看配置、环境变量、DNS)。
- exec_diagnostic 只能执行白名单只读命令(cat/ls/env/ps/df 等),严禁写操作、shell 元字符或任意网络外联(curl/wget/getent 等);DNS 仅可用 nslookup kubernetes.default 执行固定 Kubernetes 健康探针,不要尝试拼接 shell、下载脚本或以任何方式绕过白名单。
- 任何修改集群的写操作(重启/删除/扩缩容等)都必须通过 propose_remediation 创建审批工单,由人工批准后执行,你绝不直接执行写操作。
- 如果用户只是问候、闲聊或问"你是谁",直接回答,无需调用工具。
- 自我介绍时说明你是 AIOps 平台的智能运维助手小智。
- 你拥有对话记忆,请结合之前的对话内容回答用户。`

const maxSteps = 6

// Agent 对话式运维助手:ReAct 循环 + 工具调用 + 会话记忆(Redis)。
type Agent struct {
	mu       sync.RWMutex
	client   *ai.Client
	executor *ToolExecutor
	store    session.Store
}

// New 创建 Agent。
func New(client *ai.Client, executor *ToolExecutor, store session.Store) *Agent {
	if store == nil {
		store = session.NewMemoryStore()
	}
	return &Agent{client: client, executor: executor, store: store}
}

// SetClient 热切换 Agent 使用的大模型客户端。
func (a *Agent) SetClient(client *ai.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = client
}

// Ready 返回 Agent 是否已有可用模型客户端。
func (a *Agent) Ready() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client != nil
}

// ActiveModelName 返回当前生效的模型名称。
func (a *Agent) ActiveModelName() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client.ModelName()
}

func (a *Agent) clientSnapshot() *ai.Client {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client
}

// StreamEvent 流式事件,实时推给前端展示推理过程。
type StreamEvent struct {
	Type       string `json:"type"`                  // thinking | step | step_done | answer | error | done
	Tool       string `json:"tool,omitempty"`        // 工具名
	Command    string `json:"command,omitempty"`     // 等价命令描述(kubectl 风格)
	Args       string `json:"args,omitempty"`        // 工具参数
	Content    string `json:"content,omitempty"`     // 回答/错误内容
	DurationMs int64  `json:"duration_ms,omitempty"` // 单步耗时(step_done)或整轮总耗时(answer)
}

// ChatStream 流式处理一轮对话:边推理边推送事件(含命令与耗时)。会话历史来自 Redis。
func (a *Agent) ChatStream(ctx context.Context, sessionID, userInput string, clusterID uint, identity UserIdentity, access ChatAccessScope, emit func(StreamEvent)) {
	startAll := time.Now()
	userMsg := ai.Message{Role: "user", Content: userInput}
	if answer, ok := identityAnswer(userInput, identity); ok {
		emit(StreamEvent{Type: "answer", Content: answer, DurationMs: time.Since(startAll).Milliseconds()})
		_ = a.store.Append(ctx, sessionID, userMsg, ai.Message{Role: "assistant", Content: answer})
		emit(StreamEvent{Type: "done"})
		return
	}
	client := a.clientSnapshot()
	if client == nil {
		emit(StreamEvent{Type: "error", Content: "大模型未配置,请先在模型管理中启用默认模型"})
		return
	}

	history, err := a.store.Get(ctx, sessionID)
	if err != nil {
		history = nil // 记忆读取失败,降级为无历史
	}
	messages := []ai.Message{{Role: "system", Content: systemPromptForIdentity(identity)}}
	messages = append(messages, history...)
	messages = append(messages, userMsg)

	tools := definitionsForAccess(access)

	if a.executor != nil {
		if plan, ok := a.executor.PlanShortcut(userInput, clusterID, access); ok {
			messages = a.executePlannedTool(ctx, messages, plan, access, identity, emit)
		}
	}

	for i := 0; i < maxSteps; i++ {
		emit(StreamEvent{Type: "thinking"})
		msg, cerr := client.CompleteStream(ctx, messages, tools, func(tok string) {
			emit(StreamEvent{Type: "token", Content: tok}) // 逐 token 流式推给前端
		})
		if cerr != nil {
			emit(StreamEvent{Type: "error", Content: cerr.Error()})
			return
		}
		messages = append(messages, *msg)

		// 没有工具调用 = 最终回答
		if len(msg.ToolCalls) == 0 {
			answer := strings.TrimSpace(msg.Content)
			emit(StreamEvent{Type: "answer", Content: answer, DurationMs: time.Since(startAll).Milliseconds()})
			_ = a.store.Append(ctx, sessionID, userMsg, ai.Message{Role: "assistant", Content: answer})
			emit(StreamEvent{Type: "done"})
			return
		}

		// 逐个执行工具:先推"开始(含命令)",执行计时后推"完成(含耗时)"
		for _, tc := range msg.ToolCalls {
			emit(StreamEvent{
				Type:    "step",
				Tool:    tc.Function.Name,
				Command: commandFor(tc.Function.Name, tc.Function.Arguments),
				Args:    tc.Function.Arguments,
			})
			start := time.Now()
			result := a.executor.Execute(ctx, clusterID, access, identity, tc.Function.Name, tc.Function.Arguments)
			emit(StreamEvent{
				Type:       "step_done",
				Tool:       tc.Function.Name,
				DurationMs: time.Since(start).Milliseconds(),
			})
			messages = append(messages, ai.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
		}
	}

	emit(StreamEvent{Type: "answer", Content: "(推理步数过多,未能得出结论,请换个更具体的问法)", DurationMs: time.Since(startAll).Milliseconds()})
	emit(StreamEvent{Type: "done"})
}

func (a *Agent) executePlannedTool(ctx context.Context, messages []ai.Message, plan shortcutToolPlan, access ChatAccessScope, identity UserIdentity, emit func(StreamEvent)) []ai.Message {
	toolCallID := "shortcut-" + plan.Name
	messages = append(messages, ai.Message{
		Role: "assistant",
		// 预置快捷工具是平台直接注入的调用,并非模型生成;思考模式接口要求带 tool_calls 的
		// assistant 消息必须携带 reasoning_content,这里补一个占位说明,避免下一轮请求被判 400。
		ReasoningContent: "根据用户请求,直接执行预置的集群查询工具。",
		ToolCalls: []ai.ToolCall{{
			ID:   toolCallID,
			Type: "function",
			Function: ai.FunctionCall{
				Name:      plan.Name,
				Arguments: plan.Args,
			},
		}},
	})
	emit(StreamEvent{
		Type:    "step",
		Tool:    plan.Name,
		Command: commandFor(plan.Name, plan.Args),
		Args:    plan.Args,
	})
	start := time.Now()
	result := a.executor.Execute(ctx, plan.RequestClusterID, access, identity, plan.Name, plan.Args)
	emit(StreamEvent{
		Type:       "step_done",
		Tool:       plan.Name,
		DurationMs: time.Since(start).Milliseconds(),
	})
	return append(messages, ai.Message{Role: "tool", ToolCallID: toolCallID, Content: result})
}

// History 返回会话历史。
func (a *Agent) History(ctx context.Context, sessionID string) ([]ai.Message, error) {
	return a.store.Get(ctx, sessionID)
}

// ClearSession 清空指定会话。
func (a *Agent) ClearSession(ctx context.Context, sessionID string) error {
	return a.store.Clear(ctx, sessionID)
}
