package app

import (
	"sync/atomic"
)

// RoutingSwitch controls whether uploads prefer Drive or the fallback.
//
// There are two distinct reasons to want the fallback, and conflating them has cost
// time before:
//
//   - Fault injection pretends Drive is broken so the fallback path can be exercised
//     on demand. The provider is healthy; we are lying to ourselves.
//   - Forcing the fallback actually prefers it. Drive may be refusing traffic, and
//     the question worth answering is whether users can keep working at all.
//
// The second is a routing decision, so it lives here rather than in the config's
// fault-injection switch.
type RoutingSwitch struct {
	// configured is the durable default from config.json.
	configured bool
	// override is the panel's runtime toggle. A nil pointer means "no override", so
	// the configured value applies.
	override atomic.Pointer[bool]
}

// NewRoutingSwitch builds the switch with its configured default.
func NewRoutingSwitch(configured bool) *RoutingSwitch {
	return &RoutingSwitch{configured: configured}
}

// Forced reports whether uploads should bypass Drive.
func (s *RoutingSwitch) Forced() bool {
	if s == nil {
		return false
	}
	if override := s.override.Load(); override != nil {
		return *override
	}
	return s.configured
}

// SetForced changes the runtime value.
//
// A nil value clears the override and restores the configured default, which is what
// the panel's "follow configuration" choice means.
func (s *RoutingSwitch) SetForced(forced *bool) {
	if s == nil {
		return
	}
	s.override.Store(forced)
}

// Configured reports the durable default, so the panel can show it alongside the
// current value and make clear that clearing an override does not change it.
func (s *RoutingSwitch) Configured() bool {
	if s == nil {
		return false
	}
	return s.configured
}

// Overridden reports whether a runtime override is in place.
func (s *RoutingSwitch) Overridden() bool {
	return s != nil && s.override.Load() != nil
}
