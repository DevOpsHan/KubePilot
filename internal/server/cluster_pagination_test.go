package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"aiops/internal/store"
)

func TestListClusterResourceSnapshotsSupportsOffsetAndCapsLimit(t *testing.T) {
	st := openServerTestStore(t)
	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "资源分页测试集群",
		Slug: "resource-pagination-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	rows := make([]store.ClusterResourceSnapshot, 0, 205)
	for i := 0; i < 205; i++ {
		rows = append(rows, store.ClusterResourceSnapshot{
			ResourceName: "pods",
			APIVersion:   "v1",
			Kind:         "Pod",
			Namespace:    "default",
			Name:         fmt.Sprintf("pod-%03d", i),
			ContentHash:  fmt.Sprintf("hash-%03d", i),
		})
	}
	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, rows); err != nil {
		t.Fatalf("写入测试资源快照失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, nil)

	requestPage := func(query string) (int, struct {
		Count      int                             `json:"count"`
		Offset     int                             `json:"offset"`
		Limit      int                             `json:"limit"`
		Generation string                          `json:"generation"`
		Snapshots  []store.ClusterResourceSnapshot `json:"snapshots"`
	}) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/clusters/%d/resource-snapshots?resource=pods&%s", cluster.ID, query), nil)
		attachServerTestAdminSession(t, st, req)
		rec := httptest.NewRecorder()
		srv.Echo().ServeHTTP(rec, req)
		var body struct {
			Count      int                             `json:"count"`
			Offset     int                             `json:"offset"`
			Limit      int                             `json:"limit"`
			Generation string                          `json:"generation"`
			Snapshots  []store.ClusterResourceSnapshot `json:"snapshots"`
		}
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("解析分页响应失败: %v", err)
			}
		}
		return rec.Code, body
	}

	status, first := requestPage("offset=0&limit=3")
	if status != http.StatusOK || first.Generation == "" {
		t.Fatalf("第一页应返回快照代次: status=%d body=%+v", status, first)
	}
	status, page := requestPage("offset=2&limit=3&generation=" + url.QueryEscape(first.Generation))
	if status != http.StatusOK {
		t.Fatalf("分页请求应成功: status=%d", status)
	}
	if page.Offset != 2 || page.Limit != 3 || page.Count != 3 || len(page.Snapshots) != 3 {
		t.Fatalf("分页元数据不正确: %+v", page)
	}
	if page.Snapshots[0].Name != "pod-002" || page.Snapshots[2].Name != "pod-004" {
		t.Fatalf("offset 未正确应用: %+v", page.Snapshots)
	}

	status, capped := requestPage("offset=0&limit=500")
	if status != http.StatusOK {
		t.Fatalf("超大 limit 应被截断而不是拒绝: status=%d", status)
	}
	if capped.Limit != 200 || capped.Count != 200 || len(capped.Snapshots) != 200 {
		t.Fatalf("limit 上限未限制为 200: limit=%d count=%d len=%d", capped.Limit, capped.Count, len(capped.Snapshots))
	}
}

func TestListClusterResourceSnapshotsRejectsInvalidPagination(t *testing.T) {
	st := openServerTestStore(t)
	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "非法分页测试集群",
		Slug: "invalid-resource-pagination-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, nil)

	for _, query := range []string{"offset=-1", "offset=bad", "offset=1", "limit=0", "limit=-1", "limit=bad"} {
		t.Run(query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet,
				fmt.Sprintf("/api/clusters/%d/resource-snapshots?%s", cluster.ID, query), nil)
			attachServerTestAdminSession(t, st, req)
			rec := httptest.NewRecorder()
			srv.Echo().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("非法分页参数应返回 400: query=%s status=%d body=%s", query, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestListClusterResourceSnapshotsRejectsChangedGeneration(t *testing.T) {
	st := openServerTestStore(t)
	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name: "快照代次冲突测试集群",
		Slug: "resource-generation-conflict-test",
		Mode: store.ClusterModeKubeconfigData,
	})
	if err != nil {
		t.Fatalf("创建测试集群失败: %v", err)
	}
	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, []store.ClusterResourceSnapshot{
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a", ContentHash: "hash-a"},
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-b", ContentHash: "hash-b"},
	}); err != nil {
		t.Fatalf("写入首批资源快照失败: %v", err)
	}
	srv := NewServer(nil, nil, nil, st, nil, nil, cluster.ID, nil)

	request := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/clusters/%d/resource-snapshots?resource=pods&%s", cluster.ID, query), nil)
		attachServerTestAdminSession(t, st, req)
		rec := httptest.NewRecorder()
		srv.Echo().ServeHTTP(rec, req)
		return rec
	}
	first := request("offset=0&limit=1")
	if first.Code != http.StatusOK {
		t.Fatalf("第一页请求失败: status=%d body=%s", first.Code, first.Body.String())
	}
	var firstBody struct {
		Generation string `json:"generation"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil || firstBody.Generation == "" {
		t.Fatalf("解析第一页代次失败: err=%v body=%s", err, first.Body.String())
	}
	if _, err := st.ReplaceClusterResourceSnapshots(cluster.ID, []string{"pods"}, []store.ClusterResourceSnapshot{
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-0", ContentHash: "hash-0"},
		{ResourceName: "pods", APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "pod-a", ContentHash: "hash-a"},
	}); err != nil {
		t.Fatalf("写入新一代资源快照失败: %v", err)
	}

	conflict := request("offset=1&limit=1&generation=" + url.QueryEscape(firstBody.Generation))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("旧代次分页应返回 409: status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	var conflictBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(conflict.Body.Bytes(), &conflictBody); err != nil {
		t.Fatalf("解析冲突响应失败: %v", err)
	}
	if conflictBody.Code != "snapshot_generation_changed" {
		t.Fatalf("冲突响应应包含稳定错误码: %+v", conflictBody)
	}
}
