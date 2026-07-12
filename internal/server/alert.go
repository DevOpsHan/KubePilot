package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"aiops/internal/inspect"
	"aiops/internal/store"
)

type alertResponse struct {
	store.Alert
	ClusterName string `json:"cluster_name"`
	Silenced    bool   `json:"silenced"` // 命中生效中的静默规则
}

// handleListAlerts 返回告警列表。?status= / ?severity= / ?cluster_id= 可过滤。
func (s *Server) handleListAlerts(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionViewAlerts); err != nil {
		return err
	}
	status := c.QueryParam("status")
	severity := c.QueryParam("severity")
	clusterID, ok, err := s.optionalClusterIDFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	var alerts []store.Alert
	if ok {
		if err := s.requireClusterPermission(c, store.PermissionViewAlerts, clusterID); err != nil {
			return err
		}
		alerts, err = s.store.ListAlerts(clusterID, status, severity, 300)
	} else {
		alerts, err = s.store.ListAllAlerts(status, severity, 300)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	filtered, err := s.filterAlertsForCurrentUser(c, alerts)
	if err != nil {
		return err
	}
	responses, err := s.alertResponses(filtered)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(responses), "alerts": responses})
}

// handleAlertSummary 返回告警计数摘要(按状态与严重级别),供告警中心页头与侧边栏角标复用。
func (s *Server) handleAlertSummary(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionViewAlerts); err != nil {
		return err
	}
	alerts, err := s.store.ListAllAlerts("", "", 2000)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	filtered, err := s.filterAlertsForCurrentUser(c, alerts)
	if err != nil {
		return err
	}
	silences, silenceErr := s.store.ListActiveSilences(nil)
	if silenceErr != nil {
		silences = nil
	}
	var active, acknowledged, resolved, critical, warning, silenced int
	for _, a := range filtered {
		// 静默中的未恢复告警单独计数,不进入活跃/严重/警告(避免骚扰角标)
		if a.Status != store.AlertStatusResolved && store.MatchSilence(silences, a) {
			silenced++
			continue
		}
		switch a.Status {
		case store.AlertStatusActive:
			active++
		case store.AlertStatusAcknowledged:
			acknowledged++
		case store.AlertStatusResolved:
			resolved++
		}
		if a.Status != store.AlertStatusResolved {
			switch a.Severity {
			case "critical":
				critical++
			case "warning":
				warning++
			}
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"active":       active,
		"acknowledged": acknowledged,
		"resolved":     resolved,
		"critical":     critical,
		"warning":      warning,
		"silenced":     silenced,
		"firing":       active + acknowledged, // 触发中 = 活跃 + 已确认(不含静默)
	})
}

// handleAcknowledgeAlert 确认告警(仍触发,但已知悉)。
func (s *Server) handleAcknowledgeAlert(c echo.Context) error {
	return s.updateAlertStatus(c, store.AlertStatusAcknowledged)
}

// handleResolveAlert 人工解决告警。
func (s *Server) handleResolveAlert(c echo.Context) error {
	return s.updateAlertStatus(c, store.AlertStatusResolved)
}

func (s *Server) updateAlertStatus(c echo.Context, target string) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的告警 ID"})
	}
	requestClusterID, hasRequestClusterID, err := s.optionalClusterIDFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	alert, err := s.store.GetAlertByID(uint(id))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "告警不存在"})
	}
	alertClusterID, err := requiredAlertClusterID(alert)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if hasRequestClusterID && requestClusterID != alertClusterID {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求集群与告警所属集群不一致"})
	}
	if err := s.requireClusterPermission(c, store.PermissionManageAlerts, alertClusterID); err != nil {
		return err
	}
	if alert.Status == store.AlertStatusResolved {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "告警已解决,无需再操作"})
	}
	if target == store.AlertStatusAcknowledged && alert.Status == store.AlertStatusAcknowledged {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "告警已确认"})
	}

	operator := currentUsername(c)
	now := time.Now()
	auditAction, auditResult := "acknowledge_alert", "已确认"
	if target == store.AlertStatusResolved {
		alert.Status = store.AlertStatusResolved
		alert.ResolvedBy = operator
		alert.ResolvedAt = &now
		auditAction, auditResult = "resolve_alert", "已解决"
	} else {
		alert.Status = store.AlertStatusAcknowledged
		alert.AcknowledgedBy = operator
		alert.AcknowledgedAt = &now
	}
	if err := s.store.UpdateAlert(alert); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	_ = s.store.AddAudit(alertClusterID, &store.AuditLog{
		Action:   auditAction,
		Operator: operator,
		Target:   alert.Namespace + "/" + alert.Name,
		Detail:   alert.Rule + " · " + alert.Message,
		Result:   auditResult,
	})
	return c.JSON(http.StatusOK, map[string]any{"alert": alert})
}

func requiredAlertClusterID(a *store.Alert) (uint, error) {
	if a == nil || a.ClusterID == nil || *a.ClusterID == 0 {
		return 0, errors.New("告警缺少集群归属,禁止操作")
	}
	return *a.ClusterID, nil
}

func (s *Server) filterAlertsForCurrentUser(c echo.Context, alerts []store.Alert) ([]store.Alert, error) {
	allowed, all, err := s.authorizedClusterIDSet(c)
	if err != nil {
		return nil, err
	}
	if all {
		return alerts, nil
	}
	filtered := make([]store.Alert, 0, len(alerts))
	for _, alert := range alerts {
		if alert.ClusterID == nil {
			continue
		}
		if _, ok := allowed[*alert.ClusterID]; ok {
			filtered = append(filtered, alert)
		}
	}
	return filtered, nil
}

func (s *Server) alertResponses(alerts []store.Alert) ([]alertResponse, error) {
	clusters, err := s.store.ListClusters()
	if err != nil {
		return nil, err
	}
	clusterNames := make(map[uint]string, len(clusters))
	for _, cluster := range clusters {
		clusterNames[cluster.ID] = cluster.Name
	}
	// 静默匹配标记;查询失败按无静默处理,不阻断列表
	silences, silenceErr := s.store.ListActiveSilences(nil)
	if silenceErr != nil {
		silences = nil
	}
	responses := make([]alertResponse, 0, len(alerts))
	for _, alert := range alerts {
		item := alertResponse{Alert: alert, Silenced: store.MatchSilence(silences, alert)}
		if alert.ClusterID != nil {
			item.ClusterName = clusterNames[*alert.ClusterID]
		}
		responses = append(responses, item)
	}
	return responses, nil
}

// remediationForRule 把告警规则映射到默认修复动作。ok=false 表示该规则仅支持诊断、不建议自动修复。
func remediationForRule(rule string) (action, params, risk string, ok bool) {
	switch rule {
	case "PodCrashLoopBackOff":
		return "restart_pod", "{}", "medium", true
	case "PodHighRestarts":
		return "restart_pod", "{}", "low", true
	case "PodNotReady":
		return "restart_pod", "{}", "low", true
	case "PodFailed":
		return "delete_pod", "{}", "medium", true
	default:
		return "", "", "", false
	}
}

// suggestedActionWhitelist AI 诊断允许输出的建议动作白名单。none 表示"不宜自动修复"。
// 其余(如 scale_deployment)一律丢弃,防止模型臆造出危险或未实现的动作。
var suggestedActionWhitelist = map[string]bool{
	"restart_pod":        true,
	"restart_deployment": true,
	"delete_pod":         true,
	"none":               true,
}

// parseSuggestedAction 从诊断文本中解析并剥离「建议动作」行。
// 返回剥离该行后的诊断正文,以及命中白名单的动作(未命中或无该行时为 "")。
func parseSuggestedAction(text string) (clean, action string) {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		// 归一化:去 markdown 强调符、统一全角冒号,便于稳健匹配
		probe := strings.ReplaceAll(strings.TrimSpace(line), "*", "")
		probe = strings.ReplaceAll(probe, "：", ":")
		if strings.HasPrefix(strings.TrimSpace(probe), "建议动作") {
			rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(probe), "建议动作"))
			rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
			// 只取第一个词,忽略尾随说明(如 "restart_pod (删除后重建)")
			if fields := strings.Fields(rest); len(fields) > 0 && suggestedActionWhitelist[fields[0]] {
				action = fields[0]
			}
			continue // 无论是否命中白名单,都从正文中剥离该指令行
		}
		kept = append(kept, line)
	}
	clean = strings.TrimRight(strings.Join(kept, "\n"), "\n \t")
	return clean, action
}

// remediationActionMeta 把 AI 建议的可执行动作映射到工单参数与风险等级。
func remediationActionMeta(action string) (params, risk string, ok bool) {
	switch action {
	case "restart_pod", "restart_deployment", "delete_pod":
		return "{}", "medium", true
	default:
		return "", "", false
	}
}

// resolveRemediationAction 决定一条告警的修复动作,优先级:AI 建议动作 > 规则默认映射。
// bySuggestion=true 表示动作来自 AI 诊断建议;ok=false 表示无可自动修复的动作(仅支持诊断)。
func (s *Server) resolveRemediationAction(a *store.Alert) (action, params, risk string, bySuggestion bool, ok bool) {
	if a != nil && a.SuggestedAction != "" && a.SuggestedAction != "none" {
		if p, r, meta := remediationActionMeta(a.SuggestedAction); meta {
			return a.SuggestedAction, p, r, true, true
		}
	}
	action, params, risk, ok = remediationForRule(func() string {
		if a == nil {
			return ""
		}
		return a.Rule
	}())
	return action, params, risk, false, ok
}

// handleAlertDiagnose 对一条告警按需做一次 AI 诊断,并把诊断文本写回该告警。
func (s *Server) handleAlertDiagnose(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的告警 ID"})
	}
	alert, err := s.store.GetAlertByID(uint(id))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "告警不存在"})
	}
	clusterID, err := requiredAlertClusterID(alert)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionViewAlerts, clusterID); err != nil {
		return err
	}
	diagnoser := s.currentDiagnoser()
	if diagnoser == nil {
		msg := "AI 模型未配置或未就绪,请先在「智能助手」中启用模型"
		if status := s.CurrentModelRuntimeStatus(); status.LastError != "" {
			msg = msg + ": " + status.LastError
		}
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": msg})
	}
	anomaly := inspect.Anomaly{
		Severity:  inspect.Severity(alert.Severity),
		Kind:      alert.Kind,
		Namespace: alert.Namespace,
		Name:      alert.Name,
		Rule:      alert.Rule,
		Reason:    alert.Reason,
		Message:   alert.Message,
	}
	// 采集现场证据(容器状态/事件/崩溃日志)喂给 AI,定位具体根因;采集失败不阻断诊断
	if runtime, rErr := s.runtimeForClusterID(clusterID); rErr == nil && runtime.Collector != nil {
		ectx, ecancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
		switch alert.Kind {
		case "Pod":
			anomaly.Evidence = runtime.Collector.PodEvidence(ectx, alert.Namespace, alert.Name)
		case "Node":
			anomaly.Evidence = runtime.Collector.NodeEvidence(ectx, alert.Name)
		}
		ecancel()
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
	defer cancel()
	diagnosis, err := diagnoser.Diagnose(ctx, &anomaly)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "AI 诊断失败: " + err.Error()})
	}
	// 解析并剥离诊断文末的「建议动作」指令行:正文入库供人阅读,动作入库供一键修复联动
	clean, action := parseSuggestedAction(diagnosis)
	now := time.Now()
	alert.Diagnosis = clean
	alert.SuggestedAction = action
	alert.DiagnosedAt = &now
	if err := s.store.UpdateAlert(alert); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	_ = s.store.AddAudit(clusterID, &store.AuditLog{
		Action:   "diagnose_alert",
		Operator: currentUsername(c),
		Target:   alert.Namespace + "/" + alert.Name,
		Detail:   alert.Rule,
		Result:   "已生成 AI 诊断",
	})
	return c.JSON(http.StatusOK, map[string]any{"alert": alert})
}

// handleRemediateAlert 由一条告警一键生成修复审批工单,进入现有审批→执行→审计链路。
func (s *Server) handleRemediateAlert(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的告警 ID"})
	}
	alert, err := s.store.GetAlertByID(uint(id))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "告警不存在"})
	}
	clusterID, err := requiredAlertClusterID(alert)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionApproveRemediation, clusterID); err != nil {
		return err
	}
	if alert.Status == store.AlertStatusResolved {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "告警已恢复,无需修复"})
	}
	// 智能联动:优先采用 AI 诊断给出的建议动作,回退到规则默认映射
	action, params, risk, bySuggestion, ok := s.resolveRemediationAction(alert)
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "该告警规则没有可自动修复的动作,仅支持 AI 诊断"})
	}
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	if runtime.Remediator == nil || !runtime.Remediator.Enabled() {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "该集群修复功能未启用"})
	}
	// 智能升级:restart_pod 若能反查到所属 Deployment,则升级为整个 Deployment 滚动重启——
	// 删单个 Pod 治标不治本(控制器会按同样的坏模板重建)。裸 Pod / 其它控制器 / 反查失败则回退单 Pod。
	target := alert.Name
	if action == "restart_pod" && alert.Kind == "Pod" {
		wlCtx, cancel := context.WithTimeout(c.Request().Context(), 8*time.Second)
		wlKind, wlName, wlErr := runtime.Remediator.ResolveWorkload(wlCtx, alert.Namespace, alert.Name)
		cancel()
		if wlErr != nil {
			if s.logger != nil {
				s.logger.Warn("反查 Pod 所属工作负载失败,回退单 Pod 修复",
					zap.String("pod", alert.Namespace+"/"+alert.Name), zap.Error(wlErr))
			}
		} else if wlKind == "Deployment" && wlName != "" {
			action, target, risk = "restart_deployment", wlName, "medium"
		}
	}
	origin := "一键生成"
	if bySuggestion {
		origin = "一键生成(采纳 AI 诊断建议动作)"
	}
	reason := fmt.Sprintf("由告警 #%d(%s)%s: %s", alert.ID, alert.Rule, origin, alert.Message)
	if action == "restart_deployment" && target != alert.Name {
		reason = fmt.Sprintf("由告警 #%d(%s)%s: 滚动重启 Pod %s/%s 所属 Deployment %s",
			alert.ID, alert.Rule, origin, alert.Namespace, alert.Name, target)
	}
	alertID := alert.ID
	ticket, err := s.store.CreateTicket(clusterID, &store.ApprovalTicket{
		AlertID:   &alertID, // 回链源告警,供修复复检通过后自动解决
		Action:    action,
		Namespace: alert.Namespace,
		Target:    target,
		Params:    params,
		Reason:    reason,
		Risk:      risk,
		Status:    store.TicketPending,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	ticketID := ticket.ID
	alert.RemediationTicketID = &ticketID
	_ = s.store.UpdateAlert(alert)
	_ = s.store.AddAudit(clusterID, &store.AuditLog{
		Action:   "remediate_from_alert",
		Operator: currentUsername(c),
		Target:   alert.Namespace + "/" + alert.Name,
		Detail:   reason,
		Result:   fmt.Sprintf("已生成修复工单 #%d(%s)", ticket.ID, action),
	})
	return c.JSON(http.StatusOK, map[string]any{"ticket": ticket, "alert": alert})
}

// handleAlertTrends 返回近 N 天(默认 7,上限 30)按天分桶的告警趋势,供告警中心趋势图使用。
func (s *Server) handleAlertTrends(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionViewAlerts); err != nil {
		return err
	}
	days := 7
	if raw := strings.TrimSpace(c.QueryParam("days")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			days = v
		}
	}
	if days < 1 {
		days = 1
	}
	if days > 30 {
		days = 30
	}
	clusterID, ok, err := s.optionalClusterIDFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	var clusterIDs []uint
	if ok {
		if err := s.requireClusterPermission(c, store.PermissionViewAlerts, clusterID); err != nil {
			return err
		}
		clusterIDs = []uint{clusterID}
	} else {
		allowed, all, err := s.authorizedClusterIDSet(c)
		if err != nil {
			return err
		}
		if !all {
			clusterIDs = make([]uint, 0, len(allowed))
			for id := range allowed {
				clusterIDs = append(clusterIDs, id)
			}
			if len(clusterIDs) == 0 {
				clusterIDs = []uint{0} // 无授权集群 → 限定到不存在集群,返回全 0 的日期骨架
			}
		}
	}
	buckets, err := s.store.AlertTrends(clusterIDs, days)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"days": days, "buckets": buckets})
}
