package collector

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestWatchResourceChangesCountsPodEventsFromPreviousResourceVersion(t *testing.T) {
	client := fake.NewSimpleClientset()
	fakeWatch := watch.NewFake()
	client.Fake.PrependWatchReactor("pods", func(action clienttesting.Action) (bool, watch.Interface, error) {
		watchAction := action.(clienttesting.WatchAction)
		if watchAction.GetWatchRestrictions().ResourceVersion != "100" {
			t.Fatalf("Watch 应从上次 resourceVersion 开始: got %q", watchAction.GetWatchRestrictions().ResourceVersion)
		}
		go func() {
			fakeWatch.Add(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "default"}})
			fakeWatch.Stop()
		}()
		return true, fakeWatch, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := New(client).WatchResourceChanges(ctx, map[string]string{"pods": "100"})
	if result["pods"].ChangeCount != 1 {
		t.Fatalf("应统计 Watch 事件数量: %+v", result["pods"])
	}
	if result["pods"].Status != "success" {
		t.Fatalf("Watch 成功时状态应为 success: %+v", result["pods"])
	}
}
