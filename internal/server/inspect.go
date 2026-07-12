package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"aiops/internal/inspect"
	"aiops/internal/store"
)

type inspectClusterResult struct {
	ClusterID   uint            `json:"cluster_id"`
	ClusterName string          `json:"cluster_name"`
	Report      *inspect.Report `json:"report,omitempty"`
	Error       string          `json:"error,omitempty"`
}

// handleInspectRun 触发一次集群巡检,返回报告(含异常列表+AI诊断),并落库。
func (s *Server) handleInspectRun(c echo.Context) error {
	if err := requirePermission(c, store.PermissionRunInspect); err != nil {
		return err
	}
	if clusterID, ok, err := s.optionalClusterIDFromRequest(c); err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	} else if ok {
		return s.runInspectForCluster(c, clusterID)
	}
	return s.runInspectForAllClusters(c)
}

func (s *Server) runInspectForCluster(c echo.Context, clusterID uint) error {
	if err := s.requireClusterPermission(c, store.PermissionRunInspect, clusterID); err != nil {
		return err
	}
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	if runtime.Engine == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "巡检引擎未就绪(集群客户端不可用)",
		})
	}
	if s.store != nil {
		_, _ = s.syncClusterLocked(c.Request().Context(), clusterID, store.SyncSourceInspect)
	}
	report, err := s.runInspection(c, runtime.ClusterID, runtime.Engine)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, report)
}

func (s *Server) runInspectForAllClusters(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	clusters, err := s.store.ListClusters()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	ids, all, err := s.authorizedClusterIDs(c)
	if err != nil {
		return err
	}
	allowed := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		allowed[id] = struct{}{}
	}
	results := make([]inspectClusterResult, 0, len(clusters))
	var syncErr error
	if all {
		_, syncErr = s.syncAllClustersLocked(c.Request().Context(), store.SyncSourceInspect)
	} else {
		_, syncErr = s.syncClusterIDsLocked(c.Request().Context(), store.SyncSourceInspect, ids)
	}
	if syncErr != nil && !errors.Is(syncErr, errSyncInProgress) && s.logger != nil {
		s.logger.Warn("巡检前同步失败: " + syncErr.Error())
	}
	for _, cluster := range clusters {
		if !all {
			if _, ok := allowed[cluster.ID]; !ok {
				continue
			}
		}
		if !cluster.Enabled {
			continue
		}
		result := inspectClusterResult{ClusterID: cluster.ID, ClusterName: cluster.Name}
		runtime, runtimeErr := s.runtimeForClusterID(cluster.ID)
		if runtimeErr != nil {
			result.Error = runtimeErr.Error()
			results = append(results, result)
			continue
		}
		report, inspectErr := s.runInspection(c, cluster.ID, runtime.Engine)
		if inspectErr != nil {
			result.Error = responseErrorText(inspectErr)
		} else {
			result.Report = report
		}
		results = append(results, result)
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(results), "clusters": results})
}

func (s *Server) runInspection(c echo.Context, clusterID uint, engine *inspect.Engine) (*inspect.Report, error) {
	if engine == nil {
		return nil, errors.New("巡检引擎未就绪(集群客户端不可用)")
	}
	report, err := engine.Run(c.Request().Context())
	if err != nil {
		return nil, err
	}
	s.attachResourceCacheSummary(report, clusterID)
	// 落库(失败不影响返回当前结果)
	if s.store != nil {
		if _, serr := s.store.SaveReport(clusterID, report); serr != nil {
			if s.logger != nil {
				s.logger.Warn("巡检报告落库失败: " + serr.Error())
			}
		}
	}
	return report, nil
}

func (s *Server) attachResourceCacheSummary(report *inspect.Report, clusterID uint) {
	if report == nil || s.store == nil || clusterID == 0 {
		return
	}
	rows, err := s.store.ListClusterSyncResources(clusterID)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("读取资源缓存摘要失败: " + err.Error())
		}
		return
	}
	report.ResourceCache = store.ResourceCacheSummaryFromSyncResources(rows)
}

// handleListReports 返回历史巡检报告列表。
func (s *Server) handleListReports(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionRunInspect); err != nil {
		return err
	}
	clusterID, ok, err := s.optionalClusterIDFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	var recs []store.ReportRecord
	if ok {
		if err := s.requireClusterPermission(c, store.PermissionRunInspect, clusterID); err != nil {
			return err
		}
		recs, err = s.store.ListReports(clusterID, 50)
	} else {
		recs, err = s.store.ListAllReports(50)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	recs, err = s.filterReportsForCurrentUser(c, recs)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(recs), "reports": recs})
}

// handleGetReport 返回单个历史报告详情(含异常与 AI 诊断)。
func (s *Server) handleGetReport(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionRunInspect); err != nil {
		return err
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的报告 ID"})
	}
	clusterID, ok, err := s.optionalClusterIDFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	var rec *store.ReportRecord
	if ok {
		if err := s.requireClusterPermission(c, store.PermissionRunInspect, clusterID); err != nil {
			return err
		}
		rec, err = s.store.GetReport(clusterID, uint(id))
	} else {
		rec, err = s.store.GetReportByID(uint(id))
	}
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "报告不存在"})
	}
	if rec.ClusterID == nil || *rec.ClusterID == 0 {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "报告缺少集群归属,禁止访问"})
	}
	if err := s.requireClusterPermission(c, store.PermissionRunInspect, *rec.ClusterID); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, rec)
}

func (s *Server) filterReportsForCurrentUser(c echo.Context, reports []store.ReportRecord) ([]store.ReportRecord, error) {
	allowed, all, err := s.authorizedClusterIDSet(c)
	if err != nil {
		return nil, err
	}
	if all {
		return reports, nil
	}
	filtered := make([]store.ReportRecord, 0, len(reports))
	for _, report := range reports {
		if report.ClusterID == nil {
			continue
		}
		if _, ok := allowed[*report.ClusterID]; ok {
			filtered = append(filtered, report)
		}
	}
	return filtered, nil
}

func responseErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
