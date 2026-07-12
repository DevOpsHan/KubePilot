package server

import (
	"testing"

	"aiops/internal/inspect"
	"aiops/internal/store"
)

// TestAnomalyConcernsTarget 验证复检的目标匹配:命名空间一致 + 名称等值或 Deployment 前缀。
func TestAnomalyConcernsTarget(t *testing.T) {
	cases := []struct {
		name    string
		anomaly inspect.Anomaly
		ticket  *store.ApprovalTicket
		want    bool
	}{
		{
			name:    "Deployment 目标命中其管理的 Pod(前缀)",
			anomaly: inspect.Anomaly{Namespace: "default", Name: "web-6b7f9c8d5-abcde", Rule: "PodCrashLoopBackOff"},
			ticket:  &store.ApprovalTicket{Namespace: "default", Target: "web"},
			want:    true,
		},
		{
			name:    "裸 Pod 目标精确等值命中",
			anomaly: inspect.Anomaly{Namespace: "default", Name: "standalone-pod", Rule: "PodFailed"},
			ticket:  &store.ApprovalTicket{Namespace: "default", Target: "standalone-pod"},
			want:    true,
		},
		{
			name:    "命名空间不一致不命中",
			anomaly: inspect.Anomaly{Namespace: "kube-system", Name: "web-6b7f9c8d5-abcde"},
			ticket:  &store.ApprovalTicket{Namespace: "default", Target: "web"},
			want:    false,
		},
		{
			name:    "同命名空间但无关资源不命中",
			anomaly: inspect.Anomaly{Namespace: "default", Name: "api-xyz", Rule: "PodPending"},
			ticket:  &store.ApprovalTicket{Namespace: "default", Target: "web"},
			want:    false,
		},
		{
			name:    "前缀相近但非「目标-」边界不误命中",
			anomaly: inspect.Anomaly{Namespace: "default", Name: "website-1", Rule: "PodNotReady"},
			ticket:  &store.ApprovalTicket{Namespace: "default", Target: "web"},
			want:    false,
		},
		{
			name:    "空目标不命中",
			anomaly: inspect.Anomaly{Namespace: "default", Name: "web-abc"},
			ticket:  &store.ApprovalTicket{Namespace: "default", Target: ""},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := anomalyConcernsTarget(tc.anomaly, tc.ticket); got != tc.want {
				t.Errorf("anomalyConcernsTarget = %v, want %v", got, tc.want)
			}
		})
	}
}
