package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncConfigDefaultsToEnabled(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  http_addr: ":8080"
`)
	if !cfg.Sync.AutoSyncEnabled() {
		t.Fatal("自动同步默认应启用")
	}
	if cfg.Sync.IntervalSeconds != 300 {
		t.Fatalf("默认同步间隔不正确: got %d", cfg.Sync.IntervalSeconds)
	}
	if cfg.Sync.InitialDelaySeconds != 30 {
		t.Fatalf("默认首次同步延迟不正确: got %d", cfg.Sync.InitialDelaySeconds)
	}
}

func TestSyncConfigCanDisableFromYAML(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  http_addr: ":8080"
sync:
  enabled: false
  interval_seconds: 120
  initial_delay_seconds: 10
`)
	if cfg.Sync.AutoSyncEnabled() {
		t.Fatal("YAML 显式配置 enabled=false 时应关闭自动同步")
	}
	if cfg.Sync.IntervalSeconds != 120 {
		t.Fatalf("同步间隔不正确: got %d", cfg.Sync.IntervalSeconds)
	}
	if cfg.Sync.InitialDelaySeconds != 10 {
		t.Fatalf("首次同步延迟不正确: got %d", cfg.Sync.InitialDelaySeconds)
	}
}

func TestSyncConfigEnvOverride(t *testing.T) {
	t.Setenv("AIOPS_SYNC_ENABLED", "false")
	t.Setenv("AIOPS_SYNC_INTERVAL_SECONDS", "60")
	t.Setenv("AIOPS_SYNC_INITIAL_DELAY_SECONDS", "0")
	cfg := loadTestConfig(t, `
server:
  http_addr: ":8080"
sync:
  enabled: true
`)
	if cfg.Sync.AutoSyncEnabled() {
		t.Fatal("环境变量应能关闭自动同步")
	}
	if cfg.Sync.IntervalSeconds != 60 {
		t.Fatalf("环境变量同步间隔未生效: got %d", cfg.Sync.IntervalSeconds)
	}
	if cfg.Sync.InitialDelaySeconds != 0 {
		t.Fatalf("环境变量首次同步延迟未生效: got %d", cfg.Sync.InitialDelaySeconds)
	}
}

func TestRuntimeConfigEnvOverride(t *testing.T) {
	t.Setenv("AIOPS_SERVER_HTTP_ADDR", ":18080")
	t.Setenv("AIOPS_DATABASE_DRIVER", "mysql")
	t.Setenv("AIOPS_DATABASE_DSN", "aiops:secret@tcp(mysql:3306)/aiops?parseTime=True")
	t.Setenv("AIOPS_REDIS_ADDR", "redis:6379")
	t.Setenv("AIOPS_REDIS_PASSWORD", "redis-secret")
	t.Setenv("AIOPS_REDIS_DB", "2")

	cfg := loadTestConfig(t, `
server:
  http_addr: ":8080"
database:
  driver: "sqlite"
  dsn: "aiops.db"
redis:
  addr: "localhost:6379"
  db: 0
`)
	if cfg.Server.HTTPAddr != ":18080" {
		t.Fatalf("HTTP 地址环境变量未生效: got %s", cfg.Server.HTTPAddr)
	}
	if cfg.Database.Driver != "mysql" || cfg.Database.DSN == "aiops.db" {
		t.Fatalf("数据库环境变量未生效: %+v", cfg.Database)
	}
	if cfg.Redis.Addr != "redis:6379" || cfg.Redis.Password != "redis-secret" || cfg.Redis.DB != 2 {
		t.Fatalf("Redis 环境变量未生效: %+v", cfg.Redis)
	}
}

func loadTestConfig(t *testing.T, content string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载测试配置失败: %v", err)
	}
	return cfg
}
