// Package redis предоставляет клиент для работы с Redis.
// Используется для хранения зашифрованных MasterKey пользователей.
package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Client — потокобезопасная обёртка над *redis.Client.
type Client struct {
	cli *goredis.Client
}

// TryLock atomically acquires a lock with an expiry. The returned boolean is
// false when another worker currently owns the lock.
func (c *Client) TryLock(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	if c == nil || c.cli == nil || key == "" || token == "" || ttl <= 0 {
		return false, fmt.Errorf("invalid redis lock arguments")
	}
	ok, err := c.cli.SetNX(ctx, key, token, ttl).Result()
	return ok, err
}

// Unlock removes the lock only when it is still owned by token.
func (c *Client) Unlock(ctx context.Context, key, token string) error {
	if c == nil || c.cli == nil || key == "" || token == "" {
		return fmt.Errorf("invalid redis lock arguments")
	}
	const script = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
	_, err := c.cli.Eval(ctx, script, []string{key}, token).Result()
	return err
}

// RenewLock продлевает TTL только для владельца lock.
func (c *Client) RenewLock(ctx context.Context, key, token string, ttl time.Duration) error {
	if c == nil || c.cli == nil || key == "" || token == "" || ttl <= 0 {
		return fmt.Errorf("invalid redis lock arguments")
	}
	const script = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("pexpire", KEYS[1], ARGV[2]) else return 0 end`
	result, err := c.cli.Eval(ctx, script, []string{key}, token, ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return fmt.Errorf("redis lock ownership lost")
	}
	return nil
}

// New создаёт новый Redis-клиент.
// addr — host:port, password — пароль (может быть пустым), db — номер БД.
func New(ctx context.Context, addr, password string, db int) (*Client, error) {
	cli := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	if err := cli.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return &Client{cli: cli}, nil
}

// Ping проверяет соединение с Redis.
func (c *Client) Ping(ctx context.Context) error {
	return c.cli.Ping(ctx).Err()
}

// Set сохраняет значение по ключу с TTL.
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.cli.Set(ctx, key, value, ttl).Err()
}

// Expire обновляет TTL для существующего ключа.
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ok, err := c.cli.Expire(ctx, key, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis expire: %w", err)
	}
	if !ok {
		return false, fmt.Errorf("key %s not found in redis", key)
	}
	return true, nil
}

func (c *Client) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return c.cli.SetNX(ctx, key, value, ttl).Result()
}

// Get возвращает значение по ключу.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	return c.cli.Get(ctx, key).Bytes()
}

// Keys возвращает все ключи, соответствующие паттерну.
func (c *Client) Keys(ctx context.Context, pattern string) ([]string, error) {
	return c.cli.Keys(ctx, pattern).Result()
}

// Del удаляет ключ.
func (c *Client) Del(ctx context.Context, key string) error {
	return c.cli.Del(ctx, key).Err()
}

// Close закрывает соединение с Redis.
func (c *Client) Close() error {
	return c.cli.Close()
}
