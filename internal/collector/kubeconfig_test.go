package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewRestConfigFromKubeconfigUsesRequestedContext(t *testing.T) {
	kubeconfigPath := writeContextKubeconfig(t)

	cfg, source, err := NewRestConfigFromKubeconfig(kubeconfigPath, "cluster-b")
	if err != nil {
		t.Fatalf("加载指定 context 的 kubeconfig 失败: %v", err)
	}
	if cfg.Host != "https://b.example.invalid" {
		t.Fatalf("未使用指定 context 的 apiserver: got %q", cfg.Host)
	}
	if source != "kubeconfig:"+kubeconfigPath {
		t.Fatalf("来源描述不正确: %q", source)
	}
}

func TestNewRestConfigFromKubeconfigDataUsesRequestedContext(t *testing.T) {
	raw, err := os.ReadFile(writeContextKubeconfig(t))
	if err != nil {
		t.Fatalf("读取测试 kubeconfig 失败: %v", err)
	}

	cfg, err := NewRestConfigFromKubeconfigData(raw, "cluster-b")
	if err != nil {
		t.Fatalf("加载指定 context 的 kubeconfig 内容失败: %v", err)
	}
	if cfg.Host != "https://b.example.invalid" {
		t.Fatalf("未使用指定 context 的 apiserver: got %q", cfg.Host)
	}
}

func TestNewRestConfigAppliesDefaultTimeout(t *testing.T) {
	kubeconfigPath := writeContextKubeconfig(t)

	fileCfg, _, err := NewRestConfigFromKubeconfig(kubeconfigPath, "cluster-a")
	if err != nil {
		t.Fatalf("加载 kubeconfig 文件失败: %v", err)
	}
	if fileCfg.Timeout != defaultKubernetesRequestTimeout {
		t.Fatalf("文件 kubeconfig 应设置默认请求超时: got %s want %s", fileCfg.Timeout, defaultKubernetesRequestTimeout)
	}

	raw, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		t.Fatalf("读取测试 kubeconfig 失败: %v", err)
	}
	dataCfg, err := NewRestConfigFromKubeconfigData(raw, "cluster-a")
	if err != nil {
		t.Fatalf("加载 kubeconfig 内容失败: %v", err)
	}
	if dataCfg.Timeout != defaultKubernetesRequestTimeout {
		t.Fatalf("文本 kubeconfig 应设置默认请求超时: got %s want %s", dataCfg.Timeout, defaultKubernetesRequestTimeout)
	}

	if defaultKubernetesRequestTimeout <= 0 || defaultKubernetesRequestTimeout > 15*time.Second {
		t.Fatalf("默认请求超时应短且明确,避免同步失败时长时间阻塞: %s", defaultKubernetesRequestTimeout)
	}
}

func TestNewClientsetsWithConfigReturnsRestConfig(t *testing.T) {
	kubeconfigPath := writeContextKubeconfig(t)

	_, _, _, cfg, source, err := NewClientsetsWithConfig(kubeconfigPath)
	if err != nil {
		t.Fatalf("构建带 REST 配置的客户端失败: %v", err)
	}
	if cfg == nil || cfg.Host != "https://a.example.invalid" {
		t.Fatalf("默认运行时必须保留原始 REST 配置: %+v", cfg)
	}
	if source != "kubeconfig:"+kubeconfigPath {
		t.Fatalf("来源描述不正确: %q", source)
	}
}

func writeContextKubeconfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	raw := []byte(`
apiVersion: v1
kind: Config
clusters:
- name: cluster-a
  cluster:
    server: https://a.example.invalid
- name: cluster-b
  cluster:
    server: https://b.example.invalid
users:
- name: user-a
  user:
    token: token-a
- name: user-b
  user:
    token: token-b
contexts:
- name: cluster-a
  context:
    cluster: cluster-a
    user: user-a
- name: cluster-b
  context:
    cluster: cluster-b
    user: user-b
current-context: cluster-a
`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("写入测试 kubeconfig 失败: %v", err)
	}
	return path
}
