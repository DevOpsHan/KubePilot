// Package session 提供对话会话的存储。
// 提供内存与 Redis 两种实现;Redis 用于服务端持久化记忆(重启不丢、多副本共享)。
package session

import (
	"context"
	"sync"
	"time"

	"aiops/internal/ai"
)

// Store 会话存储接口。
type Store interface {
	// Get 返回会话的历史消息。
	Get(ctx context.Context, sessionID string) ([]ai.Message, error)
	// Append 向会话追加消息。
	Append(ctx context.Context, sessionID string, msgs ...ai.Message) error
	// Clear 清空指定会话。
	Clear(ctx context.Context, sessionID string) error
}

// MemoryStore 内存版会话存储(Redis 不可用时的降级方案)。
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string]*entry
	ttl      time.Duration
	maxMsgs  int
}

type entry struct {
	messages []ai.Message
	updated  time.Time
}

// NewMemoryStore 创建内存存储:30 分钟空闲过期,单会话最多 40 条消息。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]*entry),
		ttl:      30 * time.Minute,
		maxMsgs:  40,
	}
}

func (m *MemoryStore) Get(_ context.Context, sessionID string) ([]ai.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.sessions[sessionID]
	if !ok || time.Since(e.updated) > m.ttl {
		return nil, nil
	}
	return append([]ai.Message(nil), e.messages...), nil
}

func (m *MemoryStore) Append(_ context.Context, sessionID string, msgs ...ai.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.sessions[sessionID]
	if !ok || time.Since(e.updated) > m.ttl {
		e = &entry{}
		m.sessions[sessionID] = e
	}
	e.messages = append(e.messages, msgs...)
	if len(e.messages) > m.maxMsgs {
		e.messages = e.messages[len(e.messages)-m.maxMsgs:]
	}
	e.updated = time.Now()
	return nil
}

func (m *MemoryStore) Clear(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
	return nil
}
