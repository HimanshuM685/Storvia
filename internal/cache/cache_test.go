package cache

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestMemorySetGetDelete covers the basic contract both implementations share.
func TestMemorySetGetDelete(t *testing.T) {
	ctx := context.Background()
	c := NewMemory()

	if err := c.Set(ctx, "key", []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := c.Get(ctx, "key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "value" {
		t.Errorf("Get = %q, want value", got)
	}

	if err := c.Delete(ctx, "key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := c.Get(ctx, "key"); !errors.Is(err, ErrMiss) {
		t.Errorf("after Delete, Get = %v, want ErrMiss", err)
	}
	// Deleting an absent key must not error: revocation paths are idempotent.
	if err := c.Delete(ctx, "key"); err != nil {
		t.Errorf("deleting an absent key returned %v, want nil", err)
	}
}

// TestMemoryMissOnUnknownKey checks the sentinel every caller branches on.
func TestMemoryMissOnUnknownKey(t *testing.T) {
	_, err := NewMemory().Get(context.Background(), "never-written")
	if !errors.Is(err, ErrMiss) {
		t.Errorf("Get = %v, want ErrMiss", err)
	}
}

// TestMemoryRejectsNonPositiveTTL is the guard against an immortal cache entry.
//
// A token cached without expiry would outlive its own revocation, which is a
// security bug rather than a convenience.
func TestMemoryRejectsNonPositiveTTL(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()

	if err := c.Set(ctx, "k", []byte("v"), 0); err == nil {
		t.Error("a zero TTL must be rejected")
	}
	if err := c.Set(ctx, "k", []byte("v"), -time.Second); err == nil {
		t.Error("a negative TTL must be rejected")
	}
}

// TestMemoryExpiryUsesTheInjectedClock avoids sleeping while still proving the
// TTL is honoured.
func TestMemoryExpiryUsesTheInjectedClock(t *testing.T) {
	now := time.Now()
	c := NewMemory(WithClock(func() time.Time { return now }))

	ctx := context.Background()
	if err := c.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "k"); err != nil {
		t.Fatalf("entry should be live before expiry: %v", err)
	}

	now = now.Add(time.Minute + time.Second)
	if _, err := c.Get(ctx, "k"); !errors.Is(err, ErrMiss) {
		t.Errorf("after expiry Get = %v, want ErrMiss", err)
	}
}

// TestMemoryReturnsACopy checks that a caller cannot mutate stored state.
func TestMemoryReturnsACopy(t *testing.T) {
	ctx := context.Background()
	c := NewMemory()
	if err := c.Set(ctx, "k", []byte("original"), time.Minute); err != nil {
		t.Fatal(err)
	}

	first, err := c.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'X'

	second, err := c.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != "original" {
		t.Errorf("stored value = %q; Get must return a copy, not the stored slice", second)
	}
}

// TestMemoryCopiesOnSet is the mirror check: mutating the caller's slice after
// Set must not change the stored value.
func TestMemoryCopiesOnSet(t *testing.T) {
	ctx := context.Background()
	c := NewMemory()

	value := []byte("original")
	if err := c.Set(ctx, "k", value, time.Minute); err != nil {
		t.Fatal(err)
	}
	value[0] = 'X'

	got, err := c.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Errorf("stored value = %q, want a copy taken at Set time", got)
	}
}

// TestMemoryIsSafeForConcurrentUse matters because every request touches the
// token cache.
func TestMemoryIsSafeForConcurrentUse(t *testing.T) {
	c := NewMemory()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := string(rune('a' + n%26))
			_ = c.Set(ctx, key, []byte("v"), time.Minute)
			_, _ = c.Get(ctx, key)
			_ = c.Delete(ctx, key)
		}(i)
	}
	wg.Wait()
}

// TestMemoryJanitorReclaimsExpiredEntries proves expired entries do not
// accumulate for the lifetime of a long-running process.
func TestMemoryJanitorReclaimsExpiredEntries(t *testing.T) {
	now := time.Now()
	c := NewMemory(WithClock(func() time.Time { return now }), WithJanitorInterval(time.Hour))

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := c.Set(ctx, "expiring", []byte("v"), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// Keep the janitor out of this test by disabling the sweep and checking the
	// entries map directly after an explicit sweep.
	now = now.Add(2 * time.Minute)
	c.sweep()

	c.mu.RLock()
	remaining := len(c.entries)
	c.mu.RUnlock()
	if remaining != 0 {
		t.Errorf("after sweeping past expiry, %d entries remain, want 0", remaining)
	}
}

// TestMemoryCloseIsIdempotent matters because Close runs in a deferred call.
func TestMemoryCloseIsIdempotent(t *testing.T) {
	c := NewMemory(WithJanitorInterval(time.Millisecond))
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// interface conformance: the in-memory implementation must satisfy Cache, so a
// caller can swap in Redis without changing code.
var _ Cache = (*Memory)(nil)
var _ Cache = (*Redis)(nil)
