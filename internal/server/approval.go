package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"aiops/internal/store"
)

type approvalTicketResponse struct {
	store.ApprovalTicket
	ClusterName         string `json:"cluster_name"`
	ApproverDisplayName string `json:"approver_display_name"`
}

type auditLogResponse struct {
	store.AuditLog
	ClusterName         string `json:"cluster_name"`
	OperatorDisplayName string `json:"operator_display_name"`
}

// handleListApprovals 返回审批工单列表。?status=pending 可过滤。
func (s *Server) handleListApprovals(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionViewApprovals); err != nil {
		return err
	}
	status := c.QueryParam("status")
	approver := c.QueryParam("approver")
	clusterID, ok, err := s.optionalClusterIDFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	var ts []store.ApprovalTicket
	if ok {
		if err := s.requireClusterPermission(c, store.PermissionViewApprovals, clusterID); err != nil {
			return err
		}
		ts, err = s.store.ListTickets(clusterID, status, 100)
	} else {
		ts, err = s.store.ListAllTickets(status, 100)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	filtered, err := s.filterTicketsForCurrentUser(c, ts)
	if err != nil {
		return err
	}
	filtered = filterTicketsByApprover(filtered, approver)
	responses, err := s.approvalTicketResponses(filtered)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(responses), "tickets": responses})
}

// handleApproveTicket 批准工单 → 执行修复 → 更新状态 → 写审计。
func (s *Server) handleApproveTicket(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的工单 ID"})
	}
	requestClusterID, hasRequestClusterID, err := s.optionalClusterIDFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	t, err := s.store.GetTicketByID(uint(id))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "工单不存在"})
	}
	if t.Status != store.TicketPending {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "工单已处理,当前状态: " + t.Status})
	}
	ticketClusterID, err := requiredTicketClusterID(t)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if hasRequestClusterID && requestClusterID != ticketClusterID {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求集群与工单所属集群不一致"})
	}
	if err := s.requireClusterPermission(c, store.PermissionApproveRemediation, ticketClusterID); err != nil {
		return err
	}
	runtime, err := s.runtimeForClusterID(ticketClusterID)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	if runtime.Remediator == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "修复执行器未就绪"})
	}

	operator := currentUsername(c)
	result, execErr := runtime.Remediator.Execute(c.Request().Context(), t.Action, t.Namespace, t.Target, t.Params)
	if execErr != nil {
		t.Status = store.TicketFailed
		t.Result = execErr.Error()
	} else {
		t.Status = store.TicketExecuted
		t.Result = result
		t.VerifyState = store.VerifyPending // 执行成功 → 排期修复效果复检
	}
	t.Approver = operator
	_ = s.store.UpdateTicket(t)
	_ = s.store.AddAudit(ticketClusterID, &store.AuditLog{
		Action:   t.Action,
		Operator: operator,
		Target:   t.Namespace + "/" + t.Target,
		Detail:   t.Reason,
		Result:   t.Result,
	})

	if execErr != nil {
		return c.JSON(http.StatusBadGateway, map[string]any{"ticket": t, "error": execErr.Error()})
	}
	// 异步复检目标是否真正恢复,把结论回写工单并自动解决关联告警
	s.scheduleRemediationVerification(ticketClusterID, t)
	return c.JSON(http.StatusOK, map[string]any{"ticket": t})
}

// handleRejectTicket 拒绝工单(不执行)。
func (s *Server) handleRejectTicket(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的工单 ID"})
	}
	requestClusterID, hasRequestClusterID, err := s.optionalClusterIDFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	t, err := s.store.GetTicketByID(uint(id))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "工单不存在"})
	}
	if t.Status != store.TicketPending {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "工单已处理,当前状态: " + t.Status})
	}
	ticketClusterID, err := requiredTicketClusterID(t)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if hasRequestClusterID && requestClusterID != ticketClusterID {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求集群与工单所属集群不一致"})
	}
	if err := s.requireClusterPermission(c, store.PermissionApproveRemediation, ticketClusterID); err != nil {
		return err
	}
	operator := currentUsername(c)
	t.Status = store.TicketRejected
	t.Approver = operator
	t.Result = "已拒绝,未执行"
	_ = s.store.UpdateTicket(t)
	_ = s.store.AddAudit(ticketClusterID, &store.AuditLog{
		Action:   "reject:" + t.Action,
		Operator: operator,
		Target:   t.Namespace + "/" + t.Target,
		Detail:   t.Reason,
		Result:   "已拒绝",
	})
	return c.JSON(http.StatusOK, map[string]any{"ticket": t})
}

// handleListAudits 返回审计日志。
func (s *Server) handleListAudits(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionViewApprovals); err != nil {
		return err
	}
	clusterID, ok, err := s.optionalClusterIDFromRequest(c)
	if err != nil {
		return c.JSON(runtimeErrorStatus(err), map[string]string{"error": err.Error()})
	}
	operator := c.QueryParam("operator")
	action := c.QueryParam("action")
	var as []store.AuditLog
	if ok {
		if err := s.requireClusterPermission(c, store.PermissionViewApprovals, clusterID); err != nil {
			return err
		}
		as, err = s.store.ListAuditsFiltered(clusterID, operator, action, 100)
	} else {
		as, err = s.store.ListAllAuditsFiltered(operator, action, 100)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	as, err = s.filterAuditsForCurrentUser(c, as)
	if err != nil {
		return err
	}
	responses, err := s.auditLogResponses(as)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"count": len(responses), "audits": responses})
}

func requiredTicketClusterID(t *store.ApprovalTicket) (uint, error) {
	if t == nil || t.ClusterID == nil || *t.ClusterID == 0 {
		return 0, errors.New("工单缺少集群归属,禁止执行")
	}
	return *t.ClusterID, nil
}

func (s *Server) filterTicketsForCurrentUser(c echo.Context, tickets []store.ApprovalTicket) ([]store.ApprovalTicket, error) {
	allowed, all, err := s.authorizedClusterIDSet(c)
	if err != nil {
		return nil, err
	}
	if all {
		return tickets, nil
	}
	filtered := make([]store.ApprovalTicket, 0, len(tickets))
	for _, ticket := range tickets {
		if ticket.ClusterID == nil {
			continue
		}
		if _, ok := allowed[*ticket.ClusterID]; ok {
			filtered = append(filtered, ticket)
		}
	}
	return filtered, nil
}

func filterTicketsByApprover(tickets []store.ApprovalTicket, approver string) []store.ApprovalTicket {
	approver = strings.TrimSpace(approver)
	if approver == "" {
		return tickets
	}
	filtered := make([]store.ApprovalTicket, 0, len(tickets))
	for _, ticket := range tickets {
		if strings.EqualFold(strings.TrimSpace(ticket.Approver), approver) {
			filtered = append(filtered, ticket)
		}
	}
	return filtered
}

func (s *Server) filterAuditsForCurrentUser(c echo.Context, audits []store.AuditLog) ([]store.AuditLog, error) {
	allowed, all, err := s.authorizedClusterIDSet(c)
	if err != nil {
		return nil, err
	}
	if all {
		return audits, nil
	}
	filtered := make([]store.AuditLog, 0, len(audits))
	for _, audit := range audits {
		if audit.ClusterID == nil {
			continue
		}
		if _, ok := allowed[*audit.ClusterID]; ok {
			filtered = append(filtered, audit)
		}
	}
	return filtered, nil
}

func (s *Server) approvalTicketResponses(tickets []store.ApprovalTicket) ([]approvalTicketResponse, error) {
	clusterNames, actorNames, err := s.approvalResponseLookup()
	if err != nil {
		return nil, err
	}
	responses := make([]approvalTicketResponse, 0, len(tickets))
	for _, ticket := range tickets {
		item := approvalTicketResponse{ApprovalTicket: ticket}
		if ticket.ClusterID != nil {
			item.ClusterName = clusterNames[*ticket.ClusterID]
		}
		item.ApproverDisplayName = actorDisplayName(actorNames, ticket.Approver)
		responses = append(responses, item)
	}
	return responses, nil
}

func (s *Server) auditLogResponses(audits []store.AuditLog) ([]auditLogResponse, error) {
	clusterNames, actorNames, err := s.approvalResponseLookup()
	if err != nil {
		return nil, err
	}
	responses := make([]auditLogResponse, 0, len(audits))
	for _, audit := range audits {
		item := auditLogResponse{AuditLog: audit}
		if audit.ClusterID != nil {
			item.ClusterName = clusterNames[*audit.ClusterID]
		}
		item.OperatorDisplayName = actorDisplayName(actorNames, audit.Operator)
		responses = append(responses, item)
	}
	return responses, nil
}

func (s *Server) approvalResponseLookup() (map[uint]string, map[string]string, error) {
	clusters, err := s.store.ListClusters()
	if err != nil {
		return nil, nil, err
	}
	users, err := s.store.ListUsers()
	if err != nil {
		return nil, nil, err
	}
	clusterNames := make(map[uint]string, len(clusters))
	for _, cluster := range clusters {
		clusterNames[cluster.ID] = cluster.Name
	}
	return clusterNames, actorDisplayNameLookup(users), nil
}

func actorDisplayNameLookup(users []store.User) map[string]string {
	names := make(map[string]string, len(users)*2)
	for _, user := range users {
		display := strings.TrimSpace(user.DisplayName)
		if display == "" {
			display = user.Username
		}
		if user.Username != "" {
			names[user.Username] = display
		}
		if display != "" {
			names[display] = display
		}
		if user.Role == store.UserRoleGlobalAdmin {
			if _, exists := names["管理员"]; !exists {
				names["管理员"] = display
			}
		}
	}
	return names
}

func actorDisplayName(names map[string]string, actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return ""
	}
	if display, ok := names[actor]; ok && strings.TrimSpace(display) != "" {
		return display
	}
	return actor
}
