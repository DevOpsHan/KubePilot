// Package collector 从 Kubernetes 集群只读采集资源状态。
// 阶段一仅做只读操作(get/list),绝不修改集群。
package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Collector 持有 K8S 客户端,提供只读采集能力。
type Collector struct {
	clientset     kubernetes.Interface
	metricsClient metricsclientset.Interface
	dynamicClient dynamic.Interface
}

const (
	defaultKubernetesRequestTimeout      = 8 * time.Second
	defaultKubernetesConnectivityTimeout = 3 * time.Second
)

// New 用给定客户端创建采集器。
func New(cs kubernetes.Interface) *Collector {
	return &Collector{clientset: cs}
}

// NewWithMetrics 用给定客户端创建采集器,包含 metrics-server 指标能力。
func NewWithMetrics(cs kubernetes.Interface, metricsClient metricsclientset.Interface) *Collector {
	return &Collector{clientset: cs, metricsClient: metricsClient}
}

// NewWithMetricsAndDynamic 用给定客户端创建采集器,包含 metrics 与动态资源能力。
func NewWithMetricsAndDynamic(cs kubernetes.Interface, metricsClient metricsclientset.Interface, dynamicClient dynamic.Interface) *Collector {
	return &Collector{clientset: cs, metricsClient: metricsClient, dynamicClient: dynamicClient}
}

// Ping 对 Kubernetes API Server 执行一次轻量连通性检查。
func (c *Collector) Ping(ctx context.Context) error {
	if c == nil || c.clientset == nil {
		return fmt.Errorf("K8S 客户端未就绪")
	}
	checkCtx, cancel := context.WithTimeout(ctx, defaultKubernetesConnectivityTimeout)
	defer cancel()

	discoveryClient := c.clientset.Discovery()
	if discoveryClient != nil && discoveryClient.RESTClient() != nil {
		return discoveryClient.RESTClient().Get().AbsPath("/version").Do(checkCtx).Error()
	}
	// fake client 没有 RESTClient，使用最小 List 保持测试和替代实现可用。
	_, err := c.clientset.CoreV1().Namespaces().List(checkCtx, metav1.ListOptions{Limit: 1})
	return err
}

// NewClientset 构建 K8S 客户端:优先 in-cluster(集群内 ServiceAccount),
// 否则使用 kubeconfig(指定路径,或默认 ~/.kube/config)。
// 返回:客户端、来源描述、错误。
func NewClientset(kubeconfigPath string) (kubernetes.Interface, string, error) {
	cfg, source, err := NewRestConfig(kubeconfigPath)
	if err != nil {
		return nil, "", err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, "", err
	}
	return cs, source, nil
}

// NewClientsets 构建 K8S 核心客户端与 metrics 客户端。
func NewClientsets(kubeconfigPath string) (kubernetes.Interface, metricsclientset.Interface, dynamic.Interface, string, error) {
	cfg, source, err := NewRestConfig(kubeconfigPath)
	if err != nil {
		return nil, nil, nil, "", err
	}
	cs, metricsClient, dynamicClient, err := newClientsetsFromConfig(cfg)
	if err != nil {
		return nil, nil, nil, "", err
	}
	return cs, metricsClient, dynamicClient, source, nil
}

// NewClientsetsFromKubeconfigFile 从指定 kubeconfig 文件构建客户端。
// 该函数用于已接入集群恢复,必须使用传入的 kubeconfig,不能回落到 in-cluster。
func NewClientsetsFromKubeconfigFile(kubeconfigPath, contextName string) (kubernetes.Interface, metricsclientset.Interface, dynamic.Interface, string, error) {
	cfg, source, err := NewRestConfigFromKubeconfig(kubeconfigPath, contextName)
	if err != nil {
		return nil, nil, nil, "", err
	}
	cs, metricsClient, dynamicClient, err := newClientsetsFromConfig(cfg)
	if err != nil {
		return nil, nil, nil, "", err
	}
	return cs, metricsClient, dynamicClient, source, nil
}

func newClientsetsFromConfig(cfg *rest.Config) (kubernetes.Interface, metricsclientset.Interface, dynamic.Interface, error) {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	metricsClient, err := metricsclientset.NewForConfig(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return cs, metricsClient, dynamicClient, nil
}

// NewClientsetsFromKubeconfigData 从 kubeconfig 文本构建 K8S 核心客户端、metrics 客户端与动态客户端。
func NewClientsetsFromKubeconfigData(kubeconfigData []byte) (kubernetes.Interface, metricsclientset.Interface, dynamic.Interface, error) {
	return NewClientsetsFromKubeconfigDataWithContext(kubeconfigData, "")
}

// NewClientsetsFromKubeconfigDataWithContext 从 kubeconfig 文本和指定 context 构建客户端。
func NewClientsetsFromKubeconfigDataWithContext(kubeconfigData []byte, contextName string) (kubernetes.Interface, metricsclientset.Interface, dynamic.Interface, error) {
	cfg, err := NewRestConfigFromKubeconfigData(kubeconfigData, contextName)
	if err != nil {
		return nil, nil, nil, err
	}
	return newClientsetsFromConfig(cfg)
}

// NewRestConfig 构建 K8S REST 配置:优先 in-cluster,否则 kubeconfig。
func NewRestConfig(kubeconfigPath string) (*rest.Config, string, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		applyDefaultRestConfig(cfg)
		return cfg, "in-cluster", nil
	}
	return NewRestConfigFromKubeconfig(kubeconfigPath, "")
}

// NewRestConfigFromKubeconfig 只从 kubeconfig 文件构建 REST 配置,支持指定 context。
func NewRestConfigFromKubeconfig(kubeconfigPath, contextName string) (*rest.Config, string, error) {
	if kubeconfigPath == "" {
		home, _ := os.UserHomeDir()
		kubeconfigPath = filepath.Join(home, ".kube", "config")
	}
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: strings.TrimSpace(contextName)}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("加载 kubeconfig 失败: %w", err)
	}
	applyDefaultRestConfig(cfg)
	return cfg, "kubeconfig:" + kubeconfigPath, nil
}

// NewRestConfigFromKubeconfigData 只从 kubeconfig 文本构建 REST 配置,支持指定 context。
func NewRestConfigFromKubeconfigData(kubeconfigData []byte, contextName string) (*rest.Config, error) {
	rawConfig, err := clientcmd.Load(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("解析 kubeconfig 失败: %w", err)
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: strings.TrimSpace(contextName)}
	cfg, err := clientcmd.NewDefaultClientConfig(*rawConfig, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("加载 kubeconfig 失败: %w", err)
	}
	applyDefaultRestConfig(cfg)
	return cfg, nil
}

func applyDefaultRestConfig(cfg *rest.Config) {
	if cfg == nil {
		return
	}
	// API Server 不可达时，client-go 默认请求可能长时间阻塞。
	// 平台同步是周期性只读采集，失败应快速落库并显示明确错误。
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultKubernetesRequestTimeout
	}
}

// PodInfo 精简的 Pod 状态信息。
type PodInfo struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Ready     string `json:"ready"` // 形如 "1/1"
	Restarts  int32  `json:"restarts"`
	Node      string `json:"node"`
	Reason    string `json:"reason,omitempty"` // 异常原因,如 CrashLoopBackOff;正常则空
}

// NodeInfo 精简的节点信息。
type NodeInfo struct {
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Version string `json:"version"`
}

// NodeMetricInfo 表示节点当前 CPU/内存实时使用情况。
type NodeMetricInfo struct {
	Name                   string `json:"name"`
	CPUUsageMilli          int64  `json:"cpu_usage_milli"`
	CPUAllocatableMilli    int64  `json:"cpu_allocatable_milli"`
	CPUUsagePercent        int64  `json:"cpu_usage_percent"`
	MemoryUsageBytes       int64  `json:"memory_usage_bytes"`
	MemoryAllocatableBytes int64  `json:"memory_allocatable_bytes"`
	MemoryUsagePercent     int64  `json:"memory_usage_percent"`
}

// ClusterOverview 集群概览统计。
type ClusterOverview struct {
	Nodes       int            `json:"nodes"`
	NodesReady  int            `json:"nodes_ready"`
	Pods        int            `json:"pods"`
	PodsByPhase map[string]int `json:"pods_by_phase"`
	Namespaces  int            `json:"namespaces"`
}

// ResourceSyncSnapshot 表示一次资源同步采样结果。
type ResourceSyncSnapshot struct {
	APIVersion           string `json:"api_version"`
	Kind                 string `json:"kind"`
	ResourceName         string `json:"resource_name"`
	Count                int    `json:"count"`
	Version              string `json:"version,omitempty"`
	ResourceVersion      string `json:"resource_version,omitempty"`
	IncrementalSupported bool   `json:"incremental_supported"`
	Supported            bool   `json:"supported"`
	Error                string `json:"error,omitempty"`
}

// ResourceWatchResult 表示一次基于 Kubernetes Watch API 的增量事件统计。
type ResourceWatchResult struct {
	ResourceName string    `json:"resource_name"`
	ChangeCount  int       `json:"change_count"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
}

// ResourceObjectSnapshot 是资源对象的精简快照,用于落库和前端展示。
type ResourceObjectSnapshot struct {
	APIVersion      string `json:"api_version"`
	Kind            string `json:"kind"`
	ResourceName    string `json:"resource_name"`
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	UID             string `json:"uid"`
	ResourceVersion string `json:"resource_version"`
	Status          string `json:"status"`
	Ready           string `json:"ready"`
	NodeName        string `json:"node_name"`
	Summary         string `json:"summary"`
	ContentHash     string `json:"content_hash"`
}

// ListPods 列出指定命名空间(空字符串=全部)的 Pod。
func (c *Collector) ListPods(ctx context.Context, namespace string) ([]PodInfo, error) {
	list, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods := make([]PodInfo, 0, len(list.Items))
	for i := range list.Items {
		pods = append(pods, toPodInfo(&list.Items[i]))
	}
	return pods, nil
}

// ListNodes 列出集群所有节点。
func (c *Collector) ListNodes(ctx context.Context) ([]NodeInfo, error) {
	list, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	nodes := make([]NodeInfo, 0, len(list.Items))
	for i := range list.Items {
		n := &list.Items[i]
		nodes = append(nodes, NodeInfo{
			Name:    n.Name,
			Ready:   isNodeReady(n),
			Version: n.Status.NodeInfo.KubeletVersion,
		})
	}
	return nodes, nil
}

// ListNodeMetrics 返回节点 CPU/内存实时指标。需要集群部署 metrics-server。
func (c *Collector) ListNodeMetrics(ctx context.Context) ([]NodeMetricInfo, error) {
	if c.metricsClient == nil {
		return nil, fmt.Errorf("metrics client 未配置")
	}
	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	metricList, err := c.metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	allocatableByNode := make(map[string]corev1.ResourceList, len(nodeList.Items))
	for i := range nodeList.Items {
		allocatableByNode[nodeList.Items[i].Name] = nodeList.Items[i].Status.Allocatable
	}
	items := make([]NodeMetricInfo, 0, len(metricList.Items))
	for i := range metricList.Items {
		m := &metricList.Items[i]
		allocatable := allocatableByNode[m.Name]
		cpuUsageMilli := m.Usage.Cpu().MilliValue()
		cpuAllocatableMilli := allocatable.Cpu().MilliValue()
		memUsageBytes := m.Usage.Memory().Value()
		memAllocatableBytes := allocatable.Memory().Value()
		items = append(items, NodeMetricInfo{
			Name:                   m.Name,
			CPUUsageMilli:          cpuUsageMilli,
			CPUAllocatableMilli:    cpuAllocatableMilli,
			CPUUsagePercent:        percent(cpuUsageMilli, cpuAllocatableMilli),
			MemoryUsageBytes:       memUsageBytes,
			MemoryAllocatableBytes: memAllocatableBytes,
			MemoryUsagePercent:     percent(memUsageBytes, memAllocatableBytes),
		})
	}
	return items, nil
}

// Overview 汇总集群概览(节点/Pod/命名空间统计)。
func (c *Collector) Overview(ctx context.Context) (*ClusterOverview, error) {
	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	nsList, err := c.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	ov := &ClusterOverview{
		Nodes:       len(nodeList.Items),
		Pods:        len(podList.Items),
		Namespaces:  len(nsList.Items),
		PodsByPhase: map[string]int{},
	}
	for i := range nodeList.Items {
		if isNodeReady(&nodeList.Items[i]) {
			ov.NodesReady++
		}
	}
	for i := range podList.Items {
		ov.PodsByPhase[string(podList.Items[i].Status.Phase)]++
	}
	return ov, nil
}

// SyncResourceSnapshots 对集群内关键资源做一次只读同步采样。
func (c *Collector) SyncResourceSnapshots(ctx context.Context) []ResourceSyncSnapshot {
	snapshots := make([]ResourceSyncSnapshot, 0, 18)
	var clusterVersion string

	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("v1", "Node", "nodes", false, err))
	} else {
		if len(nodeList.Items) > 0 {
			clusterVersion = nodeList.Items[0].Status.NodeInfo.KubeletVersion
		}
		snapshots = append(snapshots, syncOK("v1", "Node", "nodes", len(nodeList.Items), false, clusterVersion, nodeList.ResourceVersion))
	}

	nsList, err := c.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("v1", "Namespace", "namespaces", false, err))
	} else {
		snapshots = append(snapshots, syncOK("v1", "Namespace", "namespaces", len(nsList.Items), false, "", nsList.ResourceVersion))
	}

	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("v1", "Pod", "pods", true, err))
	} else {
		snapshots = append(snapshots, syncOK("v1", "Pod", "pods", len(podList.Items), true, "", podList.ResourceVersion))
	}

	deployList, err := c.clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("apps/v1", "Deployment", "deployments", true, err))
	} else {
		snapshots = append(snapshots, syncOK("apps/v1", "Deployment", "deployments", len(deployList.Items), true, "", deployList.ResourceVersion))
	}

	dsList, err := c.clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("apps/v1", "DaemonSet", "daemonsets", true, err))
	} else {
		snapshots = append(snapshots, syncOK("apps/v1", "DaemonSet", "daemonsets", len(dsList.Items), true, "", dsList.ResourceVersion))
	}

	stsList, err := c.clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("apps/v1", "StatefulSet", "statefulsets", true, err))
	} else {
		snapshots = append(snapshots, syncOK("apps/v1", "StatefulSet", "statefulsets", len(stsList.Items), true, "", stsList.ResourceVersion))
	}

	serviceList, err := c.clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("v1", "Service", "services", true, err))
	} else {
		snapshots = append(snapshots, syncOK("v1", "Service", "services", len(serviceList.Items), true, "", serviceList.ResourceVersion))
	}

	ingressList, err := c.clientset.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("networking.k8s.io/v1", "Ingress", "ingresses", true, err))
	} else {
		snapshots = append(snapshots, syncOK("networking.k8s.io/v1", "Ingress", "ingresses", len(ingressList.Items), true, "", ingressList.ResourceVersion))
	}

	configMapList, err := c.clientset.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("v1", "ConfigMap", "configmaps", true, err))
	} else {
		snapshots = append(snapshots, syncOK("v1", "ConfigMap", "configmaps", len(configMapList.Items), true, "", configMapList.ResourceVersion))
	}

	secretList, err := c.clientset.CoreV1().Secrets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("v1", "Secret", "secrets", true, err))
	} else {
		snapshots = append(snapshots, syncOK("v1", "Secret", "secrets", len(secretList.Items), true, "", secretList.ResourceVersion))
	}

	jobList, err := c.clientset.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("batch/v1", "Job", "jobs", true, err))
	} else {
		snapshots = append(snapshots, syncOK("batch/v1", "Job", "jobs", len(jobList.Items), true, "", jobList.ResourceVersion))
	}

	cronJobList, err := c.clientset.BatchV1().CronJobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("batch/v1", "CronJob", "cronjobs", true, err))
	} else {
		snapshots = append(snapshots, syncOK("batch/v1", "CronJob", "cronjobs", len(cronJobList.Items), true, "", cronJobList.ResourceVersion))
	}

	pvcList, err := c.clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("v1", "PersistentVolumeClaim", "persistentvolumeclaims", true, err))
	} else {
		snapshots = append(snapshots, syncOK("v1", "PersistentVolumeClaim", "persistentvolumeclaims", len(pvcList.Items), true, "", pvcList.ResourceVersion))
	}

	pvList, err := c.clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("v1", "PersistentVolume", "persistentvolumes", true, err))
	} else {
		snapshots = append(snapshots, syncOK("v1", "PersistentVolume", "persistentvolumes", len(pvList.Items), true, "", pvList.ResourceVersion))
	}

	eventList, err := c.clientset.CoreV1().Events("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("v1", "Event", "events", true, err))
	} else {
		snapshots = append(snapshots, syncOK("v1", "Event", "events", len(eventList.Items), true, "", eventList.ResourceVersion))
	}

	hpaList, err := c.clientset.AutoscalingV2().HorizontalPodAutoscalers("").List(ctx, metav1.ListOptions{})
	if err != nil {
		snapshots = append(snapshots, syncError("autoscaling/v2", "HorizontalPodAutoscaler", "horizontalpodautoscalers", true, err))
	} else {
		snapshots = append(snapshots, syncOK("autoscaling/v2", "HorizontalPodAutoscaler", "horizontalpodautoscalers", len(hpaList.Items), true, "", hpaList.ResourceVersion))
	}

	crd := ResourceSyncSnapshot{
		APIVersion:           "apiextensions.k8s.io/v1",
		Kind:                 "CustomResourceDefinition",
		ResourceName:         "customresourcedefinitions",
		Count:                -1,
		IncrementalSupported: false,
		Supported:            false,
	}
	if c.dynamicClient != nil {
		crdList, err := c.dynamicClient.Resource(schema.GroupVersionResource{
			Group:    "apiextensions.k8s.io",
			Version:  "v1",
			Resource: "customresourcedefinitions",
		}).List(ctx, metav1.ListOptions{})
		if err != nil {
			crd = syncError(crd.APIVersion, crd.Kind, crd.ResourceName, false, err)
		} else {
			crd = syncOK(crd.APIVersion, crd.Kind, crd.ResourceName, len(crdList.Items), false, "", crdList.GetResourceVersion())
		}
	}
	snapshots = append(snapshots, crd)

	return snapshots
}

// WatchResourceChanges 从上一次 resourceVersion 开始读取 Watch 事件,用于增量同步统计。
func (c *Collector) WatchResourceChanges(ctx context.Context, previousResourceVersions map[string]string) map[string]ResourceWatchResult {
	results := map[string]ResourceWatchResult{}
	targets := []struct {
		resourceName string
		watchFn      func(context.Context, metav1.ListOptions) (watch.Interface, error)
	}{
		{
			resourceName: "pods",
			watchFn: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				return c.clientset.CoreV1().Pods("").Watch(ctx, opts)
			},
		},
		{
			resourceName: "deployments",
			watchFn: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				return c.clientset.AppsV1().Deployments("").Watch(ctx, opts)
			},
		},
		{
			resourceName: "daemonsets",
			watchFn: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				return c.clientset.AppsV1().DaemonSets("").Watch(ctx, opts)
			},
		},
		{
			resourceName: "statefulsets",
			watchFn: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
				return c.clientset.AppsV1().StatefulSets("").Watch(ctx, opts)
			},
		},
	}
	for _, target := range targets {
		results[target.resourceName] = c.watchResourceChangeCount(ctx, target.resourceName, previousResourceVersions[target.resourceName], target.watchFn)
	}
	return results
}

func (c *Collector) watchResourceChangeCount(ctx context.Context, resourceName, resourceVersion string, watchFn func(context.Context, metav1.ListOptions) (watch.Interface, error)) ResourceWatchResult {
	started := time.Now()
	result := ResourceWatchResult{
		ResourceName: resourceName,
		Status:       "n/a",
		StartedAt:    started,
		FinishedAt:   started,
	}
	if strings.TrimSpace(resourceVersion) == "" {
		return result
	}
	watchCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	timeoutSeconds := int64(1)
	watcher, err := watchFn(watchCtx, metav1.ListOptions{
		ResourceVersion:     resourceVersion,
		TimeoutSeconds:      &timeoutSeconds,
		AllowWatchBookmarks: true,
	})
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		result.FinishedAt = time.Now()
		return result
	}
	defer watcher.Stop()
	result.Status = "success"
	for event := range watcher.ResultChan() {
		if event.Type == watch.Bookmark {
			continue
		}
		result.ChangeCount++
	}
	result.FinishedAt = time.Now()
	return result
}

// SyncResourceObjectSnapshots 采集前端需要直接查看的资源对象快照。
func (c *Collector) SyncResourceObjectSnapshots(ctx context.Context) []ResourceObjectSnapshot {
	rows := make([]ResourceObjectSnapshot, 0)
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range nodes.Items {
			rows = append(rows, nodeObjectSnapshot(&nodes.Items[i]))
		}
	}
	namespaces, err := c.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range namespaces.Items {
			rows = append(rows, namespaceObjectSnapshot(&namespaces.Items[i]))
		}
	}
	pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range pods.Items {
			rows = append(rows, podObjectSnapshot(&pods.Items[i]))
		}
	}
	deployments, err := c.clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range deployments.Items {
			rows = append(rows, deploymentObjectSnapshot(&deployments.Items[i]))
		}
	}
	statefulSets, err := c.clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range statefulSets.Items {
			rows = append(rows, statefulSetObjectSnapshot(&statefulSets.Items[i]))
		}
	}
	daemonSets, err := c.clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range daemonSets.Items {
			rows = append(rows, daemonSetObjectSnapshot(&daemonSets.Items[i]))
		}
	}
	services, err := c.clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range services.Items {
			rows = append(rows, serviceObjectSnapshot(&services.Items[i]))
		}
	}
	ingresses, err := c.clientset.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range ingresses.Items {
			rows = append(rows, ingressObjectSnapshot(&ingresses.Items[i]))
		}
	}
	configMaps, err := c.clientset.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range configMaps.Items {
			rows = append(rows, configMapObjectSnapshot(&configMaps.Items[i]))
		}
	}
	secrets, err := c.clientset.CoreV1().Secrets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range secrets.Items {
			rows = append(rows, secretObjectSnapshot(&secrets.Items[i]))
		}
	}
	jobs, err := c.clientset.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range jobs.Items {
			rows = append(rows, jobObjectSnapshot(&jobs.Items[i]))
		}
	}
	cronJobs, err := c.clientset.BatchV1().CronJobs("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range cronJobs.Items {
			rows = append(rows, cronJobObjectSnapshot(&cronJobs.Items[i]))
		}
	}
	pvcs, err := c.clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range pvcs.Items {
			rows = append(rows, pvcObjectSnapshot(&pvcs.Items[i]))
		}
	}
	pvs, err := c.clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range pvs.Items {
			rows = append(rows, pvObjectSnapshot(&pvs.Items[i]))
		}
	}
	events, err := c.clientset.CoreV1().Events("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range events.Items {
			rows = append(rows, eventObjectSnapshot(&events.Items[i]))
		}
	}
	hpas, err := c.clientset.AutoscalingV2().HorizontalPodAutoscalers("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range hpas.Items {
			rows = append(rows, hpaObjectSnapshot(&hpas.Items[i]))
		}
	}
	rows = append(rows, c.dynamicResourceObjectSnapshots(ctx)...)
	return rows
}

func nodeObjectSnapshot(node *corev1.Node) ResourceObjectSnapshot {
	ready := "NotReady"
	if isNodeReady(node) {
		ready = "Ready"
	}
	summary := mustJSON(map[string]any{
		"version":           node.Status.NodeInfo.KubeletVersion,
		"os_image":          node.Status.NodeInfo.OSImage,
		"architecture":      node.Status.NodeInfo.Architecture,
		"container_runtime": node.Status.NodeInfo.ContainerRuntimeVersion,
	})
	return objectSnapshot("v1", "Node", "nodes", "", node.Name, string(node.UID), node.ResourceVersion, ready, ready, "", summary)
}

func namespaceObjectSnapshot(ns *corev1.Namespace) ResourceObjectSnapshot {
	summary := mustJSON(map[string]any{"phase": ns.Status.Phase})
	return objectSnapshot("v1", "Namespace", "namespaces", "", ns.Name, string(ns.UID), ns.ResourceVersion, string(ns.Status.Phase), "", "", summary)
}

func podObjectSnapshot(pod *corev1.Pod) ResourceObjectSnapshot {
	info := toPodInfo(pod)
	summary := mustJSON(map[string]any{
		"phase":    info.Phase,
		"ready":    info.Ready,
		"restarts": info.Restarts,
		"node":     info.Node,
		"reason":   info.Reason,
	})
	return objectSnapshot("v1", "Pod", "pods", pod.Namespace, pod.Name, string(pod.UID), pod.ResourceVersion, info.Phase, info.Ready, info.Node, summary)
}

func deploymentObjectSnapshot(deploy *appsv1.Deployment) ResourceObjectSnapshot {
	desired := deploy.Status.Replicas
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}
	ready := fmt.Sprintf("%d/%d", deploy.Status.ReadyReplicas, desired)
	status := "Progressing"
	if desired > 0 && deploy.Status.ReadyReplicas == desired && deploy.Status.AvailableReplicas == desired {
		status = "Ready"
	}
	summary := mustJSON(map[string]any{
		"replicas":           desired,
		"ready_replicas":     deploy.Status.ReadyReplicas,
		"updated_replicas":   deploy.Status.UpdatedReplicas,
		"available_replicas": deploy.Status.AvailableReplicas,
	})
	return objectSnapshot("apps/v1", "Deployment", "deployments", deploy.Namespace, deploy.Name, string(deploy.UID), deploy.ResourceVersion, status, ready, "", summary)
}

func statefulSetObjectSnapshot(sts *appsv1.StatefulSet) ResourceObjectSnapshot {
	desired := sts.Status.Replicas
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	ready := fmt.Sprintf("%d/%d", sts.Status.ReadyReplicas, desired)
	status := "Progressing"
	if desired > 0 && sts.Status.ReadyReplicas == desired {
		status = "Ready"
	}
	summary := mustJSON(map[string]any{
		"replicas":         desired,
		"ready_replicas":   sts.Status.ReadyReplicas,
		"updated_replicas": sts.Status.UpdatedReplicas,
	})
	return objectSnapshot("apps/v1", "StatefulSet", "statefulsets", sts.Namespace, sts.Name, string(sts.UID), sts.ResourceVersion, status, ready, "", summary)
}

func daemonSetObjectSnapshot(ds *appsv1.DaemonSet) ResourceObjectSnapshot {
	ready := fmt.Sprintf("%d/%d", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled)
	status := "Progressing"
	if ds.Status.DesiredNumberScheduled > 0 && ds.Status.NumberReady == ds.Status.DesiredNumberScheduled {
		status = "Ready"
	}
	summary := mustJSON(map[string]any{
		"desired": ds.Status.DesiredNumberScheduled,
		"ready":   ds.Status.NumberReady,
		"updated": ds.Status.UpdatedNumberScheduled,
	})
	return objectSnapshot("apps/v1", "DaemonSet", "daemonsets", ds.Namespace, ds.Name, string(ds.UID), ds.ResourceVersion, status, ready, "", summary)
}

func serviceObjectSnapshot(svc *corev1.Service) ResourceObjectSnapshot {
	ports := make([]string, 0, len(svc.Spec.Ports))
	for _, port := range svc.Spec.Ports {
		ports = append(ports, fmt.Sprintf("%s:%d/%s", port.Name, port.Port, port.Protocol))
	}
	summary := mustJSON(map[string]any{
		"type":       svc.Spec.Type,
		"cluster_ip": svc.Spec.ClusterIP,
		"ports":      ports,
	})
	return objectSnapshot("v1", "Service", "services", svc.Namespace, svc.Name, string(svc.UID), svc.ResourceVersion, string(svc.Spec.Type), "", "", summary)
}

func ingressObjectSnapshot(ing *networkingv1.Ingress) ResourceObjectSnapshot {
	hosts := make([]string, 0, len(ing.Spec.Rules))
	for _, rule := range ing.Spec.Rules {
		if rule.Host != "" {
			hosts = append(hosts, rule.Host)
		}
	}
	summary := mustJSON(map[string]any{"hosts": hosts, "class": ingressClassName(ing)})
	return objectSnapshot("networking.k8s.io/v1", "Ingress", "ingresses", ing.Namespace, ing.Name, string(ing.UID), ing.ResourceVersion, strings.Join(hosts, ","), "", "", summary)
}

func configMapObjectSnapshot(cm *corev1.ConfigMap) ResourceObjectSnapshot {
	summary := mustJSON(map[string]any{"keys": mapKeys(cm.Data), "binary_keys": mapKeys(cm.BinaryData)})
	return objectSnapshot("v1", "ConfigMap", "configmaps", cm.Namespace, cm.Name, string(cm.UID), cm.ResourceVersion, fmt.Sprintf("%d keys", len(cm.Data)+len(cm.BinaryData)), "", "", summary)
}

func secretObjectSnapshot(secret *corev1.Secret) ResourceObjectSnapshot {
	summary := mustJSON(map[string]any{"type": secret.Type, "keys": mapKeys(secret.Data), "string_keys": mapKeys(secret.StringData)})
	return objectSnapshot("v1", "Secret", "secrets", secret.Namespace, secret.Name, string(secret.UID), secret.ResourceVersion, string(secret.Type), "", "", summary)
}

func jobObjectSnapshot(job *batchv1.Job) ResourceObjectSnapshot {
	status := "Running"
	if job.Status.Failed > 0 {
		status = "Failed"
	} else if job.Status.Succeeded > 0 {
		status = "Succeeded"
	}
	ready := fmt.Sprintf("succeeded:%d failed:%d", job.Status.Succeeded, job.Status.Failed)
	summary := mustJSON(map[string]any{"active": job.Status.Active, "succeeded": job.Status.Succeeded, "failed": job.Status.Failed})
	return objectSnapshot("batch/v1", "Job", "jobs", job.Namespace, job.Name, string(job.UID), job.ResourceVersion, status, ready, "", summary)
}

func cronJobObjectSnapshot(cron *batchv1.CronJob) ResourceObjectSnapshot {
	status := "Active"
	if cron.Spec.Suspend != nil && *cron.Spec.Suspend {
		status = "Suspended"
	}
	summary := mustJSON(map[string]any{"schedule": cron.Spec.Schedule, "suspend": cron.Spec.Suspend})
	return objectSnapshot("batch/v1", "CronJob", "cronjobs", cron.Namespace, cron.Name, string(cron.UID), cron.ResourceVersion, status, cron.Spec.Schedule, "", summary)
}

func pvcObjectSnapshot(pvc *corev1.PersistentVolumeClaim) ResourceObjectSnapshot {
	storage := resourceStorageString(pvc.Status.Capacity)
	summary := mustJSON(map[string]any{"phase": pvc.Status.Phase, "storage": storage, "volume": pvc.Spec.VolumeName, "storage_class": storageClassName(pvc.Spec.StorageClassName)})
	return objectSnapshot("v1", "PersistentVolumeClaim", "persistentvolumeclaims", pvc.Namespace, pvc.Name, string(pvc.UID), pvc.ResourceVersion, string(pvc.Status.Phase), storage, "", summary)
}

func pvObjectSnapshot(pv *corev1.PersistentVolume) ResourceObjectSnapshot {
	storage := resourceStorageString(pv.Spec.Capacity)
	summary := mustJSON(map[string]any{"phase": pv.Status.Phase, "storage": storage, "storage_class": pv.Spec.StorageClassName, "claim": pv.Spec.ClaimRef})
	return objectSnapshot("v1", "PersistentVolume", "persistentvolumes", "", pv.Name, string(pv.UID), pv.ResourceVersion, string(pv.Status.Phase), storage, "", summary)
}

func eventObjectSnapshot(event *corev1.Event) ResourceObjectSnapshot {
	summary := mustJSON(map[string]any{"type": event.Type, "reason": event.Reason, "message": event.Message, "involved_object": event.InvolvedObject})
	return objectSnapshot("v1", "Event", "events", event.Namespace, event.Name, string(event.UID), event.ResourceVersion, event.Type, event.Reason, "", summary)
}

func hpaObjectSnapshot(hpa *autoscalingv2.HorizontalPodAutoscaler) ResourceObjectSnapshot {
	ready := fmt.Sprintf("%d/%d", hpa.Status.CurrentReplicas, hpa.Status.DesiredReplicas)
	status := "Active"
	summary := mustJSON(map[string]any{"current_replicas": hpa.Status.CurrentReplicas, "desired_replicas": hpa.Status.DesiredReplicas, "scale_target": hpa.Spec.ScaleTargetRef})
	return objectSnapshot("autoscaling/v2", "HorizontalPodAutoscaler", "horizontalpodautoscalers", hpa.Namespace, hpa.Name, string(hpa.UID), hpa.ResourceVersion, status, ready, "", summary)
}

func (c *Collector) dynamicResourceObjectSnapshots(ctx context.Context) []ResourceObjectSnapshot {
	if c.dynamicClient == nil {
		return nil
	}
	list, err := c.dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	rows := make([]ResourceObjectSnapshot, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		summary := mustJSON(map[string]any{
			"group":    nestedString(item.Object, "spec", "group"),
			"scope":    nestedString(item.Object, "spec", "scope"),
			"versions": nestedVersionNames(item.Object),
		})
		rows = append(rows, objectSnapshot("apiextensions.k8s.io/v1", "CustomResourceDefinition", "customresourcedefinitions", "", item.GetName(), string(item.GetUID()), item.GetResourceVersion(), nestedString(item.Object, "spec", "scope"), "", "", summary))
	}
	return rows
}

func objectSnapshot(apiVersion, kind, resourceName, namespace, name, uid, resourceVersion, status, ready, nodeName, summary string) ResourceObjectSnapshot {
	return ResourceObjectSnapshot{
		APIVersion:      apiVersion,
		Kind:            kind,
		ResourceName:    resourceName,
		Namespace:       namespace,
		Name:            name,
		UID:             uid,
		ResourceVersion: resourceVersion,
		Status:          status,
		Ready:           ready,
		NodeName:        nodeName,
		Summary:         summary,
		ContentHash:     hashContent(apiVersion + "|" + kind + "|" + namespace + "|" + name + "|" + resourceVersion + "|" + summary),
	}
}

func ingressClassName(ing *networkingv1.Ingress) string {
	if ing.Spec.IngressClassName == nil {
		return ""
	}
	return *ing.Spec.IngressClassName
}

func storageClassName(name *string) string {
	if name == nil {
		return ""
	}
	return *name
}

func resourceStorageString(list corev1.ResourceList) string {
	value, ok := list[corev1.ResourceStorage]
	if !ok {
		return ""
	}
	return value.String()
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nestedString(obj map[string]any, fields ...string) string {
	current := any(obj)
	for _, field := range fields {
		asMap, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = asMap[field]
	}
	value, _ := current.(string)
	return value
}

func nestedVersionNames(obj map[string]any) []string {
	spec, ok := obj["spec"].(map[string]any)
	if !ok {
		return nil
	}
	versions, ok := spec["versions"].([]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(versions))
	for _, version := range versions {
		item, ok := version.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := item["name"].(string); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func hashContent(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func syncOK(apiVersion, kind, resource string, count int, incremental bool, version, resourceVersion string) ResourceSyncSnapshot {
	return ResourceSyncSnapshot{
		APIVersion:           apiVersion,
		Kind:                 kind,
		ResourceName:         resource,
		Count:                count,
		Version:              version,
		ResourceVersion:      resourceVersion,
		IncrementalSupported: incremental,
		Supported:            true,
	}
}

func syncError(apiVersion, kind, resource string, incremental bool, err error) ResourceSyncSnapshot {
	return ResourceSyncSnapshot{
		APIVersion:           apiVersion,
		Kind:                 kind,
		ResourceName:         resource,
		Count:                -1,
		IncrementalSupported: incremental,
		Supported:            true,
		Error:                err.Error(),
	}
}

// toPodInfo 把原始 Pod 转为精简信息,计算就绪数、重启数与异常原因。
func toPodInfo(p *corev1.Pod) PodInfo {
	var ready, total, restarts int32
	for i := range p.Status.ContainerStatuses {
		cs := &p.Status.ContainerStatuses[i]
		total++
		if cs.Ready {
			ready++
		}
		restarts += cs.RestartCount
	}
	info := PodInfo{
		Namespace: p.Namespace,
		Name:      p.Name,
		Phase:     string(p.Status.Phase),
		Ready:     fmt.Sprintf("%d/%d", ready, total),
		Restarts:  restarts,
		Node:      p.Spec.NodeName,
		Reason:    podAbnormalReason(p),
	}
	return info
}

// podAbnormalReason 提取 Pod 异常原因(如 CrashLoopBackOff/ImagePullBackOff/OOMKilled),正常则返回空。
func podAbnormalReason(p *corev1.Pod) string {
	for i := range p.Status.ContainerStatuses {
		cs := &p.Status.ContainerStatuses[i]
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" && cs.State.Terminated.Reason != "Completed" {
			return cs.State.Terminated.Reason
		}
	}
	return ""
}

// isNodeReady 判断节点 Ready 状态。
func isNodeReady(n *corev1.Node) bool {
	for i := range n.Status.Conditions {
		cond := &n.Status.Conditions[i]
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func percent(used, total int64) int64 {
	if total <= 0 {
		return 0
	}
	return used * 100 / total
}
