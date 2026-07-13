package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"aiops/internal/clustersync"
	"aiops/internal/collector"
	"aiops/internal/store"
)

var (
	syncMu            sync.Mutex
	errSyncInProgress = errors.New("集群同步正在进行")
)

type syncStatusTracker struct {
	mu         sync.RWMutex
	running    bool
	source     string
	startedAt  time.Time
	finishedAt time.Time
	nextRunAt  time.Time
	lastError  string
}

type syncStatusSnapshot struct {
	Running    bool
	Source     string
	StartedAt  time.Time
	FinishedAt time.Time
	NextRunAt  time.Time
	LastError  string
}

type clusterSyncStatusItem struct {
	ClusterID         uint       `json:"cluster_id"`
	ClusterName       string     `json:"cluster_name"`
	ClusterStatus     string     `json:"cluster_status"`
	LastSource        string     `json:"last_source"`
	LastSyncedAt      *time.Time `json:"last_synced_at,omitempty"`
	LastSuccessfulAt  *time.Time `json:"last_successful_at,omitempty"`
	Status            string     `json:"status"`
	IncrementalStatus string     `json:"incremental_status"`
	DataStale         bool       `json:"data_stale"`
	Error             string     `json:"error,omitempty"`
}

func (t *syncStatusTracker) begin(source string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running = true
	t.source = source
	t.startedAt = time.Now()
	t.finishedAt = time.Time{}
	t.lastError = ""
}

func (t *syncStatusTracker) finish(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running = false
	t.finishedAt = time.Now()
	if err != nil {
		t.lastError = err.Error()
	}
}

func (t *syncStatusTracker) setNextRun(next time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextRunAt = next
}

func (t *syncStatusTracker) snapshot() syncStatusSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return syncStatusSnapshot{
		Running:    t.running,
		Source:     t.source,
		StartedAt:  t.startedAt,
		FinishedAt: t.finishedAt,
		NextRunAt:  t.nextRunAt,
		LastError:  t.lastError,
	}
}

// handleListClusterSyncResources 返回指定集群最近一次资源同步状态。
func (s *Server) handleListClusterSyncResources(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	clusterID, err := clusterIDFromPath(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionViewClusters, clusterID); err != nil {
		return err
	}
	rows, err := s.store.ListClusterSyncResources(clusterID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(rows), "resources": rows})
}

// handleListClusterResourceSnapshots 返回指定集群最近一次同步保存的资源对象快照。
func (s *Server) handleListClusterResourceSnapshots(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	clusterID, err := clusterIDFromPath(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionViewClusters, clusterID); err != nil {
		return err
	}
	limit := 200
	if raw := c.QueryParam("limit"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			limit = parsed
		}
	}
	rows, err := s.store.ListClusterResourceSnapshots(clusterID, c.QueryParam("resource"), limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(rows), "snapshots": rows})
}

// handleGetClusterResourceObject 使用动态客户端实时获取单个对象并返回其 YAML。
// 列表数据来自快照缓存,单对象内容则实时拉取,保证 YAML 反映集群当前状态。
func (s *Server) handleGetClusterResourceObject(c echo.Context) error {
	clusterID, err := clusterIDFromPath(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionViewClusters, clusterID); err != nil {
		return err
	}
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	if runtime.Collector == nil {
		return s.errNoCollector(c)
	}
	detail, err := runtime.Collector.GetResourceObjectYAML(
		c.Request().Context(),
		c.QueryParam("api_version"),
		c.QueryParam("resource"),
		c.QueryParam("namespace"),
		c.QueryParam("name"),
	)
	if err != nil {
		if errors.Is(err, collector.ErrInvalidResourceRequest) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, detail)
}

// handleListClusterDeletedResources 返回资源缓存中已消失对象的删除历史。
func (s *Server) handleListClusterDeletedResources(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	clusterID, err := clusterIDFromPath(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionViewClusters, clusterID); err != nil {
		return err
	}
	limit := 100
	if raw := c.QueryParam("limit"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			limit = parsed
		}
	}
	rows, err := s.store.ListClusterDeletedResources(clusterID, c.QueryParam("resource"), limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(rows), "resources": rows})
}

// handleListClusterSyncTasks 返回指定集群最近的同步任务记录。
func (s *Server) handleListClusterSyncTasks(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	clusterID, err := clusterIDFromPath(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionViewClusters, clusterID); err != nil {
		return err
	}
	limit := 20
	if raw := c.QueryParam("limit"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			limit = parsed
		}
	}
	tasks, err := s.store.ListClusterSyncTasks(clusterID, limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(tasks), "tasks": tasks})
}

// handleClusterSyncStatus 返回全局同步运行状态与各集群最近一次同步来源。
func (s *Server) handleClusterSyncStatus(c echo.Context) error {
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
	items := make([]clusterSyncStatusItem, 0, len(clusters))
	for _, cluster := range clusters {
		if !all {
			if _, ok := allowed[cluster.ID]; !ok {
				continue
			}
		}
		items = append(items, s.clusterSyncStatus(cluster))
	}
	state := s.syncState.snapshot()
	return c.JSON(http.StatusOK, map[string]any{
		"running":     state.Running,
		"source":      state.Source,
		"started_at":  timeOrNil(state.StartedAt),
		"finished_at": timeOrNil(state.FinishedAt),
		"next_run_at": timeOrNil(state.NextRunAt),
		"last_error":  state.LastError,
		"clusters":    items,
	})
}

func (s *Server) clusterSyncStatus(cluster store.Cluster) clusterSyncStatusItem {
	item := clusterSyncStatusItem{
		ClusterID:         cluster.ID,
		ClusterName:       cluster.Name,
		ClusterStatus:     cluster.Status,
		Status:            store.SyncStatusNA,
		IncrementalStatus: store.SyncStatusNA,
	}
	rows, err := s.store.ListClusterSyncResources(cluster.ID)
	if err != nil {
		item.Error = err.Error()
		return item
	}
	historyStatus := store.SyncStatusNA
	incrementalStatus := store.SyncStatusNA
	if len(rows) > 0 {
		historyStatus = store.SyncStatusSuccess
	}
	for _, row := range rows {
		if row.UpdatedAt.After(timeValue(item.LastSyncedAt)) {
			updatedAt := row.UpdatedAt
			item.LastSyncedAt = &updatedAt
			item.LastSource = row.LastSource
		}
		if row.FullStatus == store.SyncStatusSuccess {
			successAt := row.UpdatedAt
			if row.FullFinishedAt != nil {
				successAt = *row.FullFinishedAt
			}
			if successAt.After(timeValue(item.LastSuccessfulAt)) {
				item.LastSuccessfulAt = &successAt
			}
		}
		if row.FullStatus == store.SyncStatusFailed || row.IncrementalStatus == store.SyncStatusFailed {
			historyStatus = store.SyncStatusFailed
			if item.Error == "" {
				item.Error = row.FullError
			}
		}
		if row.IncrementalStatus == store.SyncStatusFailed {
			incrementalStatus = store.SyncStatusFailed
		} else if row.IncrementalStatus == store.SyncStatusSuccess && incrementalStatus != store.SyncStatusFailed {
			incrementalStatus = store.SyncStatusSuccess
		}
	}
	item.Status = historyStatus
	item.IncrementalStatus = incrementalStatus

	tasks, taskErr := s.store.ListClusterSyncTasks(cluster.ID, 1)
	if taskErr != nil {
		if item.Error == "" {
			item.Error = taskErr.Error()
		}
		return item
	}
	if len(tasks) == 0 {
		return item
	}
	latest := tasks[0]
	item.Status = latest.Status
	item.LastSource = latest.Source
	if latest.FinishedAt != nil {
		finishedAt := *latest.FinishedAt
		item.LastSyncedAt = &finishedAt
	} else {
		startedAt := latest.StartedAt
		item.LastSyncedAt = &startedAt
	}
	if latest.Error != "" {
		item.Error = latest.Error
	}
	item.DataStale = cluster.Status == store.ClusterStatusDisconnected && item.LastSuccessfulAt != nil
	if item.DataStale && latest.Status == store.SyncStatusFailed && latest.ResourceCount == 0 {
		item.IncrementalStatus = store.SyncStatusNotRun
	}
	return item
}

// handleSyncCluster 对指定集群执行一次只读资源同步。
func (s *Server) handleSyncCluster(c echo.Context) error {
	clusterID, err := clusterIDFromPath(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := s.requireClusterPermission(c, store.PermissionManageClusters, clusterID); err != nil {
		return err
	}
	result, syncErr := s.syncCluster(c, clusterID)
	if errors.Is(syncErr, errSyncInProgress) {
		return c.JSON(http.StatusConflict, map[string]string{"error": syncErr.Error()})
	}
	if syncErr != nil && result == nil {
		return c.JSON(runtimeErrorStatus(syncErr), map[string]string{"error": syncErr.Error()})
	}
	return c.JSON(http.StatusOK, result)
}

// handleSyncAllClusters 对所有已接入集群执行一次只读资源同步。
func (s *Server) handleSyncAllClusters(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageClusters); err != nil {
		return err
	}
	ids, all, err := s.authorizedClusterIDs(c)
	if err != nil {
		return err
	}
	var results []*clustersync.Result
	if all {
		results, err = s.syncAllClustersLocked(c.Request().Context(), store.SyncSourceManual)
	} else {
		results, err = s.syncClusterIDsLocked(c.Request().Context(), store.SyncSourceManual, ids)
	}
	if err != nil {
		if errors.Is(err, errSyncInProgress) {
			return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(results), "results": results})
}

// StartAutoSync 启动后台定时同步。调用方需要传入服务生命周期 context。
func (s *Server) StartAutoSync(ctx context.Context, interval, initialDelay time.Duration) {
	if interval <= 0 {
		return
	}
	if initialDelay < 0 {
		initialDelay = 0
	}
	if s.logger != nil {
		s.logger.Info("集群自动同步调度器已启用",
			zap.Duration("interval", interval),
			zap.Duration("initial_delay", initialDelay),
		)
	}
	s.syncState.setNextRun(time.Now().Add(initialDelay))
	go func() {
		timer := time.NewTimer(initialDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.runScheduledSync(ctx)
		}
		s.syncState.setNextRun(time.Now().Add(interval))

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runScheduledSync(ctx)
				s.syncState.setNextRun(time.Now().Add(interval))
			}
		}
	}()
}

func (s *Server) runScheduledSync(ctx context.Context) {
	results, err := s.syncAllClustersLocked(ctx, store.SyncSourceAuto)
	if s.logger == nil {
		return
	}
	if errors.Is(err, errSyncInProgress) {
		s.logger.Info("集群自动同步跳过:已有同步任务正在运行")
		return
	}
	if err != nil {
		s.logger.Warn("集群自动同步失败", zap.Error(err))
		return
	}
	failed := 0
	for _, result := range results {
		if result != nil && result.Status == store.ClusterStatusError {
			failed++
		}
	}
	s.logger.Info("集群自动同步完成", zap.Int("clusters", len(results)), zap.Int("failed", failed))
}

func (s *Server) syncAllClustersLocked(ctx context.Context, source string) ([]*clustersync.Result, error) {
	if !syncMu.TryLock() {
		return nil, errSyncInProgress
	}
	defer syncMu.Unlock()
	s.syncState.begin(source)
	results, err := s.syncAllClusters(ctx, source)
	s.syncState.finish(err)
	return results, err
}

func (s *Server) syncClusterIDsLocked(ctx context.Context, source string, clusterIDs []uint) ([]*clustersync.Result, error) {
	if !syncMu.TryLock() {
		return nil, errSyncInProgress
	}
	defer syncMu.Unlock()
	s.syncState.begin(source)
	results, err := s.syncClusterIDs(ctx, source, clusterIDs)
	s.syncState.finish(err)
	return results, err
}

func (s *Server) syncAllClusters(ctx context.Context, source string) ([]*clustersync.Result, error) {
	if s.store == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	clusters, err := s.store.ListClusters()
	if err != nil {
		return nil, err
	}
	results := make([]*clustersync.Result, 0, len(clusters))
	for _, cluster := range clusters {
		result, syncErr := s.syncClusterWithContext(ctx, cluster.ID, source)
		if syncErr != nil && result == nil {
			result = &clustersync.Result{ClusterID: cluster.ID, Status: store.ClusterStatusError, Error: syncErr.Error()}
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Server) syncClusterIDs(ctx context.Context, source string, clusterIDs []uint) ([]*clustersync.Result, error) {
	if s.store == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	results := make([]*clustersync.Result, 0, len(clusterIDs))
	for _, clusterID := range clusterIDs {
		result, syncErr := s.syncClusterWithContext(ctx, clusterID, source)
		if syncErr != nil && result == nil {
			result = &clustersync.Result{ClusterID: clusterID, Status: store.ClusterStatusError, Error: syncErr.Error()}
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *Server) syncCluster(c echo.Context, clusterID uint) (*clustersync.Result, error) {
	return s.syncClusterLocked(c.Request().Context(), clusterID, store.SyncSourceManual)
}

func (s *Server) syncClusterLocked(ctx context.Context, clusterID uint, source string) (*clustersync.Result, error) {
	if !syncMu.TryLock() {
		return nil, errSyncInProgress
	}
	defer syncMu.Unlock()
	s.syncState.begin(source)
	result, err := s.syncClusterWithContext(ctx, clusterID, source)
	s.syncState.finish(err)
	return result, err
}

func (s *Server) syncClusterWithContext(ctx context.Context, clusterID uint, source string) (*clustersync.Result, error) {
	if s.store == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil {
		_ = s.store.UpdateClusterStatus(clusterID, store.ClusterStatusError, err.Error())
		return &clustersync.Result{ClusterID: clusterID, Status: store.ClusterStatusError, Error: err.Error()}, err
	}
	return clustersync.SyncClusterWithSource(ctx, s.store, runtime, source)
}

func clusterIDFromPath(c echo.Context) (uint, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("无效的集群 ID")
	}
	return uint(id), nil
}

func timeOrNil(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
