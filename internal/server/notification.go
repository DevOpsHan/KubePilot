package server

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"aiops/internal/config"
	"aiops/internal/notify"
	"aiops/internal/security"
	"aiops/internal/store"
)

// encPrefix 标记该值为 AES-GCM 密文(未配置 AIOPS_SECRET_KEY 时按明文存,保持向后兼容)。
const encPrefix = "enc:"

// sealSecret 尽力加密:有密钥则加密并带 enc: 前缀,否则原文返回。
func sealSecret(plain string) string {
	if plain == "" {
		return ""
	}
	key := os.Getenv("AIOPS_SECRET_KEY")
	if key == "" {
		return plain
	}
	cipherText, err := security.EncryptString(plain, key)
	if err != nil {
		return plain
	}
	return encPrefix + cipherText
}

// openSecret 解开 sealSecret 的值;解密失败返回空串(视为未配置,避免把密文当明文用)。
func openSecret(stored string) string {
	if !strings.HasPrefix(stored, encPrefix) {
		return stored
	}
	plain, err := security.DecryptString(strings.TrimPrefix(stored, encPrefix), os.Getenv("AIOPS_SECRET_KEY"))
	if err != nil {
		return ""
	}
	return plain
}

// ConfigureNotifications 记录文件配置兜底并按「DB 优先、文件兜底」构建当前生效的通知分发器。
// 由 app 装配时调用;之后每次保存设置都会热替换。
func (s *Server) ConfigureNotifications(cfg config.NotificationConfig) {
	s.fallbackNotify = cfg
	if err := s.syncNotifierFromStore(); err != nil && s.logger != nil {
		s.logger.Warn("加载数据库通知设置失败,使用文件配置", zap.Error(err))
	}
}

// syncNotifierFromStore 重建通知分发器:DB 有设置用 DB,否则用文件配置。
func (s *Server) syncNotifierFromStore() error {
	cfg := s.fallbackNotify
	if s.store != nil {
		setting, err := s.store.GetNotificationSetting()
		if err != nil {
			return err
		}
		if setting != nil {
			cfg = settingToNotifyConfig(setting)
		}
	}
	s.SetNotifier(notify.NewFromConfig(cfg, s.logger))
	return nil
}

// settingToNotifyConfig 把落库设置(密文)转换为 notify 可用的明文配置。
func settingToNotifyConfig(setting *store.NotificationSetting) config.NotificationConfig {
	resolved := setting.NotifyResolved
	return config.NotificationConfig{
		Enabled:        setting.Enabled,
		MinSeverity:    setting.MinSeverity,
		NotifyResolved: &resolved,
		DingTalk: config.DingTalkNotifyConfig{
			WebhookURL: openSecret(setting.DingTalkWebhook),
			Secret:     openSecret(setting.DingTalkSecret),
		},
		WeCom: config.WeComNotifyConfig{
			WebhookURL: openSecret(setting.WeComWebhook),
		},
	}
}

// currentNotifySnapshot 返回「当前展示用」的设置快照:DB 有记录用 DB,否则用文件配置。
// 返回值中的 webhook/secret 均为明文(仅用于脱敏展示,不直接下发)。
func (s *Server) currentNotifySnapshot() (cfg config.NotificationConfig, source string, err error) {
	if s.store != nil {
		setting, gErr := s.store.GetNotificationSetting()
		if gErr != nil {
			return cfg, "", gErr
		}
		if setting != nil {
			return settingToNotifyConfig(setting), "database", nil
		}
	}
	return s.fallbackNotify, "file", nil
}

// webhookHint 返回脱敏提示(尾 8 字符),空值返回空串。
func webhookHint(url string) string {
	if url == "" {
		return ""
	}
	if len(url) <= 8 {
		return "…" + url
	}
	return "…" + url[len(url)-8:]
}

// handleGetNotificationSettings 返回脱敏后的通知设置(不含任何明文 webhook/secret)。
func (s *Server) handleGetNotificationSettings(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageNotifications); err != nil {
		return err
	}
	cfg, source, err := s.currentNotifySnapshot()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"enabled":                     cfg.Enabled,
		"min_severity":                cfg.MinSeverity,
		"notify_resolved":             cfg.ResolvedEnabled(),
		"dingtalk_webhook_configured": cfg.DingTalk.WebhookURL != "",
		"dingtalk_webhook_hint":       webhookHint(cfg.DingTalk.WebhookURL),
		"dingtalk_secret_configured":  cfg.DingTalk.Secret != "",
		"wecom_webhook_configured":    cfg.WeCom.WebhookURL != "",
		"wecom_webhook_hint":          webhookHint(cfg.WeCom.WebhookURL),
		"active_channels":             s.currentNotifier().Channels(),
		"source":                      source,
	})
}

// handleSaveNotificationSettings 保存通知设置并热生效。
// webhook/secret 字段语义:字段缺省(null)=保持不变;空串=清除;非空=更新。
func (s *Server) handleSaveNotificationSettings(c echo.Context) error {
	if s.store == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "数据库未就绪"})
	}
	if err := requirePermission(c, store.PermissionManageNotifications); err != nil {
		return err
	}
	var req struct {
		Enabled         *bool   `json:"enabled"`
		MinSeverity     *string `json:"min_severity"`
		NotifyResolved  *bool   `json:"notify_resolved"`
		DingTalkWebhook *string `json:"dingtalk_webhook"`
		DingTalkSecret  *string `json:"dingtalk_secret"`
		WeComWebhook    *string `json:"wecom_webhook"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "请求体格式错误"})
	}
	if req.MinSeverity != nil {
		v := strings.ToLower(strings.TrimSpace(*req.MinSeverity))
		if v != "warning" && v != "critical" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "min_severity 只能是 warning 或 critical"})
		}
		*req.MinSeverity = v
	}

	// 基线:DB 已有记录用 DB;首次保存以文件配置为基线(局部更新不丢已有 webhook)
	setting, err := s.store.GetNotificationSetting()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if setting == nil {
		fb := s.fallbackNotify
		setting = &store.NotificationSetting{
			Enabled:         fb.Enabled,
			MinSeverity:     fb.MinSeverity,
			NotifyResolved:  fb.ResolvedEnabled(),
			DingTalkWebhook: sealSecret(fb.DingTalk.WebhookURL),
			DingTalkSecret:  sealSecret(fb.DingTalk.Secret),
			WeComWebhook:    sealSecret(fb.WeCom.WebhookURL),
		}
	}
	if setting.MinSeverity == "" {
		setting.MinSeverity = "warning"
	}
	if req.Enabled != nil {
		setting.Enabled = *req.Enabled
	}
	if req.MinSeverity != nil {
		setting.MinSeverity = *req.MinSeverity
	}
	if req.NotifyResolved != nil {
		setting.NotifyResolved = *req.NotifyResolved
	}
	if req.DingTalkWebhook != nil {
		setting.DingTalkWebhook = sealSecret(strings.TrimSpace(*req.DingTalkWebhook))
	}
	if req.DingTalkSecret != nil {
		setting.DingTalkSecret = sealSecret(strings.TrimSpace(*req.DingTalkSecret))
	}
	if req.WeComWebhook != nil {
		setting.WeComWebhook = sealSecret(strings.TrimSpace(*req.WeComWebhook))
	}
	setting.UpdatedBy = currentUsername(c)
	if err := s.store.SaveNotificationSetting(setting); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if err := s.syncNotifierFromStore(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "设置已保存但生效失败: " + err.Error()})
	}
	_ = s.store.AddAudit(s.defaultClusterID, &store.AuditLog{
		Action:   "update_notification_settings",
		Operator: setting.UpdatedBy,
		Target:   "notification_settings",
		Detail:   "min_severity=" + setting.MinSeverity,
		Result:   "已保存并热生效",
	})
	return s.handleGetNotificationSettings(c)
}

// handleTestNotification 向当前生效渠道发送一条测试消息,返回逐渠道成败。
func (s *Server) handleTestNotification(c echo.Context) error {
	if err := requirePermission(c, store.PermissionManageNotifications); err != nil {
		return err
	}
	notifier := s.currentNotifier()
	if notifier == nil || !notifier.Enabled() {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "通知未启用或未配置任何渠道"})
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
	defer cancel()
	results := notifier.SendAll(ctx, notify.Message{
		Title: "🔔 AIOps 测试通知",
		Text: "### 🔔 AIOps 测试通知\n\n- **来源**:通知设置「发送测试」\n- **操作人**:" + currentUsername(c) +
			"\n- **时间**:" + time.Now().Format("2006-01-02 15:04:05") + "\n\n收到本条说明通知链路配置正确。",
	})
	channelResults := make(map[string]string, len(results))
	allOK := true
	for name, sendErr := range results {
		if sendErr != nil {
			channelResults[name] = sendErr.Error()
			allOK = false
		} else {
			channelResults[name] = "ok"
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": allOK, "channels": channelResults})
}
