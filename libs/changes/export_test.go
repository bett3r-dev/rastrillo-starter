package changes

import "log/slog"

// Lenient makes t's Refuse behave as outside go test: an undeclared
// rule comes back on Refused.Err instead of panicking.
func Lenient(t *Trail) *Trail {
	t.strict = false
	return t
}

// LoggerOf is where t's refusals are logged.
func LoggerOf(t *Trail) *slog.Logger { return t.logger }
