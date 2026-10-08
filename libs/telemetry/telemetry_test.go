package telemetry

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amadan.net/rastrillo/rastrillo/budget"
	"amadan.net/rastrillo/rastrillo/db"
	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestMain(m *testing.M) { os.Exit(budget.Main(m)) }

func providers() (*tracetest.SpanRecorder, *sdktrace.TracerProvider, *sdkmetric.ManualReader, *sdkmetric.MeterProvider) {
	rec := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	return rec, sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)),
		reader, sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
}

func attr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// A request is named by its route pattern, never its path, and the
// duration sample carries the route and the status.
func TestRequestSpanIsNamedByRoute(t *testing.T) {
	t.Parallel()
	rec, tp, reader, mp := providers()
	r := chi.NewRouter()
	r.Use(Middleware(tp, mp))
	r.Get("/bills/{id}/split", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/bills/42/split", nil))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/nowhere/secret-token", nil))

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	if got := spans[0].Name(); got != "GET /bills/{id}/split" {
		t.Errorf("span name = %q, want the route pattern", got)
	}
	if v, _ := attr(spans[0], "http.response.status_code"); v.AsInt64() != http.StatusTeapot {
		t.Errorf("status attribute = %v, want 418", v.AsInt64())
	}
	if got := spans[1].Name(); got != "GET" {
		t.Errorf("unrouted span name = %q, want the method alone (a path can carry a token)", got)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Histogram[float64]).DataPoints {
				if route, ok := dp.Attributes.Value("http.route"); ok && route.AsString() == "/bills/{id}/split" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("no http.server.request.duration sample for /bills/{id}/split")
	}
}

// A caller's traceparent makes the request's span a child of the
// caller's, so a trace crosses services.
func TestRequestSpanJoinsTheCallersTrace(t *testing.T) {
	t.Parallel()
	rec, tp, _, mp := providers()
	r := chi.NewRouter()
	r.Use(Middleware(tp, mp))
	r.Get("/", func(http.ResponseWriter, *http.Request) {})

	ctx, parent := tp.Tracer("caller").Start(context.Background(), "caller")
	req := httptest.NewRequest("GET", "/", nil)
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(req.Header))
	r.ServeHTTP(httptest.NewRecorder(), req)
	parent.End()

	for _, s := range rec.Ended() {
		if s.Name() == "GET /" {
			if s.Parent().SpanID() != parent.SpanContext().SpanID() {
				t.Errorf("request span parent = %v, want the caller's span", s.Parent().SpanID())
			}
			return
		}
	}
	t.Fatal("no span for GET /")
}

// A query given the request's context is a child of the request's
// span, and its span carries the SQL without the values bound into it.
func TestQuerySpanIsTheRequestsChildAndCarriesNoValues(t *testing.T) {
	t.Parallel()
	rec, tp, _, mp := providers()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.G.Exec("CREATE TABLE things (id INTEGER PRIMARY KEY, name TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := InstrumentDB(d.G, tp, mp); err != nil {
		t.Fatal(err)
	}

	type thing struct {
		ID   int64
		Name string
	}
	ctx, req := tp.Tracer("test").Start(context.Background(), "request")
	if err := d.G.WithContext(ctx).Create(&thing{Name: "luis@example.com"}).Error; err != nil {
		t.Fatal(err)
	}
	var got []thing
	if err := d.G.WithContext(ctx).Where("name = ?", "luis@example.com").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	req.End()

	names := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		names[s.Name()] = s
	}
	for _, name := range []string{"INSERT things", "SELECT things"} {
		s, ok := names[name]
		if !ok {
			t.Fatalf("no %q span among %v", name, keys(names))
		}
		if s.Parent().SpanID() != req.SpanContext().SpanID() {
			t.Errorf("%s: parent is not the request span", name)
		}
		if s.SpanKind() != trace.SpanKindClient {
			t.Errorf("%s: kind = %v, want client", name, s.SpanKind())
		}
		q, _ := attr(s, "db.query.text")
		if q.AsString() == "" || strings.Contains(q.AsString(), "luis@example.com") {
			t.Errorf("%s: db.query.text = %q, want the SQL without its values", name, q.AsString())
		}
	}
}

func keys(m map[string]sdktrace.ReadOnlySpan) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Tee hands each record to every handler, and is the one handler when
// telemetry is off.
func TestTeeWritesToEveryHandler(t *testing.T) {
	t.Parallel()
	var a, b bytes.Buffer
	ha, hb := slog.NewTextHandler(&a, nil), slog.NewTextHandler(&b, nil)
	slog.New(Tee(ha, hb)).With("correlation", "ref1").Info("bank event")
	for name, buf := range map[string]*bytes.Buffer{"first": &a, "second": &b} {
		if !strings.Contains(buf.String(), "bank event") || !strings.Contains(buf.String(), "correlation=ref1") {
			t.Errorf("%s handler got %q", name, buf.String())
		}
	}
	if Tee(ha, nil) != slog.Handler(ha) {
		t.Error("Tee with telemetry off should be the stderr handler itself")
	}
}

type logSink struct{ records []sdklog.Record }

func (s *logSink) Export(_ context.Context, rs []sdklog.Record) error {
	for _, r := range rs {
		s.records = append(s.records, r.Clone())
	}
	return nil
}
func (*logSink) Shutdown(context.Context) error   { return nil }
func (*logSink) ForceFlush(context.Context) error { return nil }

// A line logged with the request's context reaches the backend with
// that request's trace and span, which is what links it to the trace.
func TestLogLineCarriesTheRequestsTrace(t *testing.T) {
	t.Parallel()
	_, tp, _, mp := providers()
	sink := &logSink{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(sink)))
	logger := slog.New(Tee(slog.NewTextHandler(io.Discard, nil), otelslog.NewHandler(Scope, otelslog.WithLoggerProvider(lp))))

	var want trace.SpanContext
	r := chi.NewRouter()
	r.Use(Middleware(tp, mp))
	r.Post("/agent/bank", func(w http.ResponseWriter, r *http.Request) {
		want = trace.SpanContextFromContext(r.Context())
		logger.InfoContext(r.Context(), "bank event", "id", "tx-1")
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/agent/bank", nil))

	if len(sink.records) != 1 {
		t.Fatalf("records = %d, want 1", len(sink.records))
	}
	got := sink.records[0]
	if !want.IsValid() || got.TraceID() != want.TraceID() || got.SpanID() != want.SpanID() {
		t.Errorf("log record trace/span = %v/%v, want the request's %v/%v", got.TraceID(), got.SpanID(), want.TraceID(), want.SpanID())
	}
}

// With no SDK installed (telemetry off, or any test), every request
// still has a trace id, and it carries over to whatever the request's
// context is handed to: a child span, a goroutine.
func TestEveryRequestHasATraceIDWithoutAnSDK(t *testing.T) {
	t.Parallel()
	var outer, child, inGoroutine string
	r := chi.NewRouter()
	r.Use(Middleware(nil, nil)) // the globals, never set in this binary
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		outer = TraceID(r.Context())
		ctx, span := otel.Tracer("nested").Start(r.Context(), "drain")
		defer span.End()
		child = TraceID(ctx)
		done := make(chan struct{})
		go func() { inGoroutine = TraceID(ctx); close(done) }()
		<-done
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if len(outer) != 32 {
		t.Fatalf("TraceID = %q, want 32 hex digits", outer)
	}
	if child != outer || inGoroutine != outer {
		t.Fatalf("child span %q, goroutine %q, want the request's %q", child, inGoroutine, outer)
	}
	if TraceID(context.Background()) != "" {
		t.Fatal("TraceID outside a request should be empty")
	}
}

// Correlate puts the request's trace id on a line logged with the
// request's context, and nothing on a line logged without one.
func TestCorrelateTagsLinesLoggedInARequest(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(Correlate(slog.NewTextHandler(&buf, nil))).With("app", "myapp")
	ctx := trace.ContextWithSpanContext(context.Background(), mint())
	log.InfoContext(ctx, "in a request")
	log.Info("at boot")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if want := "correlation=" + TraceID(ctx); !strings.Contains(lines[0], want) || !strings.Contains(lines[0], "app=myapp") {
		t.Errorf("request line = %q, want %s", lines[0], want)
	}
	if strings.Contains(lines[1], "correlation=") {
		t.Errorf("boot line = %q, want no correlation", lines[1])
	}
}

// Work no request started gets a trace of its own from Start, SDK or
// not, and keeps an existing one when there is.
func TestStartGivesWorkOutsideARequestATrace(t *testing.T) {
	t.Parallel()
	ctx, span := Start(context.Background(), "job")
	defer span.End()
	if len(TraceID(ctx)) != 32 {
		t.Fatalf("TraceID = %q, want a fresh trace", TraceID(ctx))
	}
	inner, span2 := Start(ctx, "step")
	defer span2.End()
	if TraceID(inner) != TraceID(ctx) {
		t.Fatalf("nested Start: %q, want the outer %q", TraceID(inner), TraceID(ctx))
	}
}
