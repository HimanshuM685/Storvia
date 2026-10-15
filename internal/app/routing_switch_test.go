package app

import "testing"

// TestRoutingSwitchFollowsConfigByDefault. The durable setting is what a deployment
// ships with, so with no runtime choice made it must be what applies.
func TestRoutingSwitchFollowsConfigByDefault(t *testing.T) {
	for _, configured := range []bool{false, true} {
		switcher := NewRoutingSwitch(configured)
		if got := switcher.Forced(); got != configured {
			t.Errorf("configured=%v: Forced() = %v, want %v", configured, got, configured)
		}
		if switcher.Overridden() {
			t.Errorf("configured=%v: a fresh switch reports an override", configured)
		}
	}
}

// TestRoutingSwitchOverrideBeatsConfig.
func TestRoutingSwitchOverrideBeatsConfig(t *testing.T) {
	switcher := NewRoutingSwitch(true)
	switcher.SetForced(boolPtr(false))

	if switcher.Forced() {
		t.Error("the runtime choice did not override a config that forces the fallback")
	}
	if !switcher.Overridden() {
		t.Error("an active override is not reported as one")
	}
	// The configured value is still remembered, so clearing the override restores it
	// rather than reverting to some third value.
	if !switcher.Configured() {
		t.Error("the configured default was lost")
	}
}

// TestRoutingSwitchClearRestoresConfig. "Go back to whatever is configured" is the
// option an operator reaches for most, so it has to restore the config value exactly.
func TestRoutingSwitchClearRestoresConfig(t *testing.T) {
	for _, configured := range []bool{false, true} {
		switcher := NewRoutingSwitch(configured)
		switcher.SetForced(boolPtr(!configured))
		switcher.SetForced(nil)

		if got := switcher.Forced(); got != configured {
			t.Errorf("configured=%v: after clearing, Forced() = %v, want %v",
				configured, got, configured)
		}
		if switcher.Overridden() {
			t.Errorf("configured=%v: the override should be gone after clearing", configured)
		}
	}
}

// TestNilRoutingSwitchIsSafe. The console reports "forcing unavailable" when the switch
// is absent rather than panicking, which means several code paths can be nil.
func TestNilRoutingSwitchIsSafe(t *testing.T) {
	var switcher *RoutingSwitch
	switcher.SetForced(boolPtr(true))
	switcher.SetForced(nil)
	if switcher.Forced() {
		t.Error("a nil switch must report the fallback as not forced")
	}
	if switcher.Configured() {
		t.Error("a nil switch must report no configured default")
	}
	if switcher.Overridden() {
		t.Error("a nil switch must report no override")
	}
}
