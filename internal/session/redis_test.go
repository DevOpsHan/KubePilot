package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"aiops/internal/ai"
)

func TestRedisStoreAppendDoesNotLoseConcurrentMessages(t *testing.T) {
	const (
		writers = 64
		rounds  = 3
	)
	store, _ := newRedisStoreForTest(t, writers, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for round := 0; round < rounds; round++ {
		sessionID := fmt.Sprintf("concurrent-%d", round)
		start := make(chan struct{})
		errs := make(chan error, writers)
		var wg sync.WaitGroup
		for index := 0; index < writers; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				errs <- store.Append(ctx, sessionID, ai.Message{
					Role:    "user",
					Content: fmt.Sprintf("round-%d-message-%d", round, index),
				})
			}(index)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("第 %d 轮并发追加失败: %v", round, err)
			}
		}

		messages, err := store.Get(ctx, sessionID)
		if err != nil {
			t.Fatalf("第 %d 轮读取会话失败: %v", round, err)
		}
		if len(messages) != writers {
			t.Fatalf("第 %d 轮并发追加丢失消息: got=%d want=%d", round, len(messages), writers)
		}
		seen := make(map[string]struct{}, writers)
		for _, message := range messages {
			seen[message.Content] = struct{}{}
		}
		if len(seen) != writers {
			t.Fatalf("第 %d 轮并发追加出现重复或覆盖: unique=%d want=%d", round, len(seen), writers)
		}
	}
}

func TestRedisStoreAppendPreservesJSONLimitAndTTL(t *testing.T) {
	const sessionID = "legacy-json"
	store, server := newRedisStoreForTest(t, 3, 2*time.Hour)
	ctx := context.Background()
	legacy := []ai.Message{
		{Role: "user", Content: "old-1"},
		{Role: "assistant", Content: "old-2"},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("编码历史会话失败: %v", err)
	}
	if err := store.client.Set(ctx, store.key(sessionID), data, time.Minute).Err(); err != nil {
		t.Fatalf("写入历史会话失败: %v", err)
	}

	if err := store.Append(ctx, sessionID,
		ai.Message{Role: "user", Content: "new-1"},
		ai.Message{Role: "assistant", Content: "new-2"},
	); err != nil {
		t.Fatalf("追加会话失败: %v", err)
	}
	messages, err := store.Get(ctx, sessionID)
	if err != nil {
		t.Fatalf("读取追加后的会话失败: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("会话上限错误: got=%d want=3", len(messages))
	}
	for index, expected := range []string{"old-2", "new-1", "new-2"} {
		if messages[index].Content != expected {
			t.Fatalf("截断后的消息顺序错误: index=%d got=%q want=%q", index, messages[index].Content, expected)
		}
	}
	if ttl := server.TTL(store.key(sessionID)); ttl != 2*time.Hour {
		t.Fatalf("追加后 TTL 未刷新: got=%s want=%s", ttl, 2*time.Hour)
	}
}

func newRedisStoreForTest(t *testing.T, maxMessages int, ttl time.Duration) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr:     server.Addr(),
		PoolSize: 128,
	})
	t.Cleanup(func() { _ = client.Close() })
	return &RedisStore{client: client, ttl: ttl, maxMsgs: maxMessages}, server
}
