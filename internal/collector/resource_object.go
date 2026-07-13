package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	sigsyaml "sigs.k8s.io/yaml"
)

// ErrInvalidResourceRequest 表示调用方传入的资源定位参数无效(应映射为 400 而非上游错误)。
var ErrInvalidResourceRequest = errors.New("无效的资源请求参数")

// ResourceObjectDetail 是单个 Kubernetes 对象的实时详情。
type ResourceObjectDetail struct {
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	YAML       string `json:"yaml"`
}

// GetResourceObjectYAML 使用动态客户端实时获取单个对象并渲染为 YAML。
// apiVersion 形如 "v1"(核心组)或 "apps/v1";resourceName 是复数资源名(如 "deployments")。
// namespace 为空表示集群级资源。
func (c *Collector) GetResourceObjectYAML(ctx context.Context, apiVersion, resourceName, namespace, name string) (*ResourceObjectDetail, error) {
	if c == nil || c.dynamicClient == nil {
		return nil, fmt.Errorf("K8S 动态客户端未就绪")
	}
	apiVersion = strings.TrimSpace(apiVersion)
	resourceName = strings.TrimSpace(resourceName)
	name = strings.TrimSpace(name)
	namespace = strings.TrimSpace(namespace)
	if apiVersion == "" || resourceName == "" || name == "" {
		return nil, fmt.Errorf("%w: api_version、resource 和 name 均不能为空", ErrInvalidResourceRequest)
	}

	gvr, err := parseGroupVersionResource(apiVersion, resourceName)
	if err != nil {
		return nil, err
	}

	requestCtx, cancel := context.WithTimeout(ctx, defaultKubernetesRequestTimeout)
	defer cancel()

	resourceClient := c.dynamicClient.Resource(gvr)
	var obj *unstructured.Unstructured
	if namespace != "" {
		obj, err = resourceClient.Namespace(namespace).Get(requestCtx, name, metav1.GetOptions{})
	} else {
		obj, err = resourceClient.Get(requestCtx, name, metav1.GetOptions{})
	}
	if err != nil {
		return nil, err
	}

	yamlBytes, err := sigsyaml.Marshal(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("序列化 YAML 失败: %w", err)
	}
	return &ResourceObjectDetail{
		APIVersion: apiVersion,
		Kind:       obj.GetKind(),
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		YAML:       string(yamlBytes),
	}, nil
}

// parseGroupVersionResource 从 apiVersion(group/version 或 version)与复数资源名构造 GVR。
func parseGroupVersionResource(apiVersion, resourceName string) (schema.GroupVersionResource, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("%w: 无效的 api_version %q: %v", ErrInvalidResourceRequest, apiVersion, err)
	}
	return gv.WithResource(resourceName), nil
}
