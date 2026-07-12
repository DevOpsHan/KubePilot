package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"aiops/internal/inspect"
	"aiops/internal/prom"
	"aiops/internal/store"
)

// inspectionRuleView 单条规则的展示视图:元信息 + 当前生效设置 + 覆盖状态。
type inspectionRuleView struct {
	inspect.RuleMeta
	Enabled          bool    `json:"enabled"`
	Threshold        float64 `json:"threshold"`
	DefaultEnabled   bool    `json:"default_enabled"`
	DefaultThreshold float64 `json:"default_threshold"`
	Overridden       bool    `json:"overridden"` // 当前层是否存在自定义行(集群层=已覆盖全局;全局层=已改动默认)
}

// EffectiveRuleConfig 三层 merge:内置默认 → 全局行(cluster 0)→ 集群覆盖行。
// 作为 inspect.SetRuleConfigProvider 的提供者(每轮巡检按集群调用,页面修改即热生效)。
func (s *Server) EffectiveRuleConfig(clusterID uint) inspect.RuleConfig {
	cfg := inspect.DefaultRuleConfig()
	if s.store == nil {
		return cfg
	}
	apply := func(layer uint) {
		settings, err := s.store.ListInspectionRuleSettings(layer)
		if err != nil {
			return
		}
		for _, setting := range settings {
			if _, known := cfg[setting.RuleName]; !known {
				continue // 忽略库里的未知规则(版本回退等场景)
			}
			cfg[setting.RuleName] = inspect.RuleSetting{Enabled: setting.Enabled, Threshold: setting.Threshold}
		}
	}
	apply(0)
	if clusterID != 0 {
		apply(clusterID)
	}
	return cfg
}

// effectivePromURL 返回集群实际生效的 Prometheus 地址与来源。
// 优先级:集群独立配置 > 页面配置的全局地址(DB)> config.yaml 兜底。
func (s *Server) effectivePromURL(clusterID uint) (url, source string) {
	if clusterID != 0 && s.store != nil {
		if policy, err := s.store.GetClusterPolicy(clusterID); err == nil {
			if u := strings.TrimSpace(policy.PrometheusURL); u != "" {
				return u, "cluster"
			}
		}
	}
	if s.store != nil {
		if u, err := s.store.GetGlobalPrometheusURL(); err == nil && u != "" {
			return u, "global"
		}
	}
	if u := strings.TrimSpace(s.globalPromURL); u != "" {
		return u, "global"
	}
	return "", "none"
}

// applyPromQuerierAll 对所有集群引擎按各自生效地址重建查询器(全局地址变更时调用)。
func (s *Server) applyPromQuerierAll() {
	if s.runtimeManager == nil {
		return
	}
	for _, runtime := range s.runtimeManager.List() {
		if runtime != nil {
			s.applyPromQuerier(runtime.ClusterID)
		}
	}
}

// applyPromQuerier 按当前生效地址重建集群引擎的 Prometheus 查询器(热生效)。
func (s *Server) applyPromQuerier(clusterID uint) {
	runtime, err := s.runtimeForClusterID(clusterID)
	if err != nil || runtime == nil || runtime.Engine == nil {
		return
	}
	url, _ := s.effectivePromURL(clusterID)
	if url == "" {
		runtime.Engine.SetPromQuerier(nil)
		return
	}
	runtime.Engine.SetPromQuerier(prom.New(url))
}

// InitClusterPromQueriers 启动时按各集群生效地址初始化引擎的 Prometheus 查询器与集群绑定。
func (s *Server) InitClusterPromQueriers() {
	if s.runtimeManager == nil {
		return
	}
	for _, runtime := range s.runtimeManager.List() {
		if runtime == nil || runtime.Engine == nil {
			continue
		}
		runtime.Engine.SetClusterID(runtime.ClusterID)
		s.applyPromQuerier(runtime.ClusterID)
	}
}

// ruleLayerFromRequest 解析查看/修改的配置层:cluster_id 缺省或 0 = 全局层。
func (s *Server) ruleLayerFromRequest(c echo.Context) (uint, error) {
	raw := strings.TrimSpace(c.QueryParam("cluster_id"))
	if raw == "" || raw == "0" {
		return 0, nil
	}
	clusterID, ok, err := s.optionalClusterIDFromRequest(c)
	if err != nil || !ok {
		return 0, fmt.Errorf("无效的 cluster_id")
	}
	return clusterID, nil
}

// handleListInspectionRules 返回指定层的巡检规则视图与 Prometheus 接入状态。
func (s *Server) handleListInspectionRules(c echo.Context) error {
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
	cfg := s.EffectiveRuleConfig(layer)
	defaults := inspect.DefaultRuleConfig()
	// 当前层已有的自定义行(用于 overridden 标记)
	overridden := map[string]bool{}
	if s.store != nil {
		if settings, lerr := s.store.ListInspectionRuleSettings(layer); lerr == nil {
			for _, setting := range settings {
				overridden[setting.RuleName] = true
			}
		}
	}
	rules := make([]inspectionRuleView, 0)
	for _, meta := range inspect.RuleMetas() {
		current := cfg[meta.Name]
		def := defaults[meta.Name]
		rules = append(rules, inspectionRuleView{
			RuleMeta:         meta,
			Enabled:          current.Enabled,
			Threshold:        current.Threshold,
			DefaultEnabled:   def.Enabled,
			DefaultThreshold: def.Threshold,
			Overridden:       overridden[meta.Name],
		})
	}
	promURL, promSource := s.effectivePromURL(layer)
	promStatus := map[string]any{"configured": promURL != "", "source": promSource, "reachable": false}
	if layer != 0 {
		promStatus["cluster_url_configured"] = promSource == "cluster"
	}
	if promURL != "" {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
		promStatus["reachable"] = prom.New(promURL).Ping(ctx) == nil
		cancel()
	}
	return c.JSON(http.StatusOK, map[string]any{"rules": rules, "prometheus": promStatus, "cluster_id": layer})
}

// handleUpdateInspectionRule 更新指定层的规则设置(启停/阈值),下一轮巡检自动生效。
func (s *Server) handleUpdateInspectionRule(c echo.Context) error {
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
	ruleName := c.Param("name")
	defaults := inspect.DefaultRuleConfig()
	def, known := defaults[ruleName]
	if !known {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "未知的巡检规则: " + ruleName})
	}
	var req struct {
		Enabled   *bool    `json:"enabled"`
		Threshold *float64 `json:"threshold"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体格式错误"})
	}
	// 基线 = 该层当前生效值,允许只改一个字段
	current := s.EffectiveRuleConfig(layer)[ruleName]
	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}
	if req.Threshold != nil {
		if def.Threshold > 0 && *req.Threshold <= 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "阈值必须大于 0"})
		}
		current.Threshold = *req.Threshold
	}
	operator := currentUsername(c)
	if err := s.store.SaveInspectionRuleSetting(&store.InspectionRuleSetting{
		RuleName:  ruleName,
		ClusterID: layer,
		Enabled:   current.Enabled,
		Threshold: current.Threshold,
		UpdatedBy: operator,
	}); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	auditClusterID := layer
	if auditClusterID == 0 {
		auditClusterID = s.defaultClusterID
	}
	_ = s.store.AddAudit(auditClusterID, &store.AuditLog{
		Action:   "update_inspection_rule",
		Operator: operator,
		Target:   ruleName,
		Detail:   fmt.Sprintf("layer=%s enabled=%v threshold=%v", ruleLayerText(layer), current.Enabled, current.Threshold),
		Result:   "已保存,下一轮巡检生效",
	})
	return c.JSON(http.StatusOK, map[string]any{
		"rule": map[string]any{"name": ruleName, "cluster_id": layer, "enabled": current.Enabled, "threshold": current.Threshold},
	})
}

// handleResetInspectionRule 删除指定层的规则自定义(集群层→恢复继承全局;全局层→恢复内置默认)。
func (s *Server) handleResetInspectionRule(c echo.Context) error {
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
	ruleName := c.Param("name")
	if _, known := inspect.DefaultRuleConfig()[ruleName]; !known {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "未知的巡检规则: " + ruleName})
	}
	if err := s.store.DeleteInspectionRuleSetting(ruleName, layer); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	auditClusterID := layer
	if auditClusterID == 0 {
		auditClusterID = s.defaultClusterID
	}
	result := "已恢复继承全局配置"
	if layer == 0 {
		result = "已恢复内置默认"
	}
	_ = s.store.AddAudit(auditClusterID, &store.AuditLog{
		Action:   "reset_inspection_rule",
		Operator: currentUsername(c),
		Target:   ruleName,
		Detail:   "layer=" + ruleLayerText(layer),
		Result:   result,
	})
	return c.JSON(http.StatusOK, map[string]string{"message": result})
}

// handleUpdateClusterPrometheus 配置 Prometheus 地址(热生效)。
// cluster_id=0/缺省 → 页面全局地址(所有未单独配置的集群回退到此);cluster_id>0 → 集群独立地址。
func (s *Server) handleUpdateClusterPrometheus(c echo.Context) error {
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
	var req struct {
		URL string `json:"url"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体格式错误"})
	}
	url := strings.TrimSpace(req.URL)
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Prometheus 地址必须以 http:// 或 https:// 开头"})
	}
	operator := currentUsername(c)
	var result string
	if layer == 0 {
		// 全局地址:入库并对所有集群引擎热生效
		if err := s.store.SaveGlobalPrometheusURL(url, operator); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		s.applyPromQuerierAll()
		result = "已配置全局 Prometheus 并对所有集群热生效"
		if url == "" {
			result = "已清除页面全局配置,回退 config.yaml"
		}
	} else {
		if err := s.store.UpdateClusterPrometheusURL(layer, url); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		s.applyPromQuerier(layer)
		result = "已配置集群独立 Prometheus 并热生效"
		if url == "" {
			result = "已清除,回退全局 Prometheus 配置"
		}
	}
	auditClusterID := layer
	if auditClusterID == 0 {
		auditClusterID = s.defaultClusterID
	}
	_ = s.store.AddAudit(auditClusterID, &store.AuditLog{
		Action:   "update_prometheus_url",
		Operator: operator,
		Target:   ruleLayerText(layer),
		Detail:   url,
		Result:   result,
	})
	effURL, source := s.effectivePromURL(layer)
	return c.JSON(http.StatusOK, map[string]any{"message": result, "source": source, "configured": effURL != ""})
}

// ruleLayerText 配置层的可读描述。
func ruleLayerText(layer uint) string {
	if layer == 0 {
		return "全局"
	}
	return fmt.Sprintf("集群 %d", layer)
}
