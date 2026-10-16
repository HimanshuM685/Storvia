package httpapi

import "testing"

// TestAccountLabelFallsBackInOrder.
//
// The navbar shows the connected Google account so an operator can see whose account
// they are about to upload from. Before this, it showed the configured ids
// "user_dev_admin @ tenant_dev_admin", which read identically no matter which account
// was connected.
//
// The chain is deliberate: Google's display name, then the email address, then the
// configured id. Each is less certainly meaningful than the one before it, and the
// configured id is a last resort rather than the default.
func TestAccountLabelFallsBackInOrder(t *testing.T) {
	tests := []struct {
		name  string
		email string
		user  string
		want  string
	}{
		{"Shovan B", "shovan@example.com", "user_dev_admin", "Shovan B"},
		{"", "shovan@example.com", "user_dev_admin", "shovan@example.com"},
		// A connection made before the profile columns existed has neither. It must
		// still render something rather than a blank navbar.
		{"", "", "user_dev_admin", "user_dev_admin"},
		// Whitespace is not a name.
		{"   ", "  ", "user_dev_admin", "user_dev_admin"},
	}

	for _, tc := range tests {
		if got := accountLabel(tc.name, tc.email, tc.user); got != tc.want {
			t.Errorf("accountLabel(%q, %q, %q) = %q, want %q",
				tc.name, tc.email, tc.user, got, tc.want)
		}
	}
}
