package telemetry

import (
	"context"
	"errors"
	"log/slog"
)

// Tee is a slog.Handler that hands every record to each of hs: stderr
// keeps the lines the platform's journal already collects, and the
// OTLP handler ships the same lines to Loki with the trace and span of
// the context they were logged under. Nil handlers are skipped, so
// Tee(stderr, t.Logs) is just stderr when telemetry is off.
func Tee(hs ...slog.Handler) slog.Handler {
	var t tee
	for _, h := range hs {
		if h != nil {
			t = append(t, h)
		}
	}
	if len(t) == 1 {
		return t[0]
	}
	return t
}

type tee []slog.Handler

func (t tee) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range t {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (t tee) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range t {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (t tee) WithAttrs(as []slog.Attr) slog.Handler {
	out := make(tee, len(t))
	for i, h := range t {
		out[i] = h.WithAttrs(as)
	}
	return out
}

func (t tee) WithGroup(name string) slog.Handler {
	out := make(tee, len(t))
	for i, h := range t {
		out[i] = h.WithGroup(name)
	}
	return out
}

// Correlate wraps h so every record logged with a request's context
// (InfoContext, ErrorContext, ...) carries correlation=<trace id>,
// read off that context. No call site names it: a line logged in a
// request, or in anything the request's context was handed to, is
// correlated; a line logged without one is not.
func Correlate(h slog.Handler) slog.Handler { return correlate{h} }

type correlate struct{ slog.Handler }

func (c correlate) Handle(ctx context.Context, r slog.Record) error {
	if id := TraceID(ctx); id != "" {
		r = r.Clone()
		r.AddAttrs(slog.String("correlation", id))
	}
	return c.Handler.Handle(ctx, r)
}

func (c correlate) WithAttrs(as []slog.Attr) slog.Handler {
	return correlate{c.Handler.WithAttrs(as)}
}

func (c correlate) WithGroup(name string) slog.Handler {
	return correlate{c.Handler.WithGroup(name)}
}
