package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// failingClient stands in for an unreachable Redis.
//
// It embeds the interface rather than implementing it: only the three commands the
// session store actually calls are overridden, and anything else panics loudly rather
// than silently returning a plausible zero.
type failingClient struct {
	redis.UniversalClient
}

func (failingClient) Get(context.Context, string) *redis.StringCmd {
	return redis.NewStringResult("", errors.New("connection refused"))
}
func (failingClient) Set(context.Context, string, any, time.Duration) *redis.StatusCmd {
	return redis.NewStatusResult("", errors.New("connection refused"))
}
func (failingClient) Ping(context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("", errors.New("connection refused"))
}
func (failingClient) Del(context.Context, ...string) *redis.IntCmd {
	return redis.NewIntResult(0, errors.New("connection refused"))
}

// The store must distinguish "no such session" from "backend down". Conflating them
// logs every user out the moment Redis blinks.
func TestRedisSessionsReportsBackendFailureDistinctly(t *testing.T) {
	store := NewRedisSessions(&failingClient{})

	err := store.StoreSession(context.Background(), Session{ID: "s1"})
	if !errors.Is(err, ErrSessionBackendUnavailable) {
		t.Errorf("StoreSession error = %v, want ErrSessionBackendUnavailable", err)
	}
	if err := store.Ping(context.Background()); !errors.Is(err, ErrSessionBackendUnavailable) {
		t.Errorf("Ping error = %v, want ErrSessionBackendUnavailable", err)
	}
	// A lookup miss is a miss, not an error: a broken store must not present as
	// "everybody is signed out" through the normal path.
	if _, ok := store.LookupSession(context.Background(), "s1"); ok {
		t.Error("an unreachable store must not resolve a session")
	}
}

// TestSessionStoreMustHaveAnID guards against an empty key, which would make every
// write collide on one Redis entry.
func TestSessionStoreMustHaveAnID(t *testing.T) {
	store := NewRedisSessions(&failingClient{})
	if err := store.StoreSession(context.Background(), Session{}); err == nil {
		t.Fatal("a session with no id must be refused")
	}
}

// TestSessionKeysAreNamespaced matters when several services share one Redis.
func TestSessionKeysAreNamespaced(t *testing.T) {
	a := NewRedisSessions(&failingClient{})
	b := NewRedisSessions(&failingClient{}, WithSessionPrefix("other:"))

	if a.key("s1") == b.key("s1") {
		t.Error("two prefixes produced the same key")
	}
	if a.key("s1") != "storvia:session:s1" {
		t.Errorf("key = %q", a.key("s1"))
	}
}

// TestSessionTTLDefaultsSensibly guards the failure this prevents: a key expiring
// before its token does logs users out while their cookie is still valid, which
// presents as random authentication failures.
func TestSessionTTLDefaultsSensibly(t *testing.T) {
	store := NewRedisSessions(&failingClient{})
	if store.ttl <= 0 {
		t.Errorf("default ttl = %v, want positive", store.ttl)
	}
	// A zero or negative override must not disable expiry.
	store = NewRedisSessions(&failingClient{}, WithSessionTTL(0))
	if store.ttl <= 0 {
		t.Error("a zero ttl must not clear the default; sessions would never expire")
	}
}
