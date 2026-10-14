// Package cache provides the caching abstraction used for OAuth access tokens,
// Drive availability results, and Drive circuit evidence.
//
// The interface is deliberately small. In-memory is the default because a single
// instance needs no extra infrastructure; Redis exists for multi-instance
// deployments that must share token and circuit state across replicas.
package cache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrMiss is returned by every implementation when a key is absent or expired.
// Callers must branch on it with errors.Is; a backend-specific nil check is a bug.
var ErrMiss = errors.New("cache: key not found")

// Cache is a namespaced key/value store with per-entry expiry.
type Cache interface {
	// Get returns the value for key, or ErrMiss.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores value under key for ttl. A non-positive ttl is an error rather
	// than a silent no-expiry entry, because an immortal token cache entry is a
	// revocation bug.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Delete removes key. Deleting an absent key is not an error, which keeps
	// revocation paths idempotent.
	Delete(ctx context.Context, key string) error
	// Close releases backend resources.
	Close() error
}

// entry is one stored value with its deadline.
type entry struct {
	value   []byte
	expires time.Time
}

// Memory is an in-process TTL cache safe for concurrent use. It is the default
// backend. A background janitor reclaims expired entries so that a long-lived
// process does not accumulate them between requests.
type Memory struct {
	mu      sync.RWMutex
	entries map[string]entry
	// now is injectable so tests can advance time without sleeping.
	now func() time.Time
	// janitorInterval is the sweep period. Zero disables sweeping, which is
	// what tests use to make expiry deterministic.
	janitorInterval time.Duration
	stop            chan struct{}
	closeOnce       sync.Once
}

var _ Cache = (*Memory)(nil)

// MemoryOption configures Memory.
type MemoryOption func(*Memory)

// WithJanitorInterval sets how often expired entries are swept.
func WithJanitorInterval(d time.Duration) MemoryOption {
	return func(m *Memory) { m.janitorInterval = d }
}

// WithClock overrides the time source. Tests only.
func WithClock(now func() time.Time) MemoryOption {
	return func(m *Memory) { m.now = now }
}

// NewMemory builds an in-process cache.
func NewMemory(opts ...MemoryOption) *Memory {
	m := &Memory{
		entries: make(map[string]entry),
		now:     time.Now,
		stop:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.janitorInterval > 0 {
		go m.janitor()
	}
	return m
}

func (m *Memory) janitor() {
	ticker := time.NewTicker(m.janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.sweep()
		}
	}
}

func (m *Memory) sweep() {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, e := range m.entries {
		if now.After(e.expires) {
			delete(m.entries, key)
		}
	}
}

// Get returns the value for key or ErrMiss.
func (m *Memory) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	e, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok || m.now().After(e.expires) {
		return nil, ErrMiss
	}
	// Copy so a caller cannot mutate stored state through the returned slice.
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, nil
}

// Set stores value under key for ttl.
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("cache: ttl must be positive")
	}
	stored := make([]byte, len(value))
	copy(stored, value)
	m.mu.Lock()
	m.entries[key] = entry{value: stored, expires: m.now().Add(ttl)}
	m.mu.Unlock()
	return nil
}

// Delete removes key. Absent keys are not an error.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
	return nil
}

// Close stops the janitor.
func (m *Memory) Close() error {
	m.closeOnce.Do(func() { close(m.stop) })
	return nil
}
