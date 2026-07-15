package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"aiops/internal/clusterruntime"
	"aiops/internal/config"
	"aiops/internal/store"
)

func TestAppDoesNotLogDatabaseDSN(t *testing.T) {
	sensitiveMarkers := []string{"database-user-canary", "database-password-canary"}
	dsn := fmt.Sprintf("file:database-log-test?mode=memory&cache=shared&username=%s&password=%s", sensitiveMarkers[0], sensitiveMarkers[1])
	redisServer := miniredis.RunT(t)
	cfg := &config.Config{
		Kubernetes: config.KubernetesConfig{Kubeconfig: filepath.Join(t.TempDir(), "missing-kubeconfig")},
		Database:   config.DatabaseConfig{Driver: "sqlite", DSN: dsn},
		Redis:      config.RedisConfig{Addr: redisServer.Addr()},
	}

	core, observedLogs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	application, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	t.Cleanup(func() {
		if application.Store != nil {
			_ = application.Store.Close()
		}
	})

	databaseReadyLogs := observedLogs.FilterMessage("数据库就绪").All()
	if len(databaseReadyLogs) != 1 {
		t.Fatalf("数据库就绪日志数量不正确: got %d want 1", len(databaseReadyLogs))
	}
	fields := databaseReadyLogs[0].ContextMap()
	if fields["driver"] != "sqlite" {
		t.Fatalf("数据库就绪日志应保留驱动信息: got %v", fields["driver"])
	}
	if _, ok := fields["dsn"]; ok {
		t.Fatal("数据库就绪日志不应包含 dsn 字段")
	}
	for _, entry := range observedLogs.AllUntimed() {
		for _, marker := range sensitiveMarkers {
			if strings.Contains(entry.Message, marker) {
				t.Fatal("应用日志消息不应包含数据库 DSN 中的敏感信息")
			}
		}
		for field, value := range entry.ContextMap() {
			for _, marker := range sensitiveMarkers {
				if strings.Contains(fmt.Sprint(value), marker) {
					t.Fatalf("应用日志字段 %q 不应包含数据库 DSN 中的敏感信息", field)
				}
			}
		}
	}
}

func TestEnsureDefaultClusterCreatesRecordFromKubeconfig(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := &config.Config{
		Kubernetes: config.KubernetesConfig{Kubeconfig: "C:/Users/test/.kube/config"},
	}
	cluster, err := ensureDefaultCluster(st, cfg, "kubeconfig:C:/Users/test/.kube/config")
	if err != nil {
		t.Fatalf("确保默认集群失败: %v", err)
	}
	if cluster.Slug != store.DefaultClusterSlug {
		t.Fatalf("默认集群标识不正确: %q", cluster.Slug)
	}
	if cluster.Mode != store.ClusterModeKubeconfig {
		t.Fatalf("默认集群模式不正确: got %q want %q", cluster.Mode, store.ClusterModeKubeconfig)
	}

	credential, err := st.GetClusterCredential(cluster.ID)
	if err != nil {
		t.Fatalf("查询默认集群凭据失败: %v", err)
	}
	if credential.KubeconfigPath != "C:/Users/test/.kube/config" {
		t.Fatalf("默认集群 kubeconfig 路径不正确: %q", credential.KubeconfigPath)
	}
}

func TestAppDoesNotLoadModelFromConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	raw := []byte(`
server:
  http_addr: ":0"
kubernetes:
  kubeconfig: "` + filepath.ToSlash(filepath.Join(dir, "missing-kubeconfig")) + `"
database:
  driver: "sqlite"
  dsn: "` + filepath.ToSlash(filepath.Join(dir, "aiops.db")) + `"
redis:
  addr: "127.0.0.1:1"
model:
  provider: "deepseek"
  api_key: "legacy-config-key"
  base_url: "https://api.deepseek.com"
  model_name: "legacy-config-model"
`)
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("加载测试配置失败: %v", err)
	}
	application, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("创建应用失败: %v", err)
	}
	t.Cleanup(func() {
		if application.Store != nil {
			_ = application.Store.Close()
		}
	})

	status := application.Server.CurrentModelRuntimeStatus()
	if status.Ready || status.Source != "" {
		t.Fatalf("应用不应从 config.yaml 加载模型: %+v", status)
	}
}

func TestRestoreStoredClusterRuntimesSkipsDisabledClusters(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	enabled := false
	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name:           "禁用集群",
		Slug:           "disabled-cluster",
		Mode:           store.ClusterModeKubeconfig,
		Status:         store.ClusterStatusUnknown,
		Enabled:        &enabled,
		KubeconfigPath: filepath.Join(t.TempDir(), "missing-kubeconfig"),
	})
	if err != nil {
		t.Fatalf("创建禁用集群失败: %v", err)
	}

	manager := clusterruntime.NewManager()
	restoreStoredClusterRuntimes(st, manager, 0, zap.NewNop())

	if _, err := manager.Get(cluster.ID); err == nil {
		t.Fatal("禁用集群不应恢复运行时")
	}
	updated, err := st.GetCluster(cluster.ID)
	if err != nil {
		t.Fatalf("查询禁用集群失败: %v", err)
	}
	if updated.Status != store.ClusterStatusUnknown {
		t.Fatalf("禁用集群状态不应因恢复流程被改写: got %q", updated.Status)
	}
}
