// Package notify 向外部渠道(钉钉/企业微信群机器人)推送告警通知。
// 单渠道失败只记录日志、不影响其它渠道与主流程;未启用时构造返回 nil,调用点做空安全处理。
package notify

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"aiops/internal/config"
)

// Message 一条通知消息(markdown 正文)。
type Message struct {
	Title string // 标题(钉钉 markdown 需要)
	Text  string // markdown 正文
}

// Channel 单个通知渠道。
type Channel interface {
	Name() string
	Send(ctx context.Context, msg Message) error
}

// Dispatcher 聚合多个通知渠道并附带发送策略(最低级别 / 是否通知恢复)。
type Dispatcher struct {
	channels       []Channel
	logger         *zap.Logger
	minSeverity    string
	notifyResolved bool
}

// NewFromConfig 根据配置构造 Dispatcher;未启用或无有效渠道(缺 webhook)时返回 nil。
func NewFromConfig(cfg config.NotificationConfig, logger *zap.Logger) *Dispatcher {
	if !cfg.Enabled {
		return nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	var channels []Channel
	if url := strings.TrimSpace(cfg.DingTalk.WebhookURL); url != "" {
		channels = append(channels, &dingTalkChannel{webhook: url, secret: strings.TrimSpace(cfg.DingTalk.Secret), client: client})
	}
	if url := strings.TrimSpace(cfg.WeCom.WebhookURL); url != "" {
		channels = append(channels, &weComChannel{webhook: url, client: client})
	}
	if len(channels) == 0 {
		return nil
	}
	minSeverity := strings.ToLower(strings.TrimSpace(cfg.MinSeverity))
	if minSeverity == "" {
		minSeverity = "warning"
	}
	return &Dispatcher{
		channels:       channels,
		logger:         logger,
		minSeverity:    minSeverity,
		notifyResolved: cfg.ResolvedEnabled(),
	}
}

// Dispatch 向所有渠道发送消息;单渠道失败仅记录日志。nil dispatcher 为安全空操作。
func (d *Dispatcher) Dispatch(ctx context.Context, msg Message) {
	if d == nil {
		return
	}
	for _, ch := range d.channels {
		if err := ch.Send(ctx, msg); err != nil && d.logger != nil {
			d.logger.Warn("外部通知发送失败", zap.String("channel", ch.Name()), zap.Error(err))
		}
	}
}

// Enabled 是否存在可用渠道。
func (d *Dispatcher) Enabled() bool { return d != nil && len(d.channels) > 0 }

// AllowSeverity 判断某级别是否达到发送阈值(minSeverity=critical 时过滤掉 warning)。
func (d *Dispatcher) AllowSeverity(severity string) bool {
	if d == nil {
		return false
	}
	if d.minSeverity == "critical" {
		return severity == "critical"
	}
	return severity == "critical" || severity == "warning"
}

// NotifyResolved 是否推送告警恢复通知。
func (d *Dispatcher) NotifyResolved() bool { return d != nil && d.notifyResolved }

// Channels 返回已启用渠道名(供状态展示/日志)。
func (d *Dispatcher) Channels() []string {
	if d == nil {
		return nil
	}
	names := make([]string, 0, len(d.channels))
	for _, ch := range d.channels {
		names = append(names, ch.Name())
	}
	return names
}

// SendAll 向所有渠道发送同一条消息,返回逐渠道错误(nil=成功),用于「发送测试」反馈。
func (d *Dispatcher) SendAll(ctx context.Context, msg Message) map[string]error {
	if d == nil {
		return nil
	}
	results := make(map[string]error, len(d.channels))
	for _, ch := range d.channels {
		results[ch.Name()] = ch.Send(ctx, msg)
	}
	return results
}
