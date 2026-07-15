// Package app 负责组装应用各组件。
package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"aiops/internal/agent"
	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/config"
	"aiops/internal/inspect"
	"aiops/internal/remediate"
	"aiops/internal/security"
	"aiops/internal/server"
	"aiops/internal/session"
	"aiops/internal/store"
)

// App 持有应用运行所需的核心组件。
type App struct {
	Config     *config.Config
	Logger     *zap.Logger
	Server     *server.Server
	Collector  *collector.Collector
	Engine     *inspect.Engine
	Store      *store.Store
	Agent      *agent.Agent
	Remediator *remediate.Executor
	Default    *store.Cluster
	Runtimes   *clusterruntime.Manager
}

// New 组装应用:存储 + Redis会话 + K8S采集 + 巡检 + 修复执行器 + 对话Agent + HTTP服务。
func New(cfg *config.Config, logger *zap.Logger) (*App, error) {
	// 数据库
	var st *store.Store
	if db, err := store.Open(cfg.Database.Driver, cfg.Database.DSN); err != nil {
		logger.Warn("数据库初始化失败,巡检/审批将不可用", zap.Error(err))
	} else {
		logger.Info("数据库就绪", zap.String("driver", cfg.Database.Driver))
		st = db
	}

	// 会话记忆:优先 Redis,失败降级内存
	var sessStore session.Store
	if rs, err := session.NewRedisStore(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB); err != nil {
		logger.Warn("Redis 连接失败,会话记忆降级为内存", zap.Error(err))
		sessStore = session.NewMemoryStore()
	} else {
		logger.Info("Redis 会话存储就绪", zap.String("addr", cfg.Redis.Addr))
		sessStore = rs
	}

	// K8S 客户端 + 巡检引擎 + 修复执行器
	clientset, metricsClient, dynamicClient, restConfig, source, err := collector.NewClientsetsWithConfig(cfg.Kubernetes.Kubeconfig)
	var col *collector.Collector
	var engine *inspect.Engine
	var remediator *remediate.Executor
	if err != nil {
		logger.Warn("K8S 客户端初始化失败,集群采集/巡检/修复暂不可用", zap.Error(err))
	} else {
		logger.Info("K8S 客户端就绪", zap.String("source", source))
		col = collector.NewWithMetricsDynamicAndConfig(clientset, metricsClient, dynamicClient, restConfig)
		engine = inspect.NewEngine(col, nil)
		remediator = remediate.New(clientset, cfg.Remediation.Enabled)
		logger.Info("修复执行器就绪", zap.Bool("enabled", cfg.Remediation.Enabled))
	}

	var defaultCluster *store.Cluster
	if st != nil {
		if n, err := st.PurgeDeletedUsers(store.UserDeletedRetention, time.Now()); err != nil {
			logger.Warn("清理已过期删除用户失败", zap.Error(err))
		} else if n > 0 {
			logger.Info("已清理超过保留期的删除用户", zap.Int64("count", n))
		}
		if err := ensureInitialAdmin(st, logger); err != nil {
			return nil, err
		}
		// 仅在显式配置了 kubeconfig,或运行于集群内(in-cluster)时才创建"默认集群";
		// 否则(如本地自动探测 ~/.kube/config)不自动生成,避免在集群列表里凭空多出一个集群。
		if strings.TrimSpace(cfg.Kubernetes.Kubeconfig) != "" || source == "in-cluster" {
			defaultCluster, err = ensureDefaultCluster(st, cfg, source)
			if err != nil {
				return nil, err
			}
			logger.Info("默认集群记录就绪",
				zap.Uint("cluster_id", defaultCluster.ID),
				zap.String("mode", defaultCluster.Mode),
				zap.String("slug", defaultCluster.Slug),
			)
		} else {
			logger.Info("未显式配置 kubeconfig 且非 in-cluster,跳过默认集群自动创建(仅纳管手动导入的集群)")
		}
	}
	defaultClusterID := uint(0)
	if defaultCluster != nil {
		defaultClusterID = defaultCluster.ID
	}
	runtimeManager := clusterruntime.NewManager()
	if defaultClusterID != 0 && col != nil {
		engine.SetClusterID(defaultClusterID) // 绑定集群,读取该集群的规则覆盖配置
		if err := runtimeManager.Register(&clusterruntime.Runtime{
			ClusterID:  defaultClusterID,
			Cluster:    defaultCluster,
			Collector:  col,
			Engine:     engine,
			Remediator: remediator,
		}); err != nil {
			return nil, err
		}
	}
	if st != nil {
		restoreStoredClusterRuntimes(st, runtimeManager, defaultClusterID, logger)
	}

	// 对话 Agent(小智)
	var agentInst *agent.Agent
	if col != nil && engine != nil {
		executor := agent.NewToolExecutor(col, engine, st, remediator, defaultClusterID, runtimeManager)
		agentInst = agent.New(nil, executor, sessStore)
		logger.Info("对话助手已初始化,等待加载页面模型配置")
	} else {
		logger.Warn("对话助手未启用(需集群访问就绪)")
	}

	httpServer := server.NewServer(logger, col, engine, st, agentInst, remediator, defaultClusterID, runtimeManager)
	// 通知配置:数据库设置优先(页面可改、热生效),文件配置作为兜底
	httpServer.ConfigureNotifications(cfg.Notification)
	// 巡检规则配置:每轮巡检按集群从数据库读取(三层 merge,页面修改即热生效)
	inspect.SetRuleConfigProvider(httpServer.EffectiveRuleConfig)
	// 自定义指标规则:每轮巡检按集群读取(全局+该集群、仅启用),页面增删改即热生效
	inspect.SetCustomRuleProvider(httpServer.EffectiveCustomRules)
	// Prometheus 接入(可选):全局地址作为兜底,集群可在巡检设置页单独配置(存 policy)
	globalPromURL := strings.TrimSpace(cfg.Prometheus.URL)
	httpServer.SetGlobalPromURL(globalPromURL)
	httpServer.InitClusterPromQueriers() // 按各集群生效地址初始化引擎查询器
	if globalPromURL != "" {
		logger.Info("全局 Prometheus 已配置,指标类巡检规则可用", zap.String("url", globalPromURL))
	}
	if err := httpServer.SyncDefaultModelFromStore(); err != nil {
		logger.Warn("加载数据库默认模型失败", zap.Error(err))
	} else {
		status := httpServer.CurrentModelRuntimeStatus()
		if status.Ready {
			logger.Info("数据库默认模型已生效",
				zap.Uint("profile_id", status.ProfileID),
				zap.String("provider", status.Provider),
				zap.String("model", status.ModelName),
			)
		} else {
			logger.Warn("暂无可用模型配置,请在智能助手中配置模型")
		}
	}
	return &App{
		Config:     cfg,
		Logger:     logger,
		Server:     httpServer,
		Collector:  col,
		Engine:     engine,
		Store:      st,
		Agent:      agentInst,
		Remediator: remediator,
		Default:    defaultCluster,
		Runtimes:   runtimeManager,
	}, nil
}

func ensureInitialAdmin(st *store.Store, logger *zap.Logger) error {
	count, err := st.CountUsers()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	username := strings.TrimSpace(os.Getenv("AIOPS_ADMIN_USERNAME"))
	if username == "" {
		username = "admin"
	}
	displayName := strings.TrimSpace(os.Getenv("AIOPS_ADMIN_DISPLAY_NAME"))
	if displayName == "" {
		displayName = "系统管理员"
	}
	if _, err := st.CreateUser(store.CreateUserInput{
		Username:    username,
		DisplayName: displayName,
		Password:    store.DefaultInitialUserPassword,
		Role:        store.UserRoleGlobalAdmin,
		Status:      store.UserStatusActive,
	}); err != nil {
		return err
	}
	logger.Warn("已创建初始管理员,请首次登录后立即修改密码",
		zap.String("username", username),
		zap.String("temporary_password", store.DefaultInitialUserPassword),
	)
	return nil
}

func restoreStoredClusterRuntimes(st *store.Store, runtimeManager *clusterruntime.Manager, defaultClusterID uint, logger *zap.Logger) {
	clusters, err := st.ListClusters()
	if err != nil {
		logger.Warn("读取已接入集群失败", zap.Error(err))
		return
	}
	for i := range clusters {
		cluster := clusters[i]
		if cluster.ID == defaultClusterID {
			continue
		}
		if !cluster.Enabled {
			logger.Info("跳过禁用集群运行时恢复", zap.Uint("cluster_id", cluster.ID), zap.String("cluster", cluster.Name))
			continue
		}
		runtime, err := buildStoredClusterRuntime(st, &cluster)
		if err != nil {
			logger.Warn("恢复集群运行时失败", zap.Uint("cluster_id", cluster.ID), zap.String("cluster", cluster.Name), zap.Error(err))
			_ = st.UpdateClusterStatus(cluster.ID, store.ClusterStatusError, err.Error())
			continue
		}
		if err := runtimeManager.Register(runtime); err != nil {
			logger.Warn("注册集群运行时失败", zap.Uint("cluster_id", cluster.ID), zap.Error(err))
			_ = st.UpdateClusterStatus(cluster.ID, store.ClusterStatusError, err.Error())
			continue
		}
		logger.Info("已恢复集群运行时", zap.Uint("cluster_id", cluster.ID), zap.String("cluster", cluster.Name))
	}
}

func buildStoredClusterRuntime(st *store.Store, cluster *store.Cluster) (*clusterruntime.Runtime, error) {
	credential, err := st.GetClusterCredential(cluster.ID)
	if err != nil {
		return nil, err
	}
	policy, err := st.GetClusterPolicy(cluster.ID)
	if err != nil {
		return nil, err
	}
	var col *collector.Collector
	var remediator *remediate.Executor
	switch cluster.Mode {
	case store.ClusterModeKubeconfigData:
		key := os.Getenv("AIOPS_SECRET_KEY")
		kubeconfigData, err := security.DecryptString(credential.EncryptedKubeconfig, key)
		if err != nil {
			return nil, err
		}
		kubeClient, metricsClient, dynamicClient, restConfig, err := collector.NewClientsetsFromKubeconfigDataWithConfig([]byte(kubeconfigData), cluster.ContextName)
		if err != nil {
			return nil, err
		}
		col = collector.NewWithMetricsDynamicAndConfig(kubeClient, metricsClient, dynamicClient, restConfig)
		remediator = remediate.New(kubeClient, policy.RemediationEnabled)
	case store.ClusterModeKubeconfig:
		kubeClient, metricsClient, dynamicClient, restConfig, _, err := collector.NewClientsetsFromKubeconfigFileWithConfig(credential.KubeconfigPath, cluster.ContextName)
		if err != nil {
			return nil, err
		}
		col = collector.NewWithMetricsDynamicAndConfig(kubeClient, metricsClient, dynamicClient, restConfig)
		remediator = remediate.New(kubeClient, policy.RemediationEnabled)
	default:
		return nil, fmt.Errorf("不支持恢复的集群导入方式: %s", cluster.Mode)
	}
	engine := inspect.NewEngine(col, nil)
	engine.SetClusterID(cluster.ID) // 绑定集群,读取该集群的规则覆盖配置
	return &clusterruntime.Runtime{
		ClusterID:  cluster.ID,
		Cluster:    cluster,
		Collector:  col,
		Engine:     engine,
		Remediator: remediator,
	}, nil
}

func ensureDefaultCluster(st *store.Store, cfg *config.Config, source string) (*store.Cluster, error) {
	cluster, err := st.EnsureDefaultCluster(defaultClusterInput(cfg, source))
	if err != nil {
		return nil, err
	}
	if err := st.BackfillDefaultCluster(cluster.ID); err != nil {
		return nil, err
	}
	return cluster, nil
}

func defaultClusterInput(cfg *config.Config, source string) store.DefaultClusterInput {
	kubeconfigPath := strings.TrimSpace(cfg.Kubernetes.Kubeconfig)
	mode := store.ClusterModeInCluster
	const kubeconfigPrefix = "kubeconfig:"
	if strings.HasPrefix(source, kubeconfigPrefix) {
		mode = store.ClusterModeKubeconfig
		if kubeconfigPath == "" {
			kubeconfigPath = strings.TrimPrefix(source, kubeconfigPrefix)
		}
	} else if kubeconfigPath != "" {
		mode = store.ClusterModeKubeconfig
	}
	return store.DefaultClusterInput{
		Name:           "默认集群",
		Mode:           mode,
		KubeconfigPath: kubeconfigPath,
	}
}
