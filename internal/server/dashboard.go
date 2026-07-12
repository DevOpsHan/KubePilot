package server

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"aiops/internal/store"
)

type dashboardSummaryResponse struct {
	ClusterCount     int        `json:"cluster_count"`
	ReadyClusters    int        `json:"ready_clusters"`
	ProblemClusters  int        `json:"problem_clusters"`
	UnknownClusters  int        `json:"unknown_clusters"`
	PendingApprovals int        `json:"pending_approvals"`
	ActiveAlerts     int        `json:"active_alerts"`
	RecentReports    int        `json:"recent_reports"`
	RecentAnomalies  int        `json:"recent_anomalies"`
	SyncRunning      bool       `json:"sync_running"`
	SyncSource       string     `json:"sync_source"`
	SyncStartedAt    *time.Time `json:"sync_started_at,omitempty"`
	SyncFinishedAt   *time.Time `json:"sync_finished_at,omitempty"`
	NextSyncAt       *time.Time `json:"next_sync_at,omitempty"`
	LastSyncError    string     `json:"last_sync_error,omitempty"`
}

// handleDashboardSummary 返回后续告警与可视化页面可复用的运行摘要。
func (s *Server) handleDashboardSummary(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionViewClusters); err != nil {
		return err
	}
	clusters, err := s.store.ListClusters()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	allowed, all, err := s.authorizedClusterIDSet(c)
	if err != nil {
		return err
	}
	summary := dashboardSummaryResponse{}
	for _, cluster := range clusters {
		if !all {
			if _, ok := allowed[cluster.ID]; !ok {
				continue
			}
		}
		summary.ClusterCount++
		switch cluster.Status {
		case store.ClusterStatusReady, store.ClusterStatusImported:
			summary.ReadyClusters++
		case store.ClusterStatusDegraded, store.ClusterStatusDisconnected, store.ClusterStatusError:
			summary.ProblemClusters++
		default:
			summary.UnknownClusters++
		}
	}

	user := currentUser(c)
	if user != nil && store.RoleHasPermission(user.Role, store.PermissionViewApprovals) {
		tickets, ticketErr := s.store.ListAllTickets(store.TicketPending, 200)
		if ticketErr != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": ticketErr.Error()})
		}
		filtered, filterErr := s.filterTicketsForCurrentUser(c, tickets)
		if filterErr != nil {
			return filterErr
		}
		summary.PendingApprovals = len(filtered)
	}

	if user != nil && store.RoleHasPermission(user.Role, store.PermissionViewAlerts) {
		alerts, alertErr := s.store.ListAllAlerts(store.AlertStatusActive, "", 500)
		if alertErr != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": alertErr.Error()})
		}
		filtered, filterErr := s.filterAlertsForCurrentUser(c, alerts)
		if filterErr != nil {
			return filterErr
		}
		summary.ActiveAlerts = len(filtered)
	}

	if user != nil && store.RoleHasPermission(user.Role, store.PermissionRunInspect) {
		reports, reportErr := s.store.ListAllReports(100)
		if reportErr != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": reportErr.Error()})
		}
		filtered, filterErr := s.filterReportsForCurrentUser(c, reports)
		if filterErr != nil {
			return filterErr
		}
		summary.RecentReports = len(filtered)
		for _, report := range filtered {
			summary.RecentAnomalies += report.AnomalyCount
		}
	}

	state := s.syncState.snapshot()
	summary.SyncRunning = state.Running
	summary.SyncSource = state.Source
	summary.SyncStartedAt = timeOrNil(state.StartedAt)
	summary.SyncFinishedAt = timeOrNil(state.FinishedAt)
	summary.NextSyncAt = timeOrNil(state.NextRunAt)
	summary.LastSyncError = state.LastError

	return c.JSON(http.StatusOK, summary)
}
