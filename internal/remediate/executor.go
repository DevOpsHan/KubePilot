// Package remediate 执行集群修复写操作(重启/删除/扩缩容/滚动重启),严格受功能开关与审批流程约束。
package remediate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Executor 修复执行器。所有写操作前都会校验功能开关。
type Executor struct {
	clientset kubernetes.Interface
	enabled   bool
}

// New 创建执行器。
func New(cs kubernetes.Interface, enabled bool) *Executor {
	return &Executor{clientset: cs, enabled: enabled}
}

// Enabled 修复功能是否启用。
func (e *Executor) Enabled() bool { return e.enabled }

func (e *Executor) check(ns string) error {
	if e == nil || e.clientset == nil {
		return fmt.Errorf("修复执行器未就绪")
	}
	if !e.enabled {
		return fmt.Errorf("修复功能未启用")
	}
	return nil
}

// Execute 按 action 分发执行写操作。params 为 JSON(如 {"replicas":3})。
func (e *Executor) Execute(ctx context.Context, action, ns, target, paramsJSON string) (string, error) {
	if err := e.check(ns); err != nil {
		return "", err
	}
	switch action {
	case "restart_pod":
		return e.restartPod(ctx, ns, target)
	case "delete_pod":
		return e.deletePod(ctx, ns, target)
	case "scale_deployment":
		return e.scaleDeployment(ctx, ns, target, paramsJSON)
	case "restart_deployment":
		return e.restartDeployment(ctx, ns, target)
	default:
		return "", fmt.Errorf("不支持的修复操作: %s", action)
	}
}

// restartPod 删除 Pod,由其控制器自动重建(等效"重启")。
func (e *Executor) restartPod(ctx context.Context, ns, name string) (string, error) {
	if err := e.clientset.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return "", err
	}
	return fmt.Sprintf("已删除 Pod %s/%s,控制器将自动重建(等效重启)", ns, name), nil
}

// deletePod 删除 Pod。
func (e *Executor) deletePod(ctx context.Context, ns, name string) (string, error) {
	if err := e.clientset.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return "", err
	}
	return fmt.Sprintf("已删除 Pod %s/%s", ns, name), nil
}

// scaleDeployment 调整 Deployment 副本数。params: {"replicas": N}
func (e *Executor) scaleDeployment(ctx context.Context, ns, name, paramsJSON string) (string, error) {
	var p struct {
		Replicas *int32 `json:"replicas"`
	}
	if err := json.Unmarshal([]byte(paramsJSON), &p); err != nil || p.Replicas == nil {
		return "", fmt.Errorf("scale_deployment 需要参数 replicas(整数)")
	}
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, *p.Replicas)
	_, err := e.clientset.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已将 Deployment %s/%s 副本数调整为 %d", ns, name, *p.Replicas), nil
}

// restartDeployment 滚动重启 Deployment(patch template 注解,触发所有 Pod 重建)。
func (e *Executor) restartDeployment(ctx context.Context, ns, name string) (string, error) {
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"aiops.io/restartedAt":%q}}}}}`,
		time.Now().Format(time.RFC3339))
	_, err := e.clientset.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已触发 Deployment %s/%s 滚动重启", ns, name), nil
}

// ResolveWorkload 反查 Pod 归属的顶层工作负载。
// 若 Pod 经 ReplicaSet 由 Deployment 管理,返回 ("Deployment", deployName);
// 直属 StatefulSet/DaemonSet/Job/裸 Pod 则返回 ("", ""),由调用方回退到单 Pod 处理。
// 反查过程中出现错误(如 RBAC、网络)会返回 error,调用方应记录并回退,不应中断主流程。
func (e *Executor) ResolveWorkload(ctx context.Context, ns, pod string) (kind, name string, err error) {
	if e == nil || e.clientset == nil {
		return "", "", fmt.Errorf("修复执行器未就绪")
	}
	p, err := e.clientset.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	ref := controllerOwnerRef(p.OwnerReferences)
	if ref == nil || ref.Kind != "ReplicaSet" {
		return "", "", nil // 裸 Pod 或非 Deployment 体系(StatefulSet/DaemonSet 等)
	}
	rs, err := e.clientset.AppsV1().ReplicaSets(ns).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	rsRef := controllerOwnerRef(rs.OwnerReferences)
	if rsRef == nil || rsRef.Kind != "Deployment" {
		return "", "", nil
	}
	return "Deployment", rsRef.Name, nil
}

// controllerOwnerRef 返回 ownerReferences 中标记为 controller 的那条(即真正的管控者)。
func controllerOwnerRef(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}
