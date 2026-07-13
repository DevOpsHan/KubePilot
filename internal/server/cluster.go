package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/labstack/echo/v4"

	"aiops/internal/clusterruntime"
	"aiops/internal/store"
)

type clusterListItem struct {
	ID              uint   `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Description     string `json:"description"`
	Mode            string `json:"mode"`
	ContextName     string `json:"context_name"`
	APIServerHost   string `json:"api_server_host"`
	Version         string `json:"version"`
	Status          string `json:"status"`
	Enabled         bool   `json:"enabled"`
	RuntimeReady    bool   `json:"runtime_ready"`
	LastError       string `json:"last_error"`
	LastValidatedAt string `json:"last_validated_at,omitempty"`
}

// errNoCollector 集群客户端未就绪时的统一响应。
func (s *Server) errNoCollector(c echo.Context) error {
	return c.JSON(http.StatusServiceUnavailable, map[string]string{
		"error": "K8S 客户端未就绪,请检查 kubeconfig 或集群连通性",
	})
}

// handleListClusters 返回平台已接入的集群列表。
func (s *Server) handleListClusters(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	clusters, err := s.store.ListClusters()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	allowed, all, err := s.authorizedClusterIDSet(c)
	if err != nil {
		return err
	}
	items := make([]clusterListItem, 0, len(clusters))
	for _, cluster := range clusters {
		if !all {
			if _, ok := allowed[cluster.ID]; !ok {
				continue
			}
		}
		items = append(items, toClusterListItem(cluster, s.runtimeForClusterID))
	}
	s.applyLiveReachability(c.Request().Context(), items)
	return c.JSON(http.StatusOK, map[string]any{"count": len(items), "clusters": items})
}

// handleDeleteCluster 删除已接入集群。历史报告和审计仍保留 cluster_id 便于追溯。
func (s *Server) handleDeleteCluster(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageClusters); err != nil {
		return err
	}
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的集群 ID"})
	}
	if err := s.requireClusterPermission(c, store.PermissionManageClusters, uint(id)); err != nil {
		return err
	}
	if err := s.store.DeleteCluster(uint(id)); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if s.runtimeManager != nil {
		s.runtimeManager.Remove(uint(id))
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func toClusterListItem(cluster store.Cluster, runtimeForClusterID func(uint) (*clusterruntime.Runtime, error)) clusterListItem {
	_, runtimeErr := runtimeForClusterID(cluster.ID)
	item := clusterListItem{
		ID:            cluster.ID,
		Name:          cluster.Name,
		Slug:          cluster.Slug,
		Description:   cluster.Description,
		Mode:          cluster.Mode,
		ContextName:   cluster.ContextName,
		APIServerHost: cluster.APIServerHost,
		Version:       cluster.Version,
		Status:        cluster.Status,
		Enabled:       cluster.Enabled,
		RuntimeReady:  runtimeErr == nil,
		LastError:     cluster.LastError,
	}
	if cluster.LastValidatedAt != nil {
		item.LastValidatedAt = cluster.LastValidatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return item
}

// applyLiveReachability 对运行时就绪且当前展示为 ready/degraded 的集群做实时连通性探测,
// 探测失败即把该行展示状态下调为 disconnected(仅影响本次响应,不写库;写库仍由同步流程负责)。
// 这样即便距上次同步不足一个周期,列表也能即时反映"集群已断连"。
func (s *Server) applyLiveReachability(ctx context.Context, items []clusterListItem) {
	if s.runtimeManager == nil || len(items) == 0 {
		return
	}
	var wg sync.WaitGroup
	for i := range items {
		item := &items[i]
		// 仅对"看起来正常"的集群探测:未就绪/已断连/导入中等状态无需再探,避免无意义开销
		if !item.RuntimeReady {
			continue
		}
		if item.Status != store.ClusterStatusReady && item.Status != store.ClusterStatusDegraded {
			continue
		}
		runtime, err := s.runtimeManager.Get(item.ID)
		if err != nil || runtime == nil || runtime.Collector == nil {
			continue
		}
		wg.Add(1)
		go func(item *clusterListItem, col interface {
			Ping(context.Context) error
		}) {
			defer wg.Done()
			if err := col.Ping(ctx); err != nil {
				item.Status = store.ClusterStatusDisconnected
				item.RuntimeReady = false
				if item.LastError == "" {
					item.LastError = fmt.Sprintf("集群 API Server 连接失败: %v", err)
				}
			}
		}(item, runtime.Collector)
	}
	wg.Wait()
}

// handleClusterOverview 返回集群概览(节点/Pod/命名空间统计)。
func (s *Server) handleClusterOverview(c echo.Context) error {
	runtime, err := s.runtimeFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	if runtime.Collector == nil {
		return s.errNoCollector(c)
	}
	overview, err := runtime.Collector.Overview(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, overview)
}

// handleListPods 列出 Pod,支持 ?namespace=xxx 过滤(空=全部)。
func (s *Server) handleListPods(c echo.Context) error {
	runtime, err := s.runtimeFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	if runtime.Collector == nil {
		return s.errNoCollector(c)
	}
	namespace := c.QueryParam("namespace")
	pods, err := runtime.Collector.ListPods(c.Request().Context(), namespace)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(pods), "pods": pods})
}

// handleListNodes 列出集群节点。
func (s *Server) handleListNodes(c echo.Context) error {
	runtime, err := s.runtimeFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	if runtime.Collector == nil {
		return s.errNoCollector(c)
	}
	nodes, err := runtime.Collector.ListNodes(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(nodes), "nodes": nodes})
}

// handleListNodeMetrics 列出节点 CPU/内存实时使用情况。
func (s *Server) handleListNodeMetrics(c echo.Context) error {
	runtime, err := s.runtimeFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	if runtime.Collector == nil {
		return s.errNoCollector(c)
	}
	metrics, err := runtime.Collector.ListNodeMetrics(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(metrics), "metrics": metrics})
}
