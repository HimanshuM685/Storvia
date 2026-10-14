package service

import "errors"

// errorsAs is a thin alias so the service files read consistently and the
// errors package is imported in exactly one place.
func errorsAs(err error, target any) bool { return errors.As(err, target) }
