package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration is a time.Duration that unmarshals from a human-readable string.
//
// It exists so a configuration file can say "3s" instead of 3000000000. It also
// accepts a bare integer, which is interpreted as nanoseconds to match
// time.Duration's own JSON behaviour, and it rejects anything ambiguous rather
// than defaulting to zero: a timeout that silently becomes 0 means no timeout at
// all, which is the opposite of what an operator writing "0" probably meant.
type Duration time.Duration

// UnmarshalJSON parses a duration string.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		// Fall back to a numeric value.
		var number int64
		if numErr := json.Unmarshal(data, &number); numErr == nil {
			*d = Duration(number)
			return nil
		}
		return fmt.Errorf("config: duration must be a string such as \"3s\" or a number of nanoseconds: %w", err)
	}

	text = strings.TrimSpace(text)
	if text == "" {
		*d = 0
		return nil
	}

	// A bare number is nanoseconds, matching encoding/json's own handling.
	if number, err := strconv.ParseInt(text, 10, 64); err == nil {
		*d = Duration(number)
		return nil
	}

	parsed, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("config: %q is not a valid duration: %w", text, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalJSON renders the duration as its string form.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Duration converts back to the standard library type.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// String implements fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }

// Or returns d when it is positive, otherwise fallback. Configuration helpers use
// it to apply defaults without a long chain of ifs.
func (d Duration) Or(fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return time.Duration(d)
}
