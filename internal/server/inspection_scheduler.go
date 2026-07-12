package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"aiops/internal/notify"
	"aiops/internal/store"
)

// inspectMu 保证同一时刻只有一轮定时巡检在跑(与集群同步的 syncMu 独立,互不阻塞)。
var inspectMu sync.Mutex

// StartInspectionScheduler 启动后台定时巡检调度器:按周期对所有已接入集群跑一次规则巡检
// (只跑规则、不调用大模型),把异常对账为告警。调用方需传入服务生命周期 context 以支持优雅退出。
func (s *Server) StartInspectionScheduler(ctx context.Context, interval, initialDelay time.Duration) {
	if interval <= 0 {
		return
	}
	if initialDelay < 0 {
		initialDelay = 0
	}
	if s.logger != nil {
		s.logger.Info("定时巡检调度器已启用",
			zap.Duration("interval", interval),
			zap.Duration("initial_delay", initialDelay),
		)
	}
	s.inspectState.setNextRun(time.Now().Add(initialDelay))
	go func() {
		timer := time.NewTimer(initialDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.runScheduledInspection(ctx)
		}
		s.inspectState.setNextRun(time.Now().Add(interval))

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runScheduledInspection(ctx)
				s.inspectState.setNextRun(time.Now().Add(interval))
			}
		}
	}()
}

func (s *Server) runScheduledInspection(ctx context.Context) {
	if !inspectMu.TryLock() {
		if s.logger != nil {
			s.logger.Info("定时巡检跳过:已有巡检任务正在运行")
		}
		return
	}
	defer inspectMu.Unlock()
	s.inspectState.begin("scheduled")
	inspected, err := s.inspectAllClusters(ctx)
	s.inspectState.finish(err)
	if s.logger == nil {
		return
	}
	if err != nil {
		s.logger.Warn("定时巡检失败", zap.Error(err))
		return
	}
	s.logger.Info("定时巡检完成", zap.Int("clusters", inspected))
}

// inspectAllClusters 对所有已启用集群跑规则巡检并对账告警,返回成功巡检的集群数。
// 单个集群巡检失败只跳过该集群(不动其告警,避免误判恢复),不影响其它集群。
func (s *Server) inspectAllClusters(ctx context.Context) (int, error) {
	if s.store == nil {
		return 0, fmt.Errorf("数据库未就绪")
	}
	clusters, err := s.store.ListClusters()
	if err != nil {
		return 0, err
	}
	inspected := 0
	for _, cluster := range clusters {
		if !cluster.Enabled {
			continue
		}
		runtime, rErr := s.runtimeForClusterID(cluster.ID)
		if rErr != nil || runtime == nil || runtime.Engine == nil {
			continue // 引擎未就绪(集群客户端不可用)→ 跳过,不动该集群告警
		}
		report, runErr := runtime.Engine.RunRulesOnly(ctx)
		if runErr != nil {
			if s.logger != nil {
				s.logger.Warn("集群巡检失败,跳过告警对账",
					zap.Uint("cluster_id", cluster.ID), zap.Error(runErr))
			}
			continue
		}
		created, resolvedAlerts, syncErr := s.store.SyncAlertsForCluster(cluster.ID, report.Anomalies)
		if syncErr != nil {
			if s.logger != nil {
				s.logger.Warn("告警对账失败",
					zap.Uint("cluster_id", cluster.ID), zap.Error(syncErr))
			}
			continue
		}
		// 事务外异步推送外部通知,不阻塞巡检节奏
		go s.dispatchAlertNotifications(cluster.Name, created, resolvedAlerts)
		inspected++
	}
	return inspected, nil
}

// dispatchAlertNotifications 把本轮新增/恢复的告警按配置推送到外部渠道。
// 由巡检循环以独立 goroutine 调用,不阻塞巡检节奏;单条失败只记录日志。
// 命中生效中静默规则的告警(新增与恢复)一律不推送。
func (s *Server) dispatchAlertNotifications(clusterName string, created, resolved []store.Alert) {
	notifier := s.currentNotifier()
	if notifier == nil || !notifier.Enabled() {
		return
	}
	var silences []store.AlertSilence
	if s.store != nil {
		if list, err := s.store.ListActiveSilences(nil); err == nil {
			silences = list
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i := range created {
		a := created[i]
		if !notifier.AllowSeverity(a.Severity) || store.MatchSilence(silences, a) {
			continue
		}
		notifier.Dispatch(ctx, notify.Message{
			Title: "🔴 新增告警 · " + a.Rule,
			Text:  buildAlertMarkdown("🔴 AIOps 新增告警", clusterName, a),
		})
	}
	if notifier.NotifyResolved() {
		for i := range resolved {
			a := resolved[i]
			if store.MatchSilence(silences, a) {
				continue
			}
			notifier.Dispatch(ctx, notify.Message{
				Title: "✅ 告警恢复 · " + a.Rule,
				Text:  buildAlertMarkdown("✅ AIOps 告警恢复", clusterName, a),
			})
		}
	}
}

// buildAlertMarkdown 组织钉钉/企微通知的 markdown 正文。
func buildAlertMarkdown(header, clusterName string, a store.Alert) string {
	sevText := map[string]string{"critical": "严重", "warning": "警告"}[a.Severity]
	if sevText == "" {
		sevText = a.Severity
	}
	target := a.Name
	if a.Namespace != "" {
		target = a.Namespace + "/" + a.Name
	}
	if clusterName == "" {
		clusterName = "-"
	}
	return fmt.Sprintf("### %s\n\n- **集群**:%s\n- **级别**:%s\n- **规则**:%s\n- **对象**:%s\n- **详情**:%s\n- **时间**:%s",
		header, clusterName, sevText, a.Rule, target, a.Message, time.Now().Format("2006-01-02 15:04:05"))
}
