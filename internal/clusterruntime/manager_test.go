package clusterruntime

import "testing"

func TestManagerRegistersAndReturnsRuntimeByClusterID(t *testing.T) {
	manager := NewManager()
	runtime := &Runtime{ClusterID: 7}

	if err := manager.Register(runtime); err != nil {
		t.Fatalf("注册运行时失败: %v", err)
	}

	got, err := manager.Get(7)
	if err != nil {
		t.Fatalf("查询运行时失败: %v", err)
	}
	if got != runtime {
		t.Fatalf("返回的运行时不正确: got=%p want=%p", got, runtime)
	}
	if _, err := manager.Get(8); err == nil {
		t.Fatal("查询不存在的集群运行时时应返回错误")
	}
}

func TestManagerRejectsInvalidRuntime(t *testing.T) {
	manager := NewManager()
	if err := manager.Register(nil); err == nil {
		t.Fatal("不应允许注册空运行时")
	}
	if err := manager.Register(&Runtime{}); err == nil {
		t.Fatal("不应允许注册 clusterID 为空的运行时")
	}
}
