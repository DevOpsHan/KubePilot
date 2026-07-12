package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"aiops/internal/inspect"
	"aiops/internal/store"
)

// 修复效果复检节奏:执行成功后延时首检,之后按间隔复检,命中健康即提前结束。
const (
	remediationVerifyFirstDelay = 25 * time.Second  // 首检延时:给控制器重建/滚动重启留出生效时间
	remediationVerifyInterval   = 25 * time.Second  // 后续复检间隔
	remediationVerifyMaxChecks  = 3                 // 最多复检次数(总 ~75s)
	remediationVerifyBudget     = 110 * time.Second // goroutine 总超时兜底
)

// scheduleRemediationVerification 在修复执行成功后排期一次异步复检。
// 引擎不可用时直接把工单标记为「未复检」并写明原因;否则起分离 goroutine 延时复检。
func (s *Server) scheduleRemediationVerification(clusterID uint, t *store.ApprovalTicket) {
	if s.store == nil || t == nil {
		return
	}
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil || runtime == nil || runtime.Engine == nil {
		s.markVerifySkipped(t, "巡检引擎未就绪,无法自动复检")
		return
	}
	ticketID := t.ID
	executedAt := time.Now()
	// 分离 goroutine:短命、自终止,用带总超时的后台 context,不阻塞请求与优雅关闭
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remediationVerifyBudget)
		defer cancel()
		s.verifyRemediation(ctx, clusterID, ticketID, executedAt)
	}()
}

// verifyRemediation 轮询复检目标资源是否已恢复,并把结论回写工单(必要时自动解决关联告警)。
func (s *Server) verifyRemediation(ctx context.Context, clusterID, ticketID uint, executedAt time.Time) {
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil || runtime == nil || runtime.Engine == nil {
		if t, gErr := s.store.GetTicketByID(ticketID); gErr == nil {
			s.markVerifySkipped(t, "巡检引擎未就绪,无法自动复检")
		}
		return
	}
	t, err := s.store.GetTicketByID(ticketID)
	if err != nil {
		return
	}

	for check := 1; check <= remediationVerifyMaxChecks; check++ {
		delay := remediationVerifyInterval
		if check == 1 {
			delay = remediationVerifyFirstDelay
		}
		select {
		case <-ctx.Done():
			return // 总预算耗尽:保持 pending,交由定时巡检兜底
		case <-time.After(delay):
		}

		report, runErr := runtime.Engine.RunRulesOnly(ctx)
		if runErr != nil {
			if s.logger != nil {
				s.logger.Warn("修复复检巡检失败,稍后重试",
					zap.Uint("ticket_id", ticketID), zap.Error(runErr))
			}
			continue // 巡检暂时失败不下结论,下一轮再试
		}

		remaining := concerningAnomaly(report.Anomalies, t)
		if remaining == nil {
			s.finishVerifyHealthy(t, executedAt)
			return
		}
		if check == remediationVerifyMaxChecks {
			s.finishVerifyFailing(t, remaining)
			return
		}
	}
}

// finishVerifyHealthy 复检通过:回写工单已修复,并把仍未恢复的关联告警自动解决。
func (s *Server) finishVerifyHealthy(t *store.ApprovalTicket, executedAt time.Time) {
	now := time.Now()
	t.VerifyState = store.VerifyHealthy
	t.VerifiedAt = &now
	t.VerifyDetail = fmt.Sprintf("目标已恢复,修复耗时 %s", now.Sub(executedAt).Round(time.Second))
	_ = s.store.UpdateTicket(t)

	// 见效则自动解决关联告警(仅当其仍未恢复,避免覆盖人工/系统状态)
	if t.AlertID != nil {
		if a, err := s.store.GetAlertByID(*t.AlertID); err == nil && a != nil && a.Status != store.AlertStatusResolved {
			a.Status = store.AlertStatusResolved
			a.ResolvedBy = "AIOps 自动修复"
			a.ResolvedAt = &now
			_ = s.store.UpdateAlert(a)
		}
	}
	_ = s.store.AddAudit(ticketClusterIDOrZero(t), &store.AuditLog{
		Action:   "verify_remediation",
		Operator: "AIOps 自动修复",
		Target:   t.Namespace + "/" + t.Target,
		Detail:   t.VerifyDetail,
		Result:   "修复已生效",
	})
}

// finishVerifyFailing 复检未通过:回写工单未解决,留待人工介入或下一轮巡检。
func (s *Server) finishVerifyFailing(t *store.ApprovalTicket, remaining *inspect.Anomaly) {
	now := time.Now()
	t.VerifyState = store.VerifyFailing
	t.VerifiedAt = &now
	t.VerifyDetail = fmt.Sprintf("目标仍存在异常: %s/%s 命中 %s", remaining.Namespace, remaining.Name, remaining.Rule)
	_ = s.store.UpdateTicket(t)
	_ = s.store.AddAudit(ticketClusterIDOrZero(t), &store.AuditLog{
		Action:   "verify_remediation",
		Operator: "AIOps 自动修复",
		Target:   t.Namespace + "/" + t.Target,
		Detail:   t.VerifyDetail,
		Result:   "修复未生效,建议人工介入",
	})
}

// markVerifySkipped 标记工单无法自动复检。
func (s *Server) markVerifySkipped(t *store.ApprovalTicket, detail string) {
	now := time.Now()
	t.VerifyState = store.VerifySkipped
	t.VerifiedAt = &now
	t.VerifyDetail = detail
	_ = s.store.UpdateTicket(t)
}

// concerningAnomaly 返回异常列表中第一条仍关系到工单目标的异常;无则返回 nil。
func concerningAnomaly(anomalies []inspect.Anomaly, t *store.ApprovalTicket) *inspect.Anomaly {
	for i := range anomalies {
		if anomalyConcernsTarget(anomalies[i], t) {
			return &anomalies[i]
		}
	}
	return nil
}

// anomalyConcernsTarget 判断一条异常是否仍关系到工单的修复目标。
// 命名空间需一致;资源名与目标相等(裸 Pod / 精确对象),
// 或以「目标名-」为前缀(Deployment 目标匹配其管理的 Pod:<deploy>-<rs 哈希>-<pod 哈希>)。
func anomalyConcernsTarget(a inspect.Anomaly, t *store.ApprovalTicket) bool {
	if t == nil || t.Target == "" {
		return false
	}
	if a.Namespace != t.Namespace {
		return false
	}
	return a.Name == t.Target || strings.HasPrefix(a.Name, t.Target+"-")
}

// ticketClusterIDOrZero 取工单集群 ID,缺失返回 0(审计允许 0 集群)。
func ticketClusterIDOrZero(t *store.ApprovalTicket) uint {
	if t == nil || t.ClusterID == nil {
		return 0
	}
	return *t.ClusterID
}
