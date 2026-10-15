package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var _ Cache = (*Redis)(nil)

// Redis is a Cache backed by a Redis server. Use it when more than one API
// instance must observe the same cached OAuth access token or the same Drive
// circuit state; otherwise prefer Memory, which needs no extra service.
type Redis struct {
	client redis.UniversalClient
}

// RedisOption configures Redis.
type RedisOption func(*redis.Options)

// WithPassword sets the Redis AUTH password.
func WithPassword(password string) RedisOption {
	return func(o *redis.Options) { o.Password = password }
}

// WithDatabase selects the Redis logical database index.
func WithDatabase(index int) RedisOption {
	return func(o *redis.Options) { o.DB = index }
}

// WithClientName labels the connection in server-side client lists.
func WithClientName(name string) RedisOption {
	return func(o *redis.Options) { o.ClientName = name }
}

// NewRedis dials addr. The returned Cache reports connection problems from
// Get and Set rather than at construction, so a cache outage degrades to a miss
// instead of taking the API down.
func NewRedis(ctx context.Context, addr string, opts ...RedisOption) (*Redis, error) {
	options := &redis.Options{
		Addr:         addr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
	for _, opt := range opts {
		opt(options)
	}
	return &Redis{client: redis.NewClient(options)}, nil
}

// Get returns the value for key or ErrMiss. A Redis connection error is
// returned wrapped, so callers can decide whether a cache outage should block
// the request or fall through to the provider.
func (r *Redis) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := r.client.Get(ctx, key).Bytes()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, ErrMiss
	case err != nil:
		return nil, fmt.Errorf("cache: redis get %q: %w", key, err)
	}
	return value, nil
}

// Set stores value under key for ttl. Redis applies the expiry server side, so
// an evicted entry cannot come back as immortal.
func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("cache: ttl must be positive")
	}
	if err := r.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache: redis set %q: %w", key, err)
	}
	return nil
}

// Delete removes key. Redis DEL on an absent key succeeds, keeping revocation
// idempotent.
func (r *Redis) Delete(ctx context.Context, key string) error {
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("cache: redis delete %q: %w", key, err)
	}
	return nil
}

// Close releases the connection pool.
func (r *Redis) Close() error { return r.client.Close() }

// Ping verifies connectivity. Used by the readiness probe, not by Get.
func (r *Redis) Ping(ctx context.Context) error {
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("cache: redis ping: %w", err)
	}
	return nil
}

// Client exposes the underlying Redis client.
//
// It exists so another component can use the same connection rather than opening a
// second one. Sessions use it: a service with a pool for its cache and a separate pool
// for sessions is two pools to size, monitor, and eventually leak.
func (r *Redis) Client(context.Context) (redis.UniversalClient, error) {
	if r == nil || r.client == nil {
		return nil, errors.New("cache: this redis backend has no client")
	}
	return r.client, nil
}
