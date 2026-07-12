package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"aiops/internal/clusterruntime"
	"aiops/internal/collector"
	"aiops/internal/inspect"
	"aiops/internal/store"
)

func TestListNodesUsesRequestedClusterRuntime(t *testing.T) {
	manager := clusterruntime.NewManager()
	registerTestRuntime(t, manager, 1, "node-a")
	registerTestRuntime(t, manager, 2, "node-b")
	st := openServerTestStore(t)

	srv := NewServer(nil, nil, nil, st, nil, nil, 1, manager)

	defaultNodes := requestNodeNames(t, st, srv, "/api/cluster/nodes")
	if len(defaultNodes) != 1 || defaultNodes[0] != "node-a" {
		t.Fatalf("默认集群节点不正确: %+v", defaultNodes)
	}

	selectedNodes := requestNodeNames(t, st, srv, "/api/cluster/nodes?cluster_id=2")
	if len(selectedNodes) != 1 || selectedNodes[0] != "node-b" {
		t.Fatalf("指定集群节点不正确: %+v", selectedNodes)
	}
}

func registerTestRuntime(t *testing.T, manager *clusterruntime.Manager, clusterID uint, nodeName string) {
	t.Helper()
	kubeClient := fake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	})
	col := collector.New(kubeClient)
	if err := manager.Register(&clusterruntime.Runtime{
		ClusterID: clusterID,
		Collector: col,
		Engine:    inspect.NewEngine(col, nil),
	}); err != nil {
		t.Fatalf("注册测试运行时失败: %v", err)
	}
}

func requestNodeNames(t *testing.T, st *store.Store, srv *Server, target string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("请求节点列表失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Nodes []collector.NodeInfo `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	names := make([]string, 0, len(body.Nodes))
	for _, node := range body.Nodes {
		names = append(names, node.Name)
	}
	return names
}
