// Package server 装配 Echo HTTP 服务与路由。
package server

import (
	"context"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"

	"aiops/internal/agent"
	"aiops/internal/ai"
	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/config"
	"aiops/internal/inspect"
	"aiops/internal/notify"
	"aiops/internal/remediate"
	"aiops/internal/store"
)

// Server 封装 Echo 实例与依赖。各依赖可为 nil(对应组件未就绪时)。
type Server struct {
	echo             *echo.Echo
	logger           *zap.Logger
	collector        *collector.Collector
	engine           *inspect.Engine
	store            *store.Store
	agent            *agent.Agent
	remediator       *remediate.Executor
	defaultClusterID uint
	runtimeManager   *clusterruntime.Manager
	runtimeBuilder   clusterRuntimeBuilder
	syncState        syncStatusTracker
	inspectState     syncStatusTracker
	modelMu          sync.RWMutex
	modelClient      *ai.Client
	activeModel      ModelRuntimeStatus
	notifierMu       sync.RWMutex
	notifier         *notify.Dispatcher
	fallbackNotify   config.NotificationConfig // 文件配置兜底(DB 无设置时生效)
	globalPromURL    string                    // 全局 Prometheus 地址(集群未单独配置时回退)
}

type clusterRuntimeBuilder func(context.Context, *store.Cluster, *store.ClusterPolicy, string) (*clusterruntime.Runtime, error)

// NewServer 创建并配置 Echo 服务。
func NewServer(logger *zap.Logger, col *collector.Collector, engine *inspect.Engine, st *store.Store, ag *agent.Agent, rem *remediate.Executor, defaultClusterID uint, runtimeManager *clusterruntime.Manager) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	if runtimeManager == nil {
		runtimeManager = clusterruntime.NewManager()
	}
	if defaultClusterID != 0 && col != nil {
		_ = runtimeManager.Register(&clusterruntime.Runtime{
			ClusterID:  defaultClusterID,
			Collector:  col,
			Engine:     engine,
			Remediator: rem,
		})
	}

	s := &Server{
		echo:             e,
		logger:           logger,
		collector:        col,
		engine:           engine,
		store:            st,
		agent:            ag,
		remediator:       rem,
		defaultClusterID: defaultClusterID,
		runtimeManager:   runtimeManager,
		runtimeBuilder:   defaultClusterRuntimeBuilder,
	}
	s.registerRoutes()
	return s
}

// registerRoutes 注册所有路由。
func (s *Server) registerRoutes() {
	s.echo.GET("/health", s.handleHealth)

	api := s.echo.Group("/api")
	api.GET("/security/status", s.handleSecurityStatus)
	api.POST("/auth/login", s.handleAuthLogin)
	api.POST("/auth/change-password", s.handleAuthChangePassword)
	api.Use(s.authMiddleware)
	api.GET("/auth/me", s.handleAuthMe)
	api.POST("/auth/logout", s.handleAuthLogout)
	// 看板摘要
	api.GET("/dashboard/summary", s.handleDashboardSummary)
	// 集群管理
	api.GET("/clusters", s.handleListClusters)
	api.POST("/clusters", s.handleCreateCluster)
	api.POST("/clusters/sync", s.handleSyncAllClusters)
	api.GET("/clusters/sync/status", s.handleClusterSyncStatus)
	api.GET("/clusters/:id/sync-resources", s.handleListClusterSyncResources)
	api.GET("/clusters/:id/resource-snapshots", s.handleListClusterResourceSnapshots)
	api.GET("/clusters/:id/deleted-resources", s.handleListClusterDeletedResources)
	api.GET("/clusters/:id/sync-tasks", s.handleListClusterSyncTasks)
	api.POST("/clusters/:id/sync", s.handleSyncCluster)
	api.PUT("/clusters/:id", s.handleUpdateCluster)
	api.DELETE("/clusters/:id", s.handleDeleteCluster)
	// Pod 交互:容器列表 / 日志(可跟随)/ 交互式终端(exec)
	api.GET("/clusters/:id/pods/:namespace/:name/containers", s.handleListPodContainers)
	api.GET("/clusters/:id/pods/:namespace/:name/logs", s.handlePodLogs)
	api.GET("/clusters/:id/pods/:namespace/:name/exec", s.handlePodExec)
	// 模型管理
	api.GET("/models/status", s.handleModelStatus)
	api.GET("/models", s.handleListModels)
	api.POST("/models", s.handleCreateModel)
	api.PUT("/models/:id", s.handleUpdateModel)
	api.POST("/models/:id/default", s.handleSetDefaultModel)
	api.POST("/models/:id/test", s.handleTestModel)
	api.DELETE("/models/:id", s.handleDeleteModel)
	// 用户管理
	api.GET("/users", s.handleListUsers)
	api.POST("/users", s.handleCreateUser)
	api.PUT("/users/:id", s.handleUpdateUser)
	api.POST("/users/:id/reset-password", s.handleResetUserPassword)
	api.DELETE("/users/:id", s.handleDeleteUser)
	// 集群只读查询
	api.GET("/cluster/overview", s.handleClusterOverview)
	api.GET("/cluster/pods", s.handleListPods)
	api.GET("/cluster/nodes", s.handleListNodes)
	api.GET("/cluster/node-metrics", s.handleListNodeMetrics)
	// 巡检
	api.POST("/inspect/run", s.handleInspectRun)
	api.GET("/inspect/reports", s.handleListReports)
	api.GET("/inspect/reports/:id", s.handleGetReport)
	// 对话(小智):SSE 流式 + 历史 + 清空
	api.POST("/chat", s.handleChat)
	api.GET("/chat/history", s.handleChatHistory)
	api.POST("/chat/clear", s.handleChatClear)
	// 审批(阶段三):工单列表 + 批准/拒绝 + 审计
	api.GET("/approvals", s.handleListApprovals)
	api.POST("/approvals/:id/approve", s.handleApproveTicket)
	api.POST("/approvals/:id/reject", s.handleRejectTicket)
	api.GET("/audits", s.handleListAudits)
	// 告警中心:列表 + 摘要 + 确认/解决(定时巡检产出的告警)
	api.GET("/alerts", s.handleListAlerts)
	api.GET("/alerts/summary", s.handleAlertSummary)
	api.GET("/alerts/trends", s.handleAlertTrends)
	api.POST("/alerts/:id/acknowledge", s.handleAcknowledgeAlert)
	api.POST("/alerts/:id/resolve", s.handleResolveAlert)
	api.POST("/alerts/:id/diagnose", s.handleAlertDiagnose)
	api.POST("/alerts/:id/remediate", s.handleRemediateAlert)
	// 告警静默:生效列表 + 创建 + 提前解除
	api.GET("/silences", s.handleListSilences)
	api.POST("/silences", s.handleCreateSilence)
	api.DELETE("/silences/:id", s.handleDeleteSilence)
	// 通知设置:查看(脱敏) + 保存(热生效) + 发送测试
	api.GET("/notifications/settings", s.handleGetNotificationSettings)
	api.PUT("/notifications/settings", s.handleSaveNotificationSettings)
	api.POST("/notifications/test", s.handleTestNotification)
	// 巡检规则设置:按集群(cluster_id)三层配置,列表/修改/恢复继承 + 集群独立 Prometheus
	api.GET("/inspection/rules", s.handleListInspectionRules)
	api.PUT("/inspection/rules/:name", s.handleUpdateInspectionRule)
	api.DELETE("/inspection/rules/:name", s.handleResetInspectionRule)
	api.PUT("/inspection/prometheus", s.handleUpdateClusterPrometheus)
	// 自定义指标规则:按集群(cluster_id)增删改查 + PromQL 干跑测试
	api.GET("/inspection/custom-rules", s.handleListCustomRules)
	api.POST("/inspection/custom-rules", s.handleCreateCustomRule)
	api.POST("/inspection/custom-rules/test", s.handleTestCustomRule)
	api.PUT("/inspection/custom-rules/:id", s.handleUpdateCustomRule)
	api.DELETE("/inspection/custom-rules/:id", s.handleDeleteCustomRule)
}

// Echo 返回底层 Echo 实例,供启动与关闭使用。
func (s *Server) Echo() *echo.Echo {
	return s.echo
}

// SetNotifier 注入/热替换外部通知分发器(可为 nil,表示未启用通知)。
func (s *Server) SetNotifier(n *notify.Dispatcher) {
	s.notifierMu.Lock()
	defer s.notifierMu.Unlock()
	s.notifier = n
}

// currentNotifier 读取当前生效的通知分发器(可能为 nil)。
func (s *Server) currentNotifier() *notify.Dispatcher {
	s.notifierMu.RLock()
	defer s.notifierMu.RUnlock()
	return s.notifier
}

// SetGlobalPromURL 设置全局 Prometheus 地址(集群未单独配置时的回退源)。
func (s *Server) SetGlobalPromURL(url string) {
	s.globalPromURL = url
}
