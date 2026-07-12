package collector

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSyncResourceObjectSnapshotsCachesCommonKubernetesResources(t *testing.T) {
	replicas := int32(2)
	client := fake.NewSimpleClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", ResourceVersion: "1"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default", ResourceVersion: "2"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "default", ResourceVersion: "3"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "deploy-a", Namespace: "default", ResourceVersion: "4"},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
			Status:     appsv1.DeploymentStatus{Replicas: 2, ReadyReplicas: 1},
		},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "sts-a", Namespace: "default", ResourceVersion: "5"}},
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ds-a", Namespace: "default", ResourceVersion: "6"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc-a", Namespace: "default", ResourceVersion: "7"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.1"}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "ing-a", Namespace: "default", ResourceVersion: "8"}, Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "example.local"}}}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm-a", Namespace: "default", ResourceVersion: "9"}, Data: map[string]string{"a": "b"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec-a", Namespace: "default", ResourceVersion: "10"}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"token": []byte("secret")}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "job-a", Namespace: "default", ResourceVersion: "11"}, Status: batchv1.JobStatus{Succeeded: 1}},
		&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "cron-a", Namespace: "default", ResourceVersion: "12"}, Spec: batchv1.CronJobSpec{Schedule: "*/5 * * * *"}},
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-a", Namespace: "default", ResourceVersion: "13"},
			Status: corev1.PersistentVolumeClaimStatus{
				Phase:    corev1.ClaimBound,
				Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "pv-a", ResourceVersion: "14"},
			Status:     corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
		},
		&corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "event-a", Namespace: "default", ResourceVersion: "15"}, Reason: "Started", Type: corev1.EventTypeNormal},
		&autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: "hpa-a", Namespace: "default", ResourceVersion: "16"}, Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 1, DesiredReplicas: 2}},
	)

	rows := New(client).SyncResourceObjectSnapshots(context.Background())
	want := map[string]string{
		"nodes":                    "node-a",
		"namespaces":               "default",
		"pods":                     "pod-a",
		"deployments":              "deploy-a",
		"statefulsets":             "sts-a",
		"daemonsets":               "ds-a",
		"services":                 "svc-a",
		"ingresses":                "ing-a",
		"configmaps":               "cm-a",
		"secrets":                  "sec-a",
		"jobs":                     "job-a",
		"cronjobs":                 "cron-a",
		"persistentvolumeclaims":   "pvc-a",
		"persistentvolumes":        "pv-a",
		"events":                   "event-a",
		"horizontalpodautoscalers": "hpa-a",
	}
	for resourceName, objectName := range want {
		if !hasSnapshot(rows, resourceName, objectName) {
			t.Fatalf("缺少资源快照 %s/%s: %+v", resourceName, objectName, rows)
		}
	}
	if secret := findObjectSnapshot(rows, "secrets", "sec-a"); secret == nil || secret.Summary == "" || containsSecretValue(secret.Summary, "secret") {
		t.Fatalf("Secret 快照必须保存元信息且不能泄露 data 内容: %+v", secret)
	}
}

func hasSnapshot(rows []ResourceObjectSnapshot, resourceName, name string) bool {
	return findObjectSnapshot(rows, resourceName, name) != nil
}

func findObjectSnapshot(rows []ResourceObjectSnapshot, resourceName, name string) *ResourceObjectSnapshot {
	for i := range rows {
		if rows[i].ResourceName == resourceName && rows[i].Name == name {
			return &rows[i]
		}
	}
	return nil
}

func containsSecretValue(summary, value string) bool {
	return strings.Contains(summary, value)
}
