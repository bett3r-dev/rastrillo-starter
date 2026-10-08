package telemetry

import (
	"context"
	crand "crypto/rand"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// durationBuckets are the semantic conventions' advice for
// http.server.request.duration and db.client.operation.duration, in
// seconds.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// Propagator reads and writes W3C traceparent and baggage. The
// middleware uses it directly rather than the global, so a caller's
// trace is joined whether or not Setup ran.
var Propagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

// HTTP is Middleware on the global providers: one span and one
// duration sample per request, for every route the router serves.
func HTTP(next http.Handler) http.Handler {
	return Middleware(nil, nil)(next)
}

// Middleware traces and times each request. Mount it with r.Use on the
// chi router itself, not around it: the span is named after the route
// PATTERN (POST /bills/{id}/split), which chi only knows once it has
// routed, and never after the path, which can carry a reference or a
// token. A request no route matched is named by its method alone.
//
// Every request leaves it with a valid trace id in its context, export
// on or off: the caller's, when a traceparent came in, else a new one.
// With no SDK installed the tracer is a no-op that has no ids to give,
// so the middleware mints them; the no-op then hands that span context
// to every child span started under it. That id is the request's
// correlation (TraceID), on its change records and log lines.
//
// nil providers mean the globals.
func Middleware(tp trace.TracerProvider, mp metric.MeterProvider) func(http.Handler) http.Handler {
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	tracer := tp.Tracer(Scope)
	duration, _ := mp.Meter(Scope).Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of HTTP server requests."),
		metric.WithExplicitBucketBoundaries(durationBuckets...))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := Propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := startTraced(tracer, ctx, r.Method, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r.WithContext(ctx))

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK // the handler wrote nothing
			}
			attrs := []attribute.KeyValue{
				attribute.String("http.request.method", r.Method),
				attribute.Int("http.response.status_code", status),
			}
			if rc := chi.RouteContext(r.Context()); rc != nil {
				if route := rc.RoutePattern(); route != "" {
					attrs = append(attrs, attribute.String("http.route", route))
					span.SetName(r.Method + " " + route)
				}
			}
			span.SetAttributes(attrs...)
			if status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
			duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
		})
	}
}

// TraceID is the trace id of the request ctx belongs to, as 32 hex
// digits, or "" outside any request. It is the same in every span,
// goroutine and nested call that was handed the request's context.
func TraceID(ctx context.Context) string {
	tid := trace.SpanContextFromContext(ctx).TraceID()
	if !tid.IsValid() {
		return ""
	}
	return tid.String()
}

// Start begins a span on the global tracer that is guaranteed a trace
// id, the way every request gets one: the trace in ctx if there is
// one, else a new trace. It is how work that no request started (a
// background job) gets a correlation, so its tracked writes are not
// refused as uncorrelated.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return startTraced(otel.Tracer(Scope), ctx, name, opts...)
}

func startTraced(tracer trace.Tracer, ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	ctx, span := tracer.Start(ctx, name, opts...)
	if !span.SpanContext().IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, mint())
	}
	return ctx, span
}

// mint is a fresh, unsampled span context, for work started with no
// tracer SDK installed.
func mint() trace.SpanContext {
	var tid trace.TraceID
	var sid trace.SpanID
	_, _ = crand.Read(tid[:])
	_, _ = crand.Read(sid[:])
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid})
}
