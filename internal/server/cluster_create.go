package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"aiops/internal/clusterruntime"
	"aiops/internal/clustersync"
	"aiops/internal/collector"
	"aiops/internal/inspect"
	"aiops/internal/remediate"
	"aiops/internal/security"
	"aiops/internal/store"
)

type createClusterRequest struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Description   string `json:"description"`
	ContextName   string `json:"context_name"`
	APIServerHost string `json:"api_server_host"`
	Kubeconfig    string `json:"kubeconfig"`
}

type updateClusterRequest struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Description   string `json:"description"`
	ContextName   string `json:"context_name"`
	APIServerHost string `json:"api_server_host"`
	Kubeconfig    string `json:"kubeconfig"`
}

const maxManagedClusters = 5

// handleCreateCluster 通过 kubeconfig 文本接入外部 Kubernetes 集群。
func (s *Server) handleCreateCluster(c echo.Context) error {
	if err := s.requireGlobalAdmin(c); err != nil {
		return err
	}
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	var req createClusterRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Kubeconfig = strings.TrimSpace(req.Kubeconfig)
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "集群名称不能为空"})
	}
	if req.Kubeconfig == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "kubeconfig 不能为空"})
	}
	count, err := s.store.CountClusters()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if count >= maxManagedClusters {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "最多只能接入 5 个集群"})
	}

	encrypted, hash, err := encryptKubeconfig(req.Kubeconfig)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	cluster, err := s.store.CreateCluster(store.CreateClusterInput{
		Name:                req.Name,
		Slug:                req.Slug,
		Description:         req.Description,
		Mode:                store.ClusterModeKubeconfigData,
		ContextName:         req.ContextName,
		APIServerHost:       req.APIServerHost,
		Status:              store.ClusterStatusUnknown,
		EncryptedKubeconfig: encrypted,
		KubeconfigHash:      hash,
		RemediationEnabled:  true,
	})
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	policy, err := s.store.GetClusterPolicy(cluster.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	runtime, err := s.runtimeBuilder(c.Request().Context(), cluster, policy, req.Kubeconfig)
	if err != nil {
		_ = s.store.UpdateClusterStatus(cluster.ID, store.ClusterStatusError, err.Error())
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	if runtime.ClusterID == 0 {
		runtime.ClusterID = cluster.ID
	}
	if runtime.Engine != nil {
		runtime.Engine.SetDiagnoser(s.currentDiagnoser())
	}
	if err := s.runtimeManager.Register(runtime); err != nil {
		_ = s.store.UpdateClusterStatus(cluster.ID, store.ClusterStatusError, err.Error())
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	s.applyPromQuerier(cluster.ID) // 按生效地址(集群独立/全局)初始化 Prometheus 查询器

	nodeCount := 0
	syncResult, syncErr := clustersync.SyncClusterWithSource(c.Request().Context(), s.store, runtime, store.SyncSourceImport)
	if syncResult != nil {
		for _, row := range syncResult.Resources {
			if row.ResourceName == "nodes" && row.ItemCount >= 0 {
				nodeCount = row.ItemCount
				break
			}
		}
	}
	if syncErr != nil && s.logger != nil {
		s.logger.Warn("新增集群同步失败: " + syncErr.Error())
	}
	updated, err := s.store.GetCluster(cluster.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusCreated, map[string]any{
		"cluster":       toClusterListItem(*updated, s.runtimeForClusterID),
		"node_count":    nodeCount,
		"runtime_ready": updated.Status == store.ClusterStatusReady,
		"sync":          syncResult,
	})
}

// handleUpdateCluster 编辑已有集群。kubeconfig 为空时只更新展示信息。
func (s *Server) handleUpdateCluster(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	clusterID64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || clusterID64 == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的集群 ID"})
	}
	clusterID := uint(clusterID64)
	if err := s.requireClusterPermission(c, store.PermissionManageClusters, clusterID); err != nil {
		return err
	}

	var req updateClusterRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(req.Slug)
	req.Description = strings.TrimSpace(req.Description)
	req.ContextName = strings.TrimSpace(req.ContextName)
	req.APIServerHost = strings.TrimSpace(req.APIServerHost)
	req.Kubeconfig = strings.TrimSpace(req.Kubeconfig)
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "集群名称不能为空"})
	}

	cluster, err := s.store.GetCluster(clusterID)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "集群不存在"})
	}
	input := store.UpdateClusterInput{
		Name:          req.Name,
		Slug:          req.Slug,
		Description:   req.Description,
		Mode:          cluster.Mode,
		ContextName:   req.ContextName,
		APIServerHost: req.APIServerHost,
	}

	var runtime *clusterruntime.Runtime
	if req.Kubeconfig != "" {
		encrypted, hash, err := encryptKubeconfig(req.Kubeconfig)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		input.Mode = store.ClusterModeKubeconfigData
		input.EncryptedKubeconfig = encrypted
		input.KubeconfigHash = hash

		policy, err := s.store.GetClusterPolicy(clusterID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		candidate := *cluster
		candidate.Name = req.Name
		if req.Slug != "" {
			candidate.Slug = req.Slug
		}
		candidate.Description = req.Description
		candidate.Mode = store.ClusterModeKubeconfigData
		candidate.ContextName = req.ContextName
		candidate.APIServerHost = req.APIServerHost
		runtime, err = s.runtimeBuilder(c.Request().Context(), &candidate, policy, req.Kubeconfig)
		if err != nil {
			return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		}
	}

	updated, err := s.store.UpdateCluster(clusterID, input)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if runtime != nil {
		if runtime.ClusterID == 0 {
			runtime.ClusterID = updated.ID
		}
		runtime.Cluster = updated
		if runtime.Engine != nil {
			runtime.Engine.SetDiagnoser(s.currentDiagnoser())
		}
		if err := s.runtimeManager.Register(runtime); err != nil {
			_ = s.store.UpdateClusterStatus(updated.ID, store.ClusterStatusError, err.Error())
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		s.applyPromQuerier(updated.ID) // 按生效地址初始化 Prometheus 查询器
	} else if s.runtimeManager != nil {
		if existing, err := s.runtimeManager.Get(updated.ID); err == nil {
			existing.Cluster = updated
		}
	}

	runtimeReady := runtime != nil
	if !runtimeReady {
		if _, err := s.runtimeForClusterID(updated.ID); err == nil {
			runtimeReady = true
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"cluster":       toClusterListItem(*updated, s.runtimeForClusterID),
		"runtime_ready": runtimeReady,
	})
}

func encryptKubeconfig(kubeconfigData string) (string, string, error) {
	key := os.Getenv("AIOPS_SECRET_KEY")
	if len([]byte(key)) != 32 {
		return "", "", fmt.Errorf("AIOPS_SECRET_KEY 必须配置为 32 字节后才能保存 kubeconfig")
	}
	encrypted, err := security.EncryptString(kubeconfigData, key)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(kubeconfigData))
	return encrypted, hex.EncodeToString(sum[:]), nil
}

func defaultClusterRuntimeBuilder(ctx context.Context, cluster *store.Cluster, policy *store.ClusterPolicy, kubeconfigData string) (*clusterruntime.Runtime, error) {
	kubeClient, metricsClient, dynamicClient, err := collector.NewClientsetsFromKubeconfigDataWithContext([]byte(kubeconfigData), cluster.ContextName)
	if err != nil {
		return nil, err
	}
	col := collector.NewWithMetricsAndDynamic(kubeClient, metricsClient, dynamicClient)
	rem := remediate.New(kubeClient, policy.RemediationEnabled)
	engine := inspect.NewEngine(col, nil)
	engine.SetClusterID(cluster.ID) // 绑定集群,读取该集群的规则覆盖配置
	return &clusterruntime.Runtime{
		ClusterID:  cluster.ID,
		Cluster:    cluster,
		Collector:  col,
		Engine:     engine,
		Remediator: rem,
	}, nil
}
