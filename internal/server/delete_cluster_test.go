package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"aiops/internal/store"
)

func TestDeleteClusterRemovesClusterCredentialAndPolicy(t *testing.T) {
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cluster, err := st.CreateCluster(store.CreateClusterInput{
		Name:                "待删除集群",
		Slug:                "delete-me",
		Mode:                store.ClusterModeKubeconfigData,
		EncryptedKubeconfig: "encrypted",
		KubeconfigHash:      "hash",
		RemediationEnabled:  true,
	})
	if err != nil {
		t.Fatalf("创建集群失败: %v", err)
	}

	srv := NewServer(nil, nil, nil, st, nil, nil, 0, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/clusters/"+strconvID(cluster.ID), nil)
	attachServerTestAdminSession(t, st, req)
	rec := httptest.NewRecorder()
	srv.Echo().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("删除集群失败: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := st.GetCluster(cluster.ID); err == nil {
		t.Fatal("集群记录应被删除")
	}
	if _, err := st.GetClusterCredential(cluster.ID); err == nil {
		t.Fatal("集群凭据应被删除")
	}
	if _, err := st.GetClusterPolicy(cluster.ID); err == nil {
		t.Fatal("集群策略应被删除")
	}
}
