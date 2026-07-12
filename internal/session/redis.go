package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"aiops/internal/ai"
)

// RedisStore 基于 Redis 的会话存储:服务端持久化,后端重启不丢、多副本共享。
type RedisStore struct {
	client  *redis.Client
	ttl     time.Duration
	maxMsgs int
}

// NewRedisStore 连接 Redis 并返回存储。会话 24 小时过期,单会话最多 40 条消息。
func NewRedisStore(addr, password string, db int) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &RedisStore{client: client, ttl: 24 * time.Hour, maxMsgs: 40}, nil
}

func (s *RedisStore) key(sessionID string) string {
	return "aiops:chat:" + sessionID
}

func (s *RedisStore) Get(ctx context.Context, sessionID string) ([]ai.Message, error) {
	data, err := s.client.Get(ctx, s.key(sessionID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var msgs []ai.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (s *RedisStore) Append(ctx context.Context, sessionID string, msgs ...ai.Message) error {
	key := s.key(sessionID)
	for {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			data, err := tx.Get(ctx, key).Bytes()
			var existing []ai.Message
			switch {
			case err == nil:
				if err := json.Unmarshal(data, &existing); err != nil {
					return err
				}
			case errors.Is(err, redis.Nil):
				existing = nil
			default:
				return err
			}

			existing = append(existing, msgs...)
			if len(existing) > s.maxMsgs {
				existing = existing[len(existing)-s.maxMsgs:]
			}
			encoded, err := json.Marshal(existing)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, encoded, s.ttl)
				return nil
			})
			return err
		}, key)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (s *RedisStore) Clear(ctx context.Context, sessionID string) error {
	return s.client.Del(ctx, s.key(sessionID)).Err()
}
