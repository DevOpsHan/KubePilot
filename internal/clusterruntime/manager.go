// Package clusterruntime 管理每个 Kubernetes 集群对应的运行时依赖。
package clusterruntime

import (
	"fmt"
	"sync"

	"aiops/internal/collector"
	"aiops/internal/inspect"
	"aiops/internal/remediate"
	"aiops/internal/store"
)

// Runtime 保存某个集群可执行运维动作所需的运行时组件。
type Runtime struct {
	ClusterID  uint
	Cluster    *store.Cluster
	Collector  *collector.Collector
	Engine     *inspect.Engine
	Remediator *remediate.Executor
}

// Manager 维护 clusterID 到运行时组件的映射。
type Manager struct {
	mu       sync.RWMutex
	runtimes map[uint]*Runtime
}

// NewManager 创建空运行时管理器。
func NewManager() *Manager {
	return &Manager{runtimes: map[uint]*Runtime{}}
}

// Register 注册或替换某个集群运行时。
func (m *Manager) Register(runtime *Runtime) error {
	if runtime == nil {
		return fmt.Errorf("cluster runtime 不能为空")
	}
	if runtime.ClusterID == 0 {
		return fmt.Errorf("clusterID 不能为空")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runtimes[runtime.ClusterID] = runtime
	return nil
}

// Get 返回指定集群运行时。
func (m *Manager) Get(clusterID uint) (*Runtime, error) {
	if clusterID == 0 {
		return nil, fmt.Errorf("clusterID 不能为空")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	runtime, ok := m.runtimes[clusterID]
	if !ok {
		return nil, fmt.Errorf("集群 %d 的运行时未就绪", clusterID)
	}
	return runtime, nil
}

// Remove 移除指定集群运行时。
func (m *Manager) Remove(clusterID uint) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.runtimes, clusterID)
}

// List 返回当前已注册运行时快照。
func (m *Manager) List() []*Runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]*Runtime, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		items = append(items, runtime)
	}
	return items
}
