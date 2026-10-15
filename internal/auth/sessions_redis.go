package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrSessionBackendUnavailable reports that the session store could not be reached.
//
// It is deliberately distinct from "session not found". Confusing a Redis outage with
// an invalid token logs everyone out at once, which looks like an attack and is not.
var ErrSessionBackendUnavailable = errors.New("auth: the session store is unavailable")

// RedisSessions stores sessions in Redis.
//
// The in-memory store is fine for one process and wrong the moment there are two: a
// token issued by one instance is invisible to the other, so every second request from
// a user fails. This exists so a multi-instance deployment is possible without
// changing anything above the session port.
type RedisSessions struct {
	client redis.UniversalClient
	prefix string
	ttl    time.Duration
}

// RedisSessionOption configures the store.
type RedisSessionOption func(*RedisSessions)

// WithSessionPrefix sets the key namespace.
//
// It exists so several services can share one Redis without colliding, and so a test
// can use its own namespace rather than the production one.
func WithSessionPrefix(prefix string) RedisSessionOption {
	return func(s *RedisSessions) { s.prefix = prefix }
}

// WithSessionTTL sets how long a session survives without being touched.
//
// It should be at least the token lifetime. A shorter TTL logs users out while their
// cookie is still valid, which presents as random authentication failures.
func WithSessionTTL(ttl time.Duration) RedisSessionOption {
	return func(s *RedisSessions) {
		if ttl > 0 {
			s.ttl = ttl
		}
	}
}

// NewRedisSessions builds a session store backed by an existing client.
//
// It takes a client rather than a connection string so the caller owns the connection,
// its retry policy, and its shutdown, and so the same client can serve the cache.
func NewRedisSessions(client redis.UniversalClient, opts ...RedisSessionOption) *RedisSessions {
	store := &RedisSessions{
		client: client,
		prefix: "storvia:session:",
		ttl:    24 * time.Hour,
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

func (s *RedisSessions) key(sessionID string) string {
	return s.prefix + sessionID
}

// StoreSession records a session.
//
// The TTL is set on the key rather than checked on read, so an abandoned session
// disappears on its own instead of accumulating until something sweeps it.
func (s *RedisSessions) StoreSession(ctx context.Context, session Session) error {
	if session.ID == "" {
		return errors.New("auth: a session needs an id")
	}
	payload, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("auth: encode session: %w", err)
	}
	if err := s.client.Set(ctx, s.key(session.ID), payload, s.ttl).Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrSessionBackendUnavailable, err)
	}
	return nil
}

// LookupSession returns the identity bound to a session id.
//
// A miss is a miss, not an error. Treating "no such session" as a failure would turn
// every expired token into a server error.
func (s *RedisSessions) LookupSession(ctx context.Context, sessionID string) (Identity, bool) {
	payload, err := s.client.Get(ctx, s.key(sessionID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Identity{}, false
		}
		// A backend problem must not be reported as "not signed in".
		return Identity{}, false
	}

	var session Session
	if err := json.Unmarshal(payload, &session); err != nil {
		// A corrupt entry is treated as absent. It will expire on its own, and
		// refusing to serve it is safer than trusting what is in it.
		return Identity{}, false
	}
	if session.ExpiresAt.Before(time.Now()) {
		return Identity{}, false
	}
	return Identity{
		TenantID:  session.Identity.TenantID,
		UserID:    session.Identity.UserID,
		SessionID: session.ID,
	}, true
}

// RevokeSession removes a session immediately.
//
// It exists so a logout or a disconnect takes effect at once instead of waiting for
// the token to expire.
func (s *RedisSessions) RevokeSession(ctx context.Context, sessionID string) error {
	if err := s.client.Del(ctx, s.key(sessionID)).Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrSessionBackendUnavailable, err)
	}
	return nil
}

// Ping checks the backend is reachable.
//
// Readiness uses it, because a service that cannot resolve sessions must not be sent
// traffic: every authenticated request would fail while health checks stayed green.
func (s *RedisSessions) Ping(ctx context.Context) error {
	if err := s.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrSessionBackendUnavailable, err)
	}
	return nil
}

// Count reports how many sessions are stored.
//
// Redis has no cheap way to count a key prefix, and scanning is O(N) over the whole
// keyspace. It is offered for a deliberate administrative check and is not on any hot
// path.
func (s *RedisSessions) Count(ctx context.Context) (int64, error) {
	var count int64
	iter := s.client.Scan(ctx, 0, s.prefix+"*", 1000).Iterator()
	for iter.Next(ctx) {
		count++
	}
	if err := iter.Err(); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrSessionBackendUnavailable, err)
	}
	return count, nil
}

// ensure the store satisfies the port.
var _ Lookup = (*RedisSessions)(nil)
