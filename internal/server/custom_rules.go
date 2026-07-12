package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"aiops/internal/inspect"
	"aiops/internal/prom"
	"aiops/internal/store"
)

// customRuleOperators 允许的比较运算符白名单。
var customRuleOperators = map[string]bool{">": true, ">=": true, "<": true, "<=": true, "==": true}

// EffectiveCustomRules 返回对某集群生效的自定义指标规则规格(全局 + 该集群、仅启用)。
// 作为 inspect.SetCustomRuleProvider 的提供者,每轮巡检按集群调用,页面增删改即热生效。
func (s *Server) EffectiveCustomRules(clusterID uint) []inspect.CustomMetricRuleSpec {
	if s.store == nil {
		return nil
	}
	rules, err := s.store.ListActiveCustomMetricRules(clusterID)
	if err != nil {
		return nil
	}
	specs := make([]inspect.CustomMetricRuleSpec, 0, len(rules))
	for _, r := range rules {
		specs = append(specs, inspect.CustomMetricRuleSpec{
			Name:        r.Name,
			PromQL:      r.PromQL,
			Operator:    r.Operator,
			Threshold:   r.Threshold,
			Severity:    r.Severity,
			ObjectLabel: r.ObjectLabel,
		})
	}
	return specs
}

// customRuleView 自定义规则展示视图(附是否为继承的全局规则)。
type customRuleView struct {
	store.CustomMetricRule
	Inherited bool `json:"inherited"` // 在集群层查看时,标记该规则来自全局
}

// handleListCustomRules 列出某层的自定义规则(全局层=全局;集群层=全局[标 inherited]+本集群)。
func (s *Server) handleListCustomRules(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionViewAlerts); err != nil {
		return err
	}
	layer, err := s.ruleLayerFromRequest(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if layer != 0 {
		if err := s.requireClusterPermission(c, store.PermissionViewAlerts, layer); err != nil {
			return err
		}
	}
	views := make([]customRuleView, 0)
	if layer != 0 {
		globals, gErr := s.store.ListCustomMetricRules(0)
		if gErr != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": gErr.Error()})
		}
		for _, r := range globals {
			views = append(views, customRuleView{CustomMetricRule: r, Inherited: true})
		}
	}
	own, err := s.store.ListCustomMetricRules(layer)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	for _, r := range own {
		views = append(views, customRuleView{CustomMetricRule: r})
	}
	return c.JSON(http.StatusOK, map[string]any{"rules": views, "cluster_id": layer})
}

// customRuleInput 创建/更新自定义规则的请求体。
type customRuleInput struct {
	Name        string  `json:"name"`
	PromQL      string  `json:"promql"`
	Operator    string  `json:"operator"`
	Threshold   float64 `json:"threshold"`
	Severity    string  `json:"severity"`
	ObjectLabel string  `json:"object_label"`
	Description string  `json:"description"`
	Enabled     *bool   `json:"enabled"`
}

// validateCustomRuleInput 归一化并校验输入;返回清洗后的字段或错误信息。
func validateCustomRuleInput(in *customRuleInput) (name, promql, operator, severity, objectLabel string, errMsg string) {
	name = strings.TrimSpace(in.Name)
	promql = strings.TrimSpace(in.PromQL)
	operator = strings.TrimSpace(in.Operator)
	severity = strings.ToLower(strings.TrimSpace(in.Severity))
	objectLabel = strings.TrimSpace(in.ObjectLabel)
	if name == "" {
		return "", "", "", "", "", "规则名不能为空"
	}
	if _, isBuiltin := inspect.DefaultRuleConfig()[name]; isBuiltin {
		return "", "", "", "", "", "规则名不能与内置规则重名"
	}
	if promql == "" {
		return "", "", "", "", "", "PromQL 表达式不能为空"
	}
	if !customRuleOperators[operator] {
		return "", "", "", "", "", "运算符只能是 > >= < <= == 之一"
	}
	if severity != "critical" && severity != "warning" {
		return "", "", "", "", "", "级别只能是 critical 或 warning"
	}
	return name, promql, operator, severity, objectLabel, ""
}

// handleCreateCustomRule 创建自定义指标规则。
func (s *Server) handleCreateCustomRule(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionManageInspection); err != nil {
		return err
	}
	layer, err := s.ruleLayerFromRequest(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	var in customRuleInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体格式错误"})
	}
	name, promql, operator, severity, objectLabel, errMsg := validateCustomRuleInput(&in)
	if errMsg != "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": errMsg})
	}
	if exists, _ := s.store.CustomMetricRuleNameExists(layer, name, 0); exists {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "该层已存在同名规则"})
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	rule := &store.CustomMetricRule{
		ClusterID:   layer,
		Name:        name,
		PromQL:      promql,
		Operator:    operator,
		Threshold:   in.Threshold,
		Severity:    severity,
		ObjectLabel: objectLabel,
		Description: strings.TrimSpace(in.Description),
		Enabled:     enabled,
		CreatedBy:   currentUsername(c),
	}
	if err := s.store.CreateCustomMetricRule(rule); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	s.auditCustomRule(c, layer, "create_custom_rule", rule.Name, "已创建自定义指标规则,下一轮巡检生效")
	return c.JSON(http.StatusOK, map[string]any{"rule": rule})
}

// handleUpdateCustomRule 更新自定义指标规则。
func (s *Server) handleUpdateCustomRule(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionManageInspection); err != nil {
		return err
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的规则 ID"})
	}
	rule, err := s.store.GetCustomMetricRuleByID(uint(id))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "规则不存在"})
	}
	var in customRuleInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体格式错误"})
	}
	name, promql, operator, severity, objectLabel, errMsg := validateCustomRuleInput(&in)
	if errMsg != "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": errMsg})
	}
	if exists, _ := s.store.CustomMetricRuleNameExists(rule.ClusterID, name, rule.ID); exists {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "该层已存在同名规则"})
	}
	rule.Name = name
	rule.PromQL = promql
	rule.Operator = operator
	rule.Threshold = in.Threshold
	rule.Severity = severity
	rule.ObjectLabel = objectLabel
	rule.Description = strings.TrimSpace(in.Description)
	if in.Enabled != nil {
		rule.Enabled = *in.Enabled
	}
	if err := s.store.UpdateCustomMetricRule(rule); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	s.auditCustomRule(c, rule.ClusterID, "update_custom_rule", rule.Name, "已更新,下一轮巡检生效")
	return c.JSON(http.StatusOK, map[string]any{"rule": rule})
}

// handleDeleteCustomRule 删除自定义指标规则。
func (s *Server) handleDeleteCustomRule(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionManageInspection); err != nil {
		return err
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "无效的规则 ID"})
	}
	rule, err := s.store.GetCustomMetricRuleByID(uint(id))
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "规则不存在"})
	}
	if err := s.store.DeleteCustomMetricRule(rule.ID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	s.auditCustomRule(c, rule.ClusterID, "delete_custom_rule", rule.Name, "已删除")
	return c.JSON(http.StatusOK, map[string]string{"message": "已删除自定义规则"})
}

// handleTestCustomRule 按该层生效 Prometheus 干跑一次 PromQL,返回样本与按阈值命中数,供保存前校验。
func (s *Server) handleTestCustomRule(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageInspection); err != nil {
		return err
	}
	layer, err := s.ruleLayerFromRequest(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	var in customRuleInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体格式错误"})
	}
	promql := strings.TrimSpace(in.PromQL)
	if promql == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "PromQL 表达式不能为空"})
	}
	promURL, _ := s.effectivePromURL(layer)
	if promURL == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "该层未接入 Prometheus,无法测试"})
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 8*time.Second)
	defer cancel()
	samples, qErr := prom.New(promURL).Query(ctx, promql)
	if qErr != nil {
		return c.JSON(http.StatusOK, map[string]any{"ok": false, "error": qErr.Error()})
	}
	operator := strings.TrimSpace(in.Operator)
	label := strings.TrimSpace(in.ObjectLabel)
	if label == "" {
		label = "instance"
	}
	matched := 0
	preview := make([]map[string]any, 0, 5)
	for i, sm := range samples {
		hit := customRuleOperators[operator] && compareSample(sm.Value, operator, in.Threshold)
		if hit {
			matched++
		}
		if i < 5 {
			name := sm.Labels[label]
			if name == "" {
				name = sm.Labels["instance"]
			}
			preview = append(preview, map[string]any{"name": name, "value": sm.Value, "hit": hit})
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"ok": true, "sample_count": len(samples), "matched": matched, "preview": preview,
	})
}

// compareSample 复用运算符比较(与巡检引擎一致的语义)。
func compareSample(value float64, operator string, threshold float64) bool {
	switch operator {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	default:
		return false
	}
}

// auditCustomRule 写自定义规则相关审计(全局层挂默认集群)。
func (s *Server) auditCustomRule(c echo.Context, layer uint, action, target, result string) {
	auditClusterID := layer
	if auditClusterID == 0 {
		auditClusterID = s.defaultClusterID
	}
	_ = s.store.AddAudit(auditClusterID, &store.AuditLog{
		Action:   action,
		Operator: currentUsername(c),
		Target:   target,
		Detail:   "layer=" + ruleLayerText(layer),
		Result:   result,
	})
}
