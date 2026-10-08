// Package telemetry sends the app's traces, metrics and logs over OTLP
// to a collector (Grafana Alloy, an OpenTelemetry Collector, or the
// grafana/otel-lgtm image in ops/observability for local work).
//
// It is push, never pull. A CARLOS instance hibernates when idle, so a
// scraper would either wake every instance on its schedule or find
// nobody home. Instead the SDK batches and exports every few seconds
// while the process runs, and Shutdown flushes whatever is still
// buffered when the platform's SIGTERM ends the process, inside the
// activator's budget.
//
// Off unless OTEL_EXPORTER_OTLP_ENDPOINT is set. Off, the global
// providers stay the SDK's no-ops: the middleware and the database
// callbacks still run, and cost a few interface calls. Every other
// knob is the standard OTEL_* environment the exporters read
// themselves (OTEL_METRIC_EXPORT_INTERVAL, OTEL_BSP_SCHEDULE_DELAY,
// OTEL_RESOURCE_ATTRIBUTES, ...).
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Scope is the instrumentation scope every instrument here is named
// under. Setup sets it to Config.Service; set it before Setup only to
// name the scope differently from the service.
var Scope = "app"

// Config names the process to the backend.
type Config struct {
	Service string // service.name: the app, the same on every instance; also the Scope
	Version string // service.version: what /api/version answers
	// Tenant is who this instance serves (CARLOS runs one instance
	// per tenant), and goes out as service.instance.id as well as
	// tenant: the one resource attribute Prometheus turns into a label
	// (instance) and Loki indexes by default, so every series and log
	// stream is split by tenant without promoting anything.
	Tenant string
}

// Telemetry is what Setup started.
type Telemetry struct {
	// Logs is the slog handler that ships records to the backend, or
	// nil when telemetry is off. Fan it out beside the stderr handler
	// with Tee.
	Logs     slog.Handler
	shutdown []func(context.Context) error
}

// Enabled reports whether the environment asks for export.
func Enabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}

// Setup installs the global tracer, meter and logger providers and the
// W3C trace-context propagator. It dials nothing: the exporters connect
// on their first export, so a missing collector costs the boot nothing
// and drops data, rather than holding up a person's first click.
func Setup(ctx context.Context, cfg Config) (*Telemetry, error) {
	t := &Telemetry{}
	if cfg.Service != "" {
		Scope = cfg.Service
	}
	otel.SetTextMapPropagator(Propagator)
	if !Enabled() {
		return t, nil
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", cfg.Service),
		attribute.String("service.version", cfg.Version),
		attribute.String("service.instance.id", cfg.Tenant),
		attribute.String("tenant", cfg.Tenant),
	))
	if err != nil {
		return nil, err
	}
	// OTEL_RESOURCE_ATTRIBUTES last, so the environment can override.
	if env, err := resource.New(ctx, resource.WithFromEnv()); err == nil {
		if merged, err := resource.Merge(res, env); err == nil {
			res = merged
		}
	}

	te, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(te), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	t.shutdown = append(t.shutdown, tp.Shutdown)

	me, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, t.fail(ctx, err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me)), sdkmetric.WithResource(res))
	otel.SetMeterProvider(mp)
	t.shutdown = append(t.shutdown, mp.Shutdown)

	le, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, t.fail(ctx, err)
	}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(le)), sdklog.WithResource(res))
	global.SetLoggerProvider(lp)
	t.shutdown = append(t.shutdown, lp.Shutdown)
	t.Logs = otelslog.NewHandler(Scope, otelslog.WithLoggerProvider(lp))
	return t, nil
}

func (t *Telemetry) fail(ctx context.Context, err error) error {
	return errors.Join(err, t.Shutdown(ctx))
}

// Shutdown flushes every buffered span, metric and log record and stops
// the exporters. Call it after Serve returns, with a deadline that fits
// what is left of the platform's stop budget: Serve's drain takes up to
// 10s of the activator's 20s.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, f := range t.shutdown {
		errs = append(errs, f(ctx))
	}
	t.shutdown = nil
	return errors.Join(errs...)
}
