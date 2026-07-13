package collector

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newResourceObjectScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DeploymentList"}, &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "NamespaceList"}, &unstructured.UnstructuredList{})
	return scheme
}

func TestGetResourceObjectYAMLReturnsNamespacedObject(t *testing.T) {
	deployment := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "web",
			"namespace": "default",
		},
		"spec": map[string]any{"replicas": int64(3)},
	}}
	client := dynamicfake.NewSimpleDynamicClient(newResourceObjectScheme(), deployment)
	c := &Collector{dynamicClient: client}

	detail, err := c.GetResourceObjectYAML(context.Background(), "apps/v1", "deployments", "default", "web")
	if err != nil {
		t.Fatalf("获取对象失败: %v", err)
	}
	if detail.Kind != "Deployment" || detail.Name != "web" || detail.Namespace != "default" {
		t.Fatalf("对象元信息不符: %+v", detail)
	}
	if !strings.Contains(detail.YAML, "name: web") || !strings.Contains(detail.YAML, "kind: Deployment") {
		t.Fatalf("YAML 内容缺少关键字段: %s", detail.YAML)
	}
}

func TestGetResourceObjectYAMLReturnsClusterScopedObject(t *testing.T) {
	namespace := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": "kube-system"},
	}}
	client := dynamicfake.NewSimpleDynamicClient(newResourceObjectScheme(), namespace)
	c := &Collector{dynamicClient: client}

	detail, err := c.GetResourceObjectYAML(context.Background(), "v1", "namespaces", "", "kube-system")
	if err != nil {
		t.Fatalf("获取集群级对象失败: %v", err)
	}
	if detail.Kind != "Namespace" || detail.Name != "kube-system" {
		t.Fatalf("对象元信息不符: %+v", detail)
	}
}

func TestGetResourceObjectYAMLValidatesInput(t *testing.T) {
	c := &Collector{dynamicClient: dynamicfake.NewSimpleDynamicClient(newResourceObjectScheme())}
	if _, err := c.GetResourceObjectYAML(context.Background(), "", "deployments", "default", "web"); err == nil {
		t.Fatal("空 api_version 应返回错误")
	}
	if _, err := c.GetResourceObjectYAML(context.Background(), "apps/v1", "deployments", "default", ""); err == nil {
		t.Fatal("空 name 应返回错误")
	}
}

func TestGetResourceObjectYAMLRequiresDynamicClient(t *testing.T) {
	c := &Collector{}
	if _, err := c.GetResourceObjectYAML(context.Background(), "v1", "namespaces", "", "kube-system"); err == nil {
		t.Fatal("动态客户端缺失时应返回错误")
	}
}
