// Package config 负责加载与校验应用配置。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServerConfig HTTP 服务配置。
type ServerConfig struct {
	HTTPAddr string `yaml:"http_addr"`
}

// KubernetesConfig 集群访问配置。
type KubernetesConfig struct {
	Kubeconfig string   `yaml:"kubeconfig"`
	Namespaces []string `yaml:"namespaces"`
}

// PrometheusConfig 指标采集配置。
type PrometheusConfig struct {
	URL string `yaml:"url"`
}

// DatabaseConfig 存储配置。
type DatabaseConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

// RedisConfig 会话记忆存储配置。
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// RemediationConfig 修复能力配置。写操作只会在人工审批后执行。
type RemediationConfig struct {
	Enabled bool `yaml:"enabled"` // 是否启用修复能力(写操作)
}

// SyncConfig 集群资源同步配置。
type SyncConfig struct {
	Enabled             *bool `yaml:"enabled"`               // 是否启用后台自动同步;未配置时默认启用
	IntervalSeconds     int   `yaml:"interval_seconds"`      // 自动同步间隔
	InitialDelaySeconds int   `yaml:"initial_delay_seconds"` // 启动后首次同步延迟
}

// InspectionConfig 定时巡检配置。后台按周期只跑规则巡检并产出告警(不调用 AI,避免烧 token)。
type InspectionConfig struct {
	Enabled             *bool `yaml:"enabled"`               // 是否启用定时巡检;未配置时默认启用
	IntervalSeconds     int   `yaml:"interval_seconds"`      // 巡检间隔
	InitialDelaySeconds int   `yaml:"initial_delay_seconds"` // 启动后首次巡检延迟
}

// NotificationConfig 外部通知配置(钉钉/企业微信群机器人)。新增告警必发、恢复默认也发。
type NotificationConfig struct {
	Enabled        bool                 `yaml:"enabled"`         // 总开关;配置任一 webhook 环境变量时自动置真
	MinSeverity    string               `yaml:"min_severity"`    // 触发通知的最低级别 warning|critical,默认 warning
	NotifyResolved *bool                `yaml:"notify_resolved"` // 是否推送告警恢复;未配置默认 true
	DingTalk       DingTalkNotifyConfig `yaml:"dingtalk"`
	WeCom          WeComNotifyConfig    `yaml:"wecom"`
}

// DingTalkNotifyConfig 钉钉群自定义机器人(可选加签)。
type DingTalkNotifyConfig struct {
	WebhookURL string `yaml:"webhook_url"`
	Secret     string `yaml:"secret"` // 加签密钥(以 SEC 开头),留空则不加签
}

// WeComNotifyConfig 企业微信群机器人。
type WeComNotifyConfig struct {
	WebhookURL string `yaml:"webhook_url"`
}

// ResolvedEnabled 返回是否推送告警恢复通知。未配置默认启用。
func (c NotificationConfig) ResolvedEnabled() bool {
	if c.NotifyResolved == nil {
		return true
	}
	return *c.NotifyResolved
}

// Config 应用总配置。
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Kubernetes   KubernetesConfig   `yaml:"kubernetes"`
	Prometheus   PrometheusConfig   `yaml:"prometheus"`
	Database     DatabaseConfig     `yaml:"database"`
	Redis        RedisConfig        `yaml:"redis"`
	Remediation  RemediationConfig  `yaml:"remediation"`
	Sync         SyncConfig         `yaml:"sync"`
	Inspection   InspectionConfig   `yaml:"inspection"`
	Notification NotificationConfig `yaml:"notification"`
}

// Load 从 YAML 文件加载配置,应用默认值并做基本校验。
func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.Server.HTTPAddr) == "" {
		c.Server.HTTPAddr = ":8080"
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_SERVER_HTTP_ADDR")); raw != "" {
		c.Server.HTTPAddr = raw
	}
	if strings.TrimSpace(c.Database.Driver) == "" {
		c.Database.Driver = "sqlite"
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		c.Database.DSN = "aiops.db"
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_DATABASE_DRIVER")); raw != "" {
		c.Database.Driver = raw
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_DATABASE_DSN")); raw != "" {
		c.Database.DSN = raw
	}
	if strings.TrimSpace(c.Redis.Addr) == "" {
		c.Redis.Addr = "localhost:6379"
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_REDIS_ADDR")); raw != "" {
		c.Redis.Addr = raw
	}
	if raw := os.Getenv("AIOPS_REDIS_PASSWORD"); raw != "" {
		c.Redis.Password = raw
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_REDIS_DB")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			c.Redis.DB = v
		}
	}
	if c.Sync.IntervalSeconds <= 0 {
		c.Sync.IntervalSeconds = 300
	}
	if c.Sync.InitialDelaySeconds < 0 {
		c.Sync.InitialDelaySeconds = 0
	}
	if c.Sync.InitialDelaySeconds == 0 {
		c.Sync.InitialDelaySeconds = 30
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_SYNC_ENABLED")); raw != "" {
		enabled := parseBoolEnv(raw, true)
		c.Sync.Enabled = &enabled
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_SYNC_INTERVAL_SECONDS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			c.Sync.IntervalSeconds = v
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_SYNC_INITIAL_DELAY_SECONDS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			c.Sync.InitialDelaySeconds = v
		}
	}
	if c.Inspection.IntervalSeconds <= 0 {
		c.Inspection.IntervalSeconds = 600
	}
	if c.Inspection.InitialDelaySeconds <= 0 {
		c.Inspection.InitialDelaySeconds = 60
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_INSPECTION_ENABLED")); raw != "" {
		enabled := parseBoolEnv(raw, true)
		c.Inspection.Enabled = &enabled
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_INSPECTION_INTERVAL_SECONDS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			c.Inspection.IntervalSeconds = v
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_INSPECTION_INITIAL_DELAY_SECONDS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			c.Inspection.InitialDelaySeconds = v
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_PROMETHEUS_URL")); raw != "" {
		c.Prometheus.URL = raw
	}
	if strings.TrimSpace(c.Notification.MinSeverity) == "" {
		c.Notification.MinSeverity = "warning"
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_NOTIFY_ENABLED")); raw != "" {
		c.Notification.Enabled = parseBoolEnv(raw, c.Notification.Enabled)
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_NOTIFY_MIN_SEVERITY")); raw != "" {
		c.Notification.MinSeverity = strings.ToLower(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_NOTIFY_RESOLVED")); raw != "" {
		enabled := parseBoolEnv(raw, true)
		c.Notification.NotifyResolved = &enabled
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_DINGTALK_WEBHOOK")); raw != "" {
		c.Notification.DingTalk.WebhookURL = raw
		c.Notification.Enabled = true
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_DINGTALK_SECRET")); raw != "" {
		c.Notification.DingTalk.Secret = raw
	}
	if raw := strings.TrimSpace(os.Getenv("AIOPS_WECOM_WEBHOOK")); raw != "" {
		c.Notification.WeCom.WebhookURL = raw
		c.Notification.Enabled = true
	}
}

// Enabled 返回后台自动同步是否启用。未配置时默认启用。
func (c SyncConfig) AutoSyncEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// InspectionEnabled 返回后台定时巡检是否启用。未配置时默认启用。
func (c InspectionConfig) InspectionEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Server.HTTPAddr) == "" {
		return fmt.Errorf("server.http_addr 不能为空")
	}
	return nil
}

func parseBoolEnv(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return fallback
	}
}
