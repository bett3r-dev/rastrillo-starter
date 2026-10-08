package telemetry

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// DB instruments every statement GORM runs on g, on the global
// providers. See InstrumentDB.
func DB(g *gorm.DB) error { return InstrumentDB(g, nil, nil) }

// InstrumentDB gives every statement on g a client span and a
// db.client.operation.duration sample. The span is a child of the
// request's when the query was given the request's context
// (a.db.G.WithContext(r.Context())), and a trace of its own when not.
//
// Hand-written callbacks rather than gorm.io/plugin/opentelemetry: the
// plugin links the ClickHouse, MySQL and Postgres drivers into the
// binary to sniff the dialect, which an instance would load on every
// cold boot for a database it does not have.
//
// The span carries the SQL with its placeholders and never its values:
// those are people's names, emails and amounts, and they would outlive
// the tenant's own data in the tracing backend.
//
// nil providers mean the globals.
func InstrumentDB(g *gorm.DB, tp trace.TracerProvider, mp metric.MeterProvider) error {
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	tracer := tp.Tracer(Scope)
	duration, err := mp.Meter(Scope).Float64Histogram("db.client.operation.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of database client operations."),
		metric.WithExplicitBucketBoundaries(durationBuckets...))
	if err != nil {
		return err
	}

	type started struct {
		at   time.Time
		span trace.Span
	}
	const key = "telemetry:started"
	before := func(op string) func(*gorm.DB) {
		return func(tx *gorm.DB) {
			ctx := tx.Statement.Context
			if ctx == nil {
				ctx = context.Background()
			}
			_, span := tracer.Start(ctx, op, trace.WithSpanKind(trace.SpanKindClient))
			tx.InstanceSet(key, started{time.Now(), span})
		}
	}
	after := func(op string) func(*gorm.DB) {
		return func(tx *gorm.DB) {
			v, _ := tx.InstanceGet(key)
			s, ok := v.(started)
			if !ok {
				return
			}
			elapsed := time.Since(s.at)
			table := tx.Statement.Table
			attrs := []attribute.KeyValue{
				attribute.String("db.system.name", "sqlite"),
				attribute.String("db.operation.name", op),
				attribute.String("db.collection.name", table),
			}
			failed := tx.Error != nil && !errors.Is(tx.Error, gorm.ErrRecordNotFound)
			if failed {
				attrs = append(attrs, attribute.String("error.type", "error"))
				s.span.RecordError(tx.Error)
				s.span.SetStatus(codes.Error, tx.Error.Error())
			}
			if table != "" {
				s.span.SetName(strings.ToUpper(op) + " " + table)
			}
			s.span.SetAttributes(attrs...)
			s.span.SetAttributes(
				attribute.String("db.query.text", tx.Statement.SQL.String()),
				attribute.Int64("db.response.returned_rows", tx.Statement.RowsAffected),
			)
			s.span.End()
			duration.Record(context.WithoutCancel(tx.Statement.Context), elapsed.Seconds(), metric.WithAttributes(attrs...))
		}
	}

	cb := g.Callback()
	for _, h := range []struct {
		before, after interface {
			Register(string, func(*gorm.DB)) error
		}
		op string
	}{
		{cb.Create().Before("gorm:create"), cb.Create().After("gorm:create"), "insert"},
		{cb.Query().Before("gorm:query"), cb.Query().After("gorm:query"), "select"},
		{cb.Update().Before("gorm:update"), cb.Update().After("gorm:update"), "update"},
		{cb.Delete().Before("gorm:delete"), cb.Delete().After("gorm:delete"), "delete"},
		{cb.Row().Before("gorm:row"), cb.Row().After("gorm:row"), "row"},
		{cb.Raw().Before("gorm:raw"), cb.Raw().After("gorm:raw"), "exec"},
	} {
		if err := h.before.Register("telemetry:before:"+h.op, before(h.op)); err != nil {
			return err
		}
		if err := h.after.Register("telemetry:after:"+h.op, after(h.op)); err != nil {
			return err
		}
	}
	return nil
}

// SQLiteFiles reports the size of the database file and of its
// write-ahead log as sqlite.file.size, labelled file=db|wal. A
// WAL that keeps growing is a checkpoint that is not happening; the
// database's size is the number the instance's disk runs out on.
func SQLiteFiles(path string) error {
	meter := otel.GetMeterProvider().Meter(Scope)
	gauge, err := meter.Int64ObservableGauge("sqlite.file.size",
		metric.WithUnit("By"),
		metric.WithDescription("Size of the SQLite database file and its write-ahead log."))
	if err != nil {
		return err
	}
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for file, p := range map[string]string{"db": path, "wal": path + "-wal"} {
			if fi, err := os.Stat(p); err == nil {
				o.ObserveInt64(gauge, fi.Size(), metric.WithAttributes(attribute.String("file", file)))
			}
		}
		return nil
	}, gauge)
	return err
}
