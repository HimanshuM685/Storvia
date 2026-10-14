package auth

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// parseUnix parses a unix timestamp from a token payload.
func parseUnix(text string) (int64, error) {
	return strconv.ParseInt(text, 10, 64)
}

// MemorySessions is the development session store.
//
// It is a process-local map with a bounded size. It is appropriate for a single
// instance or for tests; a multi-instance deployment must use a durable store, and
// the config review is where that decision belongs.
type MemorySessions struct {
	mu       sync.RWMutex
	entries  map[string]Session
	maxCount int
}

// NewMemorySessions builds a session store holding at most maxCount sessions.
func NewMemorySessions(maxCount int) *MemorySessions {
	if maxCount <= 0 {
		maxCount = 10000
	}
	return &MemorySessions{entries: make(map[string]Session), maxCount: maxCount}
}

// StoreSession records a session.
func (m *MemorySessions) StoreSession(_ context.Context, session Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) >= m.maxCount {
		m.evictExpiredLocked(time.Now())
	}
	// Still full after expiry sweep: drop the oldest entry so a burst of sign-ins
	// cannot grow the map without bound.
	if len(m.entries) >= m.maxCount {
		m.evictOldestLocked()
	}
	m.entries[session.ID] = session
	return nil
}

// LookupSession returns the identity bound to a session ID.
func (m *MemorySessions) LookupSession(_ context.Context, sessionID string) (Identity, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.entries[sessionID]
	if !ok {
		return Identity{}, false
	}
	return session.Identity, true
}

// DeleteSession removes a session, which is what sign-out does.
func (m *MemorySessions) DeleteSession(_ context.Context, sessionID string) {
	m.mu.Lock()
	delete(m.entries, sessionID)
	m.mu.Unlock()
}

func (m *MemorySessions) evictExpiredLocked(now time.Time) {
	for id, session := range m.entries {
		if now.After(session.ExpiresAt) {
			delete(m.entries, id)
		}
	}
}

func (m *MemorySessions) evictOldestLocked() {
	var oldestID string
	var oldestExpiry time.Time
	for id, session := range m.entries {
		if oldestID == "" || session.ExpiresAt.Before(oldestExpiry) {
			oldestID = id
			oldestExpiry = session.ExpiresAt
		}
	}
	if oldestID != "" {
		delete(m.entries, oldestID)
	}
}
