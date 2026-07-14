// Package agent 实现对话式运维助手(ReAct + 工具调用 + 会话记忆)。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"aiops/internal/ai"
	"aiops/internal/clusterruntime"
	"aiops/internal/clustersync"
	"aiops/internal/collector"
	"aiops/internal/inspect"
	"aiops/internal/remediate"
	"aiops/internal/store"
)

const defaultDiagnosticExecTimeout = 15 * time.Second

type execCaptureFunc func(context.Context, *collector.Collector, string, string, string, []string) (string, string, error)

// ToolExecutor 执行 Agent 工具:只读查询 + 修复提议(只建审批工单,不直接执行)。
type ToolExecutor struct {
	collector        *collector.Collector
	engine           *inspect.Engine
	store            *store.Store
	remediator       *remediate.Executor
	defaultClusterID uint
	runtimeManager   *clusterruntime.Manager
	execCapture      execCaptureFunc
	// diagnosticExecTimeout 为 AI 非交互诊断设置独立硬超时,避免命令或连接无限占用资源。
	diagnosticExecTimeout time.Duration
}

// NewToolExecutor 创建工具执行器。store/remediator 用于"修复提议"建工单与开关校验。
func NewToolExecutor(col *collector.Collector, engine *inspect.Engine, st *store.Store, rem *remediate.Executor, defaultClusterID uint, runtimeManager *clusterruntime.Manager) *ToolExecutor {
	return &ToolExecutor{
		collector:        col,
		engine:           engine,
		store:            st,
		remediator:       rem,
		defaultClusterID: defaultClusterID,
		runtimeManager:   runtimeManager,
		execCapture: func(ctx context.Context, col *collector.Collector, namespace, pod, container string, command []string) (string, string, error) {
			return col.ExecCapture(ctx, namespace, pod, container, command)
		},
		diagnosticExecTimeout: defaultDiagnosticExecTimeout,
	}
}

// Definitions 返回提供给模型的工具 schema 定义。
func Definitions() []ai.Tool {
	emptyParams := map[string]any{"type": "object", "properties": map[string]any{}}
	clusterScopeProps := map[string]any{
		"cluster_id":   map[string]any{"type": "integer", "description": "可选;指定集群 ID。留空表示所有集群"},
		"cluster_name": map[string]any{"type": "string", "description": "可选;指定集群名称或编号。留空表示所有集群"},
	}
	return []ai.Tool{
		{Type: "function", Function: ai.FunctionDef{
			Name:        "list_clusters",
			Description: "列出平台已接入的 Kubernetes 集群。用户提到具体集群名称但不确定编号时先调用它。",
			Parameters:  emptyParams,
		}},
		{Type: "function", Function: ai.FunctionDef{
			Name:        "cluster_overview",
			Description: "获取集群整体概览:节点总数、就绪节点数、Pod 总数、各状态 Pod 统计、命名空间数。默认查询所有集群。",
			Parameters: map[string]any{
				"type":       "object",
				"properties": clusterScopeProps,
			},
		}},
		{Type: "function", Function: ai.FunctionDef{
			Name:        "list_pods",
			Description: "列出 Pod 及其状态(阶段/就绪/重启次数/异常原因)。默认查询所有集群,可用 namespace 参数过滤。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace":    map[string]any{"type": "string", "description": "命名空间;留空表示全部命名空间"},
					"cluster_id":   clusterScopeProps["cluster_id"],
					"cluster_name": clusterScopeProps["cluster_name"],
				},
			},
		}},
		{Type: "function", Function: ai.FunctionDef{
			Name:        "list_nodes",
			Description: "列出集群所有节点及其 Ready 状态与 Kubernetes 版本。默认查询所有集群。",
			Parameters: map[string]any{
				"type":       "object",
				"properties": clusterScopeProps,
			},
		}},
		{Type: "function", Function: ai.FunctionDef{
			Name:        "node_metrics",
			Description: "获取节点 CPU 与内存实时使用情况。默认查询所有集群;需要集群已部署 metrics-server。",
			Parameters: map[string]any{
				"type":       "object",
				"properties": clusterScopeProps,
			},
		}},
		{Type: "function", Function: ai.FunctionDef{
			Name:        "run_inspection",
			Description: "执行一次规则巡检。默认先同步并巡检所有集群;只有用户明确指定集群时才巡检单个集群。",
			Parameters: map[string]any{
				"type":       "object",
				"properties": clusterScopeProps,
			},
		}},
		{Type: "function", Function: ai.FunctionDef{
			Name:        "read_pod_logs",
			Description: "读取指定 Pod 的日志尾部(只读)。诊断 CrashLoopBackOff、报错、启动失败等问题时用它查看应用日志。必须明确指定单个集群。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace":    map[string]any{"type": "string", "description": "命名空间"},
					"pod":          map[string]any{"type": "string", "description": "Pod 名称"},
					"container":    map[string]any{"type": "string", "description": "容器名;多容器 Pod 时指定,留空取默认容器"},
					"tail":         map[string]any{"type": "integer", "description": "读取尾部行数,留空默认 50 行"},
					"cluster_id":   clusterScopeProps["cluster_id"],
					"cluster_name": clusterScopeProps["cluster_name"],
				},
				"required": []string{"namespace", "pod"},
			},
		}},
		{Type: "function", Function: ai.FunctionDef{
			Name:        "pod_evidence",
			Description: "采集指定 Pod 的现场证据:容器状态详情(waiting/terminated 原因、退出码、重启次数)+ 关联事件 + 崩溃日志。诊断 Pod 异常时优先调用它获取全貌。必须明确指定单个集群。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace":    map[string]any{"type": "string", "description": "命名空间"},
					"pod":          map[string]any{"type": "string", "description": "Pod 名称"},
					"cluster_id":   clusterScopeProps["cluster_id"],
					"cluster_name": clusterScopeProps["cluster_name"],
				},
				"required": []string{"namespace", "pod"},
			},
		}},
		{Type: "function", Function: ai.FunctionDef{
			Name: "exec_diagnostic",
			Description: "在 Pod 容器内执行【只读】诊断命令并返回输出。仅允许白名单只读命令(如 cat/ls/env/ps/df/cat /etc/resolv.conf/nslookup 等)," +
				"禁止任何写操作、shell 元字符、外联命令。用于进一步定位配置/环境/DNS 等问题。必须明确指定单个集群。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace":    map[string]any{"type": "string", "description": "命名空间"},
					"pod":          map[string]any{"type": "string", "description": "Pod 名称"},
					"container":    map[string]any{"type": "string", "description": "容器名;留空取默认容器"},
					"command":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "命令 argv 数组,如 [\"cat\",\"/etc/resolv.conf\"];首个元素必须是白名单命令"},
					"cluster_id":   clusterScopeProps["cluster_id"],
					"cluster_name": clusterScopeProps["cluster_name"],
				},
				"required": []string{"namespace", "pod", "command"},
			},
		}},
		{Type: "function", Function: ai.FunctionDef{
			Name: "propose_remediation",
			Description: "提议一个修复操作。注意:这只会创建【审批工单】,绝不立即执行,必须人工在审批中心批准后才执行。" +
				"当你诊断出故障且确定修复方案时调用。任何命名空间的写操作都必须先经过人工审批。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":    map[string]any{"type": "string", "enum": []string{"restart_pod", "delete_pod", "scale_deployment", "restart_deployment"}, "description": "修复动作"},
					"namespace": map[string]any{"type": "string", "description": "目标命名空间"},
					"target":    map[string]any{"type": "string", "description": "目标资源名(Pod 或 Deployment)"},
					"replicas":  map[string]any{"type": "integer", "description": "仅 scale_deployment 需要:目标副本数"},
					"reason":    map[string]any{"type": "string", "description": "修复理由(给审批人看)"},
					"risk":      map[string]any{"type": "string", "description": "风险等级:low/medium/high"},
				},
				"required": []string{"action", "namespace", "target", "reason"},
			},
		}},
	}
}

// commandFor 返回工具对应的等价命令描述(给用户看,kubectl 风格)。
func commandFor(name, argsJSON string) string {
	switch name {
	case "list_clusters":
		return "列出平台已接入集群"
	case "cluster_overview":
		return "kubectl get nodes,pods,ns -A  # 汇总集群概览(默认所有集群)"
	case "list_pods":
		var a struct {
			Namespace string `json:"namespace"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &a)
		if a.Namespace != "" {
			return "kubectl get pods -n " + a.Namespace
		}
		return "kubectl get pods -A"
	case "list_nodes":
		return "kubectl get nodes -o wide  # 默认所有集群"
	case "node_metrics":
		return "kubectl top nodes  # 默认所有集群节点 CPU/内存"
	case "run_inspection":
		return "同步并巡检所有集群:扫描 Pod/Node 异常规则"
	case "read_pod_logs":
		var a struct {
			Namespace string `json:"namespace"`
			Pod       string `json:"pod"`
			Container string `json:"container"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &a)
		cmd := "kubectl logs -n " + a.Namespace + " " + a.Pod
		if a.Container != "" {
			cmd += " -c " + a.Container
		}
		return cmd + " --tail 50"
	case "pod_evidence":
		var a struct {
			Namespace string `json:"namespace"`
			Pod       string `json:"pod"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &a)
		return "kubectl describe pod -n " + a.Namespace + " " + a.Pod + "  # 状态+事件+崩溃日志"
	case "exec_diagnostic":
		var a struct {
			Namespace string   `json:"namespace"`
			Pod       string   `json:"pod"`
			Container string   `json:"container"`
			Command   []string `json:"command"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &a)
		cmd := "kubectl exec -n " + a.Namespace + " " + a.Pod
		if a.Container != "" {
			cmd += " -c " + a.Container
		}
		return cmd + " -- " + strings.Join(a.Command, " ") + "  # 只读诊断"
	case "propose_remediation":
		return "创建修复审批工单(待人工批准)"
	default:
		return name
	}
}

func (e *ToolExecutor) runtimeForCluster(clusterID uint) (*clusterruntime.Runtime, error) {
	if clusterID == 0 {
		clusterID = e.defaultClusterID
	}
	if e.runtimeManager != nil {
		return e.runtimeManager.Get(clusterID)
	}
	if clusterID == 0 || clusterID == e.defaultClusterID {
		return &clusterruntime.Runtime{
			ClusterID:  clusterID,
			Collector:  e.collector,
			Engine:     e.engine,
			Remediator: e.remediator,
		}, nil
	}
	return nil, fmt.Errorf("集群 %d 的运行时未就绪", clusterID)
}

type clusterTarget struct {
	ID      uint
	Name    string
	Runtime *clusterruntime.Runtime
	Err     error
}

type toolClusterArgs struct {
	ClusterID   uint   `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
}

type shortcutToolPlan struct {
	Name             string
	Args             string
	RequestClusterID uint
}

// PlanShortcut 将常见对话快捷指令转为确定性的只读工具调用。
// 这样"巡检一下"这类高频入口无需依赖模型是否主动选择工具。
func (e *ToolExecutor) PlanShortcut(userInput string, requestClusterID uint, access ChatAccessScope) (shortcutToolPlan, bool) {
	toolName := shortcutToolName(userInput)
	if toolName == "" {
		return shortcutToolPlan{}, false
	}
	args := "{}"
	effectiveRequestClusterID := uint(0)
	if clusterID, ok := e.clusterIDFromText(userInput); ok {
		if !access.AllowsCluster(clusterID) {
			return shortcutToolPlan{}, false
		}
		args = toJSON(map[string]any{"cluster_id": clusterID})
	} else if requestClusterID != 0 && mentionsCurrentCluster(userInput) && !mentionsAllClusters(userInput) {
		effectiveRequestClusterID = requestClusterID
	}
	return shortcutToolPlan{
		Name:             toolName,
		Args:             args,
		RequestClusterID: effectiveRequestClusterID,
	}, true
}

func shortcutToolName(userInput string) string {
	text := normalizeIntentText(userInput)
	if text == "" {
		return ""
	}
	if containsAny(text, "巡检", "一键巡检", "体检", "健康检查", "检查一下", "检查集群", "有没有异常", "不健康", "故障") {
		return "run_inspection"
	}
	if containsAny(text, "异常pod", "异常 pod", "pod异常", "pod 异常", "不健康pod", "不健康 pod") {
		return "run_inspection"
	}
	if containsAny(text, "集群概览", "整体概览", "整体情况", "集群情况", "资源概览") {
		return "cluster_overview"
	}
	if containsAny(text, "性能监控", "性能", "cpu", "内存", "资源使用", "使用率", "负载") {
		return "node_metrics"
	}
	if containsAny(text, "节点状态", "node状态", "node 状态", "节点列表", "所有节点") {
		return "list_nodes"
	}
	if containsAny(text, "pod状态", "pod 状态", "pod列表", "pod 列表") {
		return "list_pods"
	}
	return ""
}

func (e *ToolExecutor) clusterIDFromText(userInput string) (uint, bool) {
	if e == nil || e.store == nil {
		return 0, false
	}
	text := normalizeIntentText(userInput)
	clusters, err := e.store.ListClusters()
	if err != nil {
		return 0, false
	}
	for _, cluster := range clusters {
		for _, candidate := range clusterMentionCandidates(cluster) {
			if candidate != "" && strings.Contains(text, candidate) {
				return cluster.ID, true
			}
		}
	}
	return 0, false
}

func clusterMentionCandidates(cluster store.Cluster) []string {
	id := fmt.Sprintf("%d", cluster.ID)
	return []string{
		normalizeIntentText(cluster.Name),
		normalizeIntentText(cluster.Slug),
		clusterCode(cluster.ID),
		"集群" + id,
		"集群 " + id,
		"cluster" + id,
		"cluster " + id,
		"编号" + id,
		"编号 " + id,
		"id" + id,
		"id " + id,
	}
}

func normalizeIntentText(text string) string {
	text = strings.TrimSpace(strings.ToLower(text))
	text = strings.ReplaceAll(text, "，", ",")
	text = strings.ReplaceAll(text, "。", ".")
	text = strings.ReplaceAll(text, "：", ":")
	return text
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, normalizeIntentText(needle)) {
			return true
		}
	}
	return false
}

func mentionsAllClusters(userInput string) bool {
	text := normalizeIntentText(userInput)
	return containsAny(text, "所有集群", "全部集群", "全集群", "所有已接入集群", "全部已接入集群")
}

func mentionsCurrentCluster(userInput string) bool {
	text := normalizeIntentText(userInput)
	return containsAny(text, "当前集群", "这个集群", "该集群", "本集群")
}

// Execute 在服务端认证访问范围内执行指定工具,返回文本结果供模型阅读。
// identity 为触发本次对话的登录用户身份,用于给 AI 发起的写/诊断操作打审计标记。
func (e *ToolExecutor) Execute(ctx context.Context, clusterID uint, access ChatAccessScope, identity UserIdentity, name, argsJSON string) string {
	if !access.allowsTool(name) {
		return access.deniedToolMessage(name)
	}
	switch name {
	case "list_clusters":
		return e.listClusters(access)

	case "cluster_overview":
		return e.executeOverview(ctx, clusterID, access, argsJSON)

	case "list_pods":
		return e.executePods(ctx, clusterID, access, argsJSON)

	case "list_nodes":
		return e.executeNodes(ctx, clusterID, access, argsJSON)

	case "node_metrics":
		return e.executeNodeMetrics(ctx, clusterID, access, argsJSON)

	case "run_inspection":
		return e.executeInspection(ctx, clusterID, access, argsJSON)

	case "read_pod_logs":
		return e.executeReadPodLogs(ctx, clusterID, access, argsJSON)

	case "pod_evidence":
		return e.executePodEvidence(ctx, clusterID, access, argsJSON)

	case "exec_diagnostic":
		return e.executeExecDiagnostic(ctx, clusterID, access, identity, argsJSON)

	case "propose_remediation":
		targets := e.targetsForTool(clusterID, access, argsJSON)
		if len(targets) != 1 || targets[0].Err != nil {
			return "修复提议必须明确一个已就绪集群,请先说明要修复哪个集群。"
		}
		return e.proposeRemediation(targets[0].ID, targets[0].Runtime.Remediator, identity, argsJSON)

	default:
		return fmt.Sprintf("未知工具: %s", name)
	}
}

func (e *ToolExecutor) listClusters(access ChatAccessScope) string {
	if e.store == nil {
		return "数据库未就绪,无法列出集群。"
	}
	clusters, err := e.store.ListClusters()
	if err != nil {
		return "查询集群失败: " + err.Error()
	}
	allowed := make([]store.Cluster, 0, len(clusters))
	for _, cluster := range clusters {
		if access.AllowsCluster(cluster.ID) {
			allowed = append(allowed, cluster)
		}
	}
	return toJSON(map[string]any{"count": len(allowed), "clusters": allowed})
}

func (e *ToolExecutor) executeOverview(ctx context.Context, requestClusterID uint, access ChatAccessScope, argsJSON string) string {
	targets := e.targetsForTool(requestClusterID, access, argsJSON)
	results := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		item := e.baseClusterResult(target)
		if target.Err == nil && target.Runtime.Collector != nil {
			overview, err := target.Runtime.Collector.Overview(ctx)
			if err != nil {
				item["error"] = err.Error()
			} else {
				item["overview"] = overview
			}
		} else if target.Err == nil {
			item["error"] = "集群客户端未就绪"
		}
		results = append(results, item)
	}
	return toJSON(map[string]any{"count": len(results), "clusters": results})
}

func (e *ToolExecutor) executePods(ctx context.Context, requestClusterID uint, access ChatAccessScope, argsJSON string) string {
	var args struct {
		Namespace string `json:"namespace"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	targets := e.targetsForTool(requestClusterID, access, argsJSON)
	results := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		item := e.baseClusterResult(target)
		if target.Err == nil && target.Runtime.Collector != nil {
			pods, err := target.Runtime.Collector.ListPods(ctx, args.Namespace)
			if err != nil {
				item["error"] = err.Error()
			} else {
				item["count"] = len(pods)
				item["pods"] = pods
			}
		} else if target.Err == nil {
			item["error"] = "集群客户端未就绪"
		}
		results = append(results, item)
	}
	return toJSON(map[string]any{"count": len(results), "clusters": results})
}

func (e *ToolExecutor) executeNodes(ctx context.Context, requestClusterID uint, access ChatAccessScope, argsJSON string) string {
	targets := e.targetsForTool(requestClusterID, access, argsJSON)
	results := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		item := e.baseClusterResult(target)
		if target.Err == nil && target.Runtime.Collector != nil {
			nodes, err := target.Runtime.Collector.ListNodes(ctx)
			if err != nil {
				item["error"] = err.Error()
			} else {
				item["count"] = len(nodes)
				item["nodes"] = nodes
			}
		} else if target.Err == nil {
			item["error"] = "集群客户端未就绪"
		}
		results = append(results, item)
	}
	return toJSON(map[string]any{"count": len(results), "clusters": results})
}

func (e *ToolExecutor) executeNodeMetrics(ctx context.Context, requestClusterID uint, access ChatAccessScope, argsJSON string) string {
	targets := e.targetsForTool(requestClusterID, access, argsJSON)
	results := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		item := e.baseClusterResult(target)
		if target.Err == nil && target.Runtime.Collector != nil {
			metrics, err := target.Runtime.Collector.ListNodeMetrics(ctx)
			if err != nil {
				item["error"] = err.Error()
			} else {
				item["count"] = len(metrics)
				item["metrics"] = metrics
			}
		} else if target.Err == nil {
			item["error"] = "集群客户端未就绪"
		}
		results = append(results, item)
	}
	return toJSON(map[string]any{"count": len(results), "clusters": results})
}

func (e *ToolExecutor) executeInspection(ctx context.Context, requestClusterID uint, access ChatAccessScope, argsJSON string) string {
	targets := e.targetsForTool(requestClusterID, access, argsJSON)
	results := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		item := e.baseClusterResult(target)
		if target.Err != nil {
			results = append(results, item)
			continue
		}
		if e.store != nil {
			if syncResult, err := clustersync.SyncClusterWithSource(ctx, e.store, target.Runtime, store.SyncSourceInspect); err != nil {
				item["sync_error"] = err.Error()
			} else {
				item["sync"] = syncResult
			}
		}
		if target.Runtime.Engine == nil {
			item["error"] = "巡检引擎未就绪"
			results = append(results, item)
			continue
		}
		report, err := target.Runtime.Engine.RunRulesOnly(ctx)
		if err != nil {
			item["error"] = err.Error()
		} else {
			e.attachResourceCacheSummary(report, target.ID)
			item["report"] = report
			if e.store != nil {
				if _, serr := e.store.SaveReport(target.ID, report); serr != nil {
					item["save_error"] = serr.Error()
				}
			}
		}
		results = append(results, item)
	}
	return toJSON(map[string]any{"count": len(results), "clusters": results})
}

func (e *ToolExecutor) attachResourceCacheSummary(report *inspect.Report, clusterID uint) {
	if report == nil || e.store == nil || clusterID == 0 {
		return
	}
	rows, err := e.store.ListClusterSyncResources(clusterID)
	if err != nil {
		return
	}
	report.ResourceCache = store.ResourceCacheSummaryFromSyncResources(rows)
}

func (e *ToolExecutor) baseClusterResult(target clusterTarget) map[string]any {
	item := map[string]any{"cluster_id": target.ID, "cluster_name": target.Name}
	if target.Err != nil {
		item["error"] = target.Err.Error()
	}
	return item
}

func (e *ToolExecutor) targetsForTool(requestClusterID uint, access ChatAccessScope, argsJSON string) []clusterTarget {
	var args toolClusterArgs
	_ = json.Unmarshal([]byte(argsJSON), &args)
	if args.ClusterID != 0 {
		return []clusterTarget{e.targetByIDForAccess(args.ClusterID, access)}
	}
	if strings.TrimSpace(args.ClusterName) != "" {
		return []clusterTarget{e.targetByName(args.ClusterName, access)}
	}
	if requestClusterID != 0 {
		return []clusterTarget{e.targetByIDForAccess(requestClusterID, access)}
	}
	return e.allTargets(access)
}

func (e *ToolExecutor) targetByIDForAccess(clusterID uint, access ChatAccessScope) clusterTarget {
	if !access.AllowsCluster(clusterID) {
		return clusterTarget{ID: clusterID, Err: fmt.Errorf("当前用户无权访问该集群,目标不在授权范围内")}
	}
	return e.targetByID(clusterID)
}

func (e *ToolExecutor) targetByID(clusterID uint) clusterTarget {
	target := clusterTarget{ID: clusterID}
	if e.store != nil {
		if cluster, err := e.store.GetCluster(clusterID); err == nil {
			target.Name = cluster.Name
		}
	}
	runtime, err := e.runtimeForCluster(clusterID)
	if err != nil {
		target.Err = err
		return target
	}
	target.Runtime = runtime
	if target.Name == "" && runtime.Cluster != nil {
		target.Name = runtime.Cluster.Name
	}
	return target
}

func (e *ToolExecutor) targetByName(name string, access ChatAccessScope) clusterTarget {
	if e.store == nil {
		return clusterTarget{Err: fmt.Errorf("数据库未就绪,无法按集群名称查找")}
	}
	needle := strings.TrimSpace(strings.ToLower(name))
	clusters, err := e.store.ListClusters()
	if err != nil {
		return clusterTarget{Err: err}
	}
	for _, cluster := range clusters {
		if !access.AllowsCluster(cluster.ID) {
			continue
		}
		if strings.ToLower(cluster.Name) == needle || strings.ToLower(cluster.Slug) == needle || clusterCode(cluster.ID) == needle {
			return e.targetByID(cluster.ID)
		}
	}
	for _, cluster := range clusters {
		if !access.AllowsCluster(cluster.ID) {
			continue
		}
		if strings.Contains(strings.ToLower(cluster.Name), needle) || strings.Contains(strings.ToLower(cluster.Slug), needle) {
			return e.targetByID(cluster.ID)
		}
	}
	return clusterTarget{Err: fmt.Errorf("未找到名称匹配 %q 的已授权集群", name)}
}

func (e *ToolExecutor) allTargets(access ChatAccessScope) []clusterTarget {
	if e.store != nil {
		clusters, err := e.store.ListClusters()
		if err != nil {
			return []clusterTarget{{Err: err}}
		}
		targets := make([]clusterTarget, 0, len(clusters))
		for _, cluster := range clusters {
			if !access.AllowsCluster(cluster.ID) {
				continue
			}
			target := e.targetByID(cluster.ID)
			if target.Name == "" {
				target.Name = cluster.Name
			}
			targets = append(targets, target)
		}
		return targets
	}
	if e.runtimeManager != nil {
		runtimes := e.runtimeManager.List()
		targets := make([]clusterTarget, 0, len(runtimes))
		for _, runtime := range runtimes {
			if !access.AllowsCluster(runtime.ClusterID) {
				continue
			}
			targets = append(targets, clusterTarget{ID: runtime.ClusterID, Runtime: runtime})
		}
		return targets
	}
	if e.defaultClusterID != 0 && access.AllowsCluster(e.defaultClusterID) {
		return []clusterTarget{e.targetByID(e.defaultClusterID)}
	}
	return []clusterTarget{{Err: fmt.Errorf("暂无已接入集群")}}
}

func clusterCode(id uint) string {
	return fmt.Sprintf("cl-%04d", id)
}

// proposeRemediation 校验修复开关后创建审批工单(不执行)。
func (e *ToolExecutor) proposeRemediation(clusterID uint, remediator *remediate.Executor, identity UserIdentity, argsJSON string) string {
	if remediator == nil || !remediator.Enabled() {
		return "修复功能未启用,无法提议修复操作。"
	}
	if e.store == nil {
		return "数据库未就绪,无法创建审批工单。"
	}
	var a struct {
		Action    string `json:"action"`
		Namespace string `json:"namespace"`
		Target    string `json:"target"`
		Replicas  *int   `json:"replicas"`
		Reason    string `json:"reason"`
		Risk      string `json:"risk"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
		return "参数解析失败: " + err.Error()
	}
	params := "{}"
	if a.Replicas != nil {
		params = fmt.Sprintf(`{"replicas":%d}`, *a.Replicas)
	}
	risk := a.Risk
	if risk == "" {
		risk = "medium"
	}
	reason := a.Reason
	if prefix := aiOperatorLabel(identity); prefix != "" {
		reason = "[" + prefix + "] " + reason
	}
	ticket, err := e.store.CreateTicket(clusterID, &store.ApprovalTicket{
		Action: a.Action, Namespace: a.Namespace, Target: a.Target,
		Params: params, Reason: reason, Risk: risk, Status: store.TicketPending,
	})
	if err != nil {
		return "创建审批工单失败: " + err.Error()
	}
	return fmt.Sprintf("已创建审批工单 #%d(操作:%s,目标:%s/%s)。⚠️ 此操作不会自动执行,需到「审批中心」人工批准后才执行。",
		ticket.ID, a.Action, a.Namespace, a.Target)
}

// singleReadyTarget 解析出唯一已就绪集群,供 AI 现场排查工具使用(读日志/证据/exec 都必须明确单集群)。
func (e *ToolExecutor) singleReadyTarget(clusterID uint, access ChatAccessScope, argsJSON string) (clusterTarget, string) {
	targets := e.targetsForTool(clusterID, access, argsJSON)
	if len(targets) != 1 {
		return clusterTarget{}, "该操作必须明确一个集群,请说明要排查哪个集群。"
	}
	if targets[0].Err != nil {
		return clusterTarget{}, targets[0].Err.Error()
	}
	if targets[0].Runtime == nil || targets[0].Runtime.Collector == nil {
		return clusterTarget{}, "集群客户端未就绪,无法执行该操作。"
	}
	return targets[0], ""
}

// executeReadPodLogs 读取指定 Pod 日志尾部(只读)。
func (e *ToolExecutor) executeReadPodLogs(ctx context.Context, clusterID uint, access ChatAccessScope, argsJSON string) string {
	var a struct {
		Namespace string `json:"namespace"`
		Pod       string `json:"pod"`
		Container string `json:"container"`
		Tail      int64  `json:"tail"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
		return "参数解析失败: " + err.Error()
	}
	if a.Namespace == "" || a.Pod == "" {
		return "read_pod_logs 需要 namespace 与 pod 参数。"
	}
	target, deny := e.singleReadyTarget(clusterID, access, argsJSON)
	if deny != "" {
		return deny
	}
	logs, err := target.Runtime.Collector.TailPodLogs(ctx, a.Namespace, a.Pod, a.Container, a.Tail)
	if err != nil {
		return fmt.Sprintf("读取日志失败(%s/%s): %s", a.Namespace, a.Pod, err.Error())
	}
	return logs
}

// executePodEvidence 采集指定 Pod 的现场证据(容器状态 + 事件 + 崩溃日志)。
func (e *ToolExecutor) executePodEvidence(ctx context.Context, clusterID uint, access ChatAccessScope, argsJSON string) string {
	var a struct {
		Namespace string `json:"namespace"`
		Pod       string `json:"pod"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
		return "参数解析失败: " + err.Error()
	}
	if a.Namespace == "" || a.Pod == "" {
		return "pod_evidence 需要 namespace 与 pod 参数。"
	}
	target, deny := e.singleReadyTarget(clusterID, access, argsJSON)
	if deny != "" {
		return deny
	}
	evidence := target.Runtime.Collector.PodEvidence(ctx, a.Namespace, a.Pod)
	if evidence == "" {
		return fmt.Sprintf("未采集到 %s/%s 的现场证据(Pod 可能不存在或客户端未就绪)。", a.Namespace, a.Pod)
	}
	return evidence
}

// executeExecDiagnostic 在 Pod 容器内执行白名单只读命令并返回输出,同时写审计。
func (e *ToolExecutor) executeExecDiagnostic(ctx context.Context, clusterID uint, access ChatAccessScope, identity UserIdentity, argsJSON string) string {
	var a struct {
		Namespace string   `json:"namespace"`
		Pod       string   `json:"pod"`
		Container string   `json:"container"`
		Command   []string `json:"command"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
		return "参数解析失败: " + err.Error()
	}
	if a.Namespace == "" || a.Pod == "" {
		return "exec_diagnostic 需要 namespace 与 pod 参数。"
	}
	if err := ValidateDiagnosticCommand(a.Command); err != nil {
		return "命令被拒绝: " + err.Error()
	}
	target, deny := e.singleReadyTarget(clusterID, access, argsJSON)
	if deny != "" {
		return deny
	}
	cmdText := strings.Join(a.Command, " ")
	e.auditAIExecStart(target.ID, identity, a.Namespace+"/"+a.Pod, cmdText)

	timeout := e.diagnosticExecTimeout
	if timeout <= 0 {
		timeout = defaultDiagnosticExecTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	execCapture := e.execCapture
	if execCapture == nil {
		execCapture = func(ctx context.Context, col *collector.Collector, namespace, pod, container string, command []string) (string, string, error) {
			return col.ExecCapture(ctx, namespace, pod, container, command)
		}
	}
	stdout, stderr, err := execCapture(execCtx, target.Runtime.Collector, a.Namespace, a.Pod, a.Container, a.Command)
	timedOut := errors.Is(err, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded)

	auditResult := "成功"
	if timedOut {
		auditResult = fmt.Sprintf("超时:超过 %s", timeout)
	} else if err != nil {
		auditResult = "失败: " + err.Error()
	}
	e.auditAIExec(target.ID, identity, a.Namespace+"/"+a.Pod, cmdText, auditResult)

	if timedOut {
		return fmt.Sprintf("命令执行超时(%s/%s $ %s):已超过 %s 的安全上限", a.Namespace, a.Pod, cmdText, timeout)
	}
	if err != nil {
		return fmt.Sprintf("命令执行失败(%s/%s $ %s): %s", a.Namespace, a.Pod, cmdText, err.Error())
	}
	var out strings.Builder
	out.WriteString(fmt.Sprintf("$ %s\n", cmdText))
	if stdout != "" {
		out.WriteString(stdout)
	}
	if stderr != "" {
		out.WriteString("\n[stderr]\n" + stderr)
	}
	if stdout == "" && stderr == "" {
		out.WriteString("(命令无输出)")
	}
	return out.String()
}

// auditAIExecStart 在命令真正发往集群前落一条开始记录,确保卡死、断连等场景仍可追踪。
func (e *ToolExecutor) auditAIExecStart(clusterID uint, identity UserIdentity, target, command string) {
	if e.store == nil {
		return
	}
	_ = e.store.AddAudit(clusterID, &store.AuditLog{
		Action:   "ai:exec_diagnostic:start",
		Operator: aiOperatorLabel(identity),
		Target:   target,
		Detail:   command,
		Result:   "开始",
	})
}

// auditAIExec 记录 AI 发起的 exec 诊断审计,操作者标记为 AI + 触发用户。
func (e *ToolExecutor) auditAIExec(clusterID uint, identity UserIdentity, target, command, result string) {
	if e.store == nil {
		return
	}
	_ = e.store.AddAudit(clusterID, &store.AuditLog{
		Action:   "ai:exec_diagnostic",
		Operator: aiOperatorLabel(identity),
		Target:   target,
		Detail:   command,
		Result:   result,
	})
}

// aiOperatorLabel 生成 "AI(小智)·触发人:<用户>" 的操作者标记。
func aiOperatorLabel(identity UserIdentity) string {
	name := strings.TrimSpace(identity.DisplayName)
	if name == "" {
		name = strings.TrimSpace(identity.Username)
	}
	if name == "" {
		return "AI(小智)"
	}
	return "AI(小智)·触发人:" + name
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("序列化失败: %v", err)
	}
	return string(b)
}
