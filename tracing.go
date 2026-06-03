package elastic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"gitlab.com/gorib/hilton"
	"gitlab.com/gorib/pry"
)

const (
	instrumentationName = "gitlab.com/gorib/elastic"
	maxStatementSize    = 10240 // 10KB limit for telemetry
)

type Client interface {
	MultiGet(ctx context.Context, index string, query any) ([]byte, error)
	Search(ctx context.Context, index string, query any) ([]byte, error)
	Bulk(ctx context.Context, index string, body []byte) ([]byte, error)
	IndexExists(ctx context.Context, index string) (bool, error)
	IndexCreate(ctx context.Context, index string, mappings any) ([]byte, error)
	DeleteByQuery(ctx context.Context, index string, query any) ([]byte, error)
}

type Option func(*config)

type config struct {
	debugLogger pry.Logger
}

// WithDebugLogger enables verbose request/response logging via pry. Off by
// default — telemetry (spans, metrics) is produced unconditionally from ctx.
func WithDebugLogger(logger pry.Logger) Option {
	return func(c *config) { c.debugLogger = logger }
}

func New(host string, prefix string, opts ...Option) (*traced, error) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	inner, err := newClient(host, prefix, cfg.debugLogger)
	if err != nil {
		return nil, err
	}
	return &traced{inner: inner}, nil
}

type traced struct {
	inner *baseClient
}

func (t *traced) MultiGet(ctx context.Context, index string, query any) ([]byte, error) {
	return execute(ctx, "multiGet", index, query, func(ctx context.Context) ([]byte, error) {
		return t.inner.MultiGet(ctx, index, query)
	})
}

func (t *traced) Search(ctx context.Context, index string, query any) ([]byte, error) {
	return execute(ctx, "search", index, query, func(ctx context.Context) ([]byte, error) {
		return t.inner.Search(ctx, index, query)
	})
}

func (t *traced) Bulk(ctx context.Context, index string, body []byte) ([]byte, error) {
	return execute(ctx, "bulk", index, body, func(ctx context.Context) ([]byte, error) {
		return t.inner.Bulk(ctx, index, body)
	})
}

func (t *traced) IndexExists(ctx context.Context, index string) (bool, error) {
	return execute(ctx, "indexExists", index, nil, func(ctx context.Context) (bool, error) {
		return t.inner.IndexExists(ctx, index)
	})
}

func (t *traced) IndexCreate(ctx context.Context, index string, mappings any) ([]byte, error) {
	return execute(ctx, "indexCreate", index, mappings, func(ctx context.Context) ([]byte, error) {
		return t.inner.IndexCreate(ctx, index, mappings)
	})
}

func (t *traced) DeleteByQuery(ctx context.Context, index string, query any) ([]byte, error) {
	return execute(ctx, "deleteByQuery", index, query, func(ctx context.Context) ([]byte, error) {
		return t.inner.DeleteByQuery(ctx, index, query)
	})
}

func execute[T any](ctx context.Context, op string, index string, statement any, f func(context.Context) (T, error)) (res T, err error) {
	provider := hilton.ProviderFromContext(ctx)
	meter := provider.Meter(instrumentationName)
	duration := meter.Histogram("db.client.operation.duration", hilton.WithUnit("s"))

	ctx, span := provider.Tracer(instrumentationName).Start(ctx, "elastic."+op,
		hilton.WithSpanKind(hilton.SpanKindClient),
		hilton.WithAttributes(
			hilton.Attr("db.system.name", "elasticsearch"),
			hilton.Attr("db.operation.name", op),
			hilton.Attr("db.collection.name", index),
		),
	)

	if statement != nil && span.IsRecording() {
		span.SetAttributes(recordStatement(statement))
	}

	start := time.Now()
	defer func() {
		took := time.Since(start).Seconds()
		attrs := []hilton.Attribute{
			hilton.Attr("db.system.name", "elasticsearch"),
			hilton.Attr("db.operation.name", op),
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(hilton.StatusError, err.Error())
			attrs = append(attrs, hilton.Attr("error.type", elasticErrorType(err)))
		}
		duration.Record(ctx, took, attrs...)
		span.End()
	}()

	return f(ctx)
}

func elasticErrorType(err error) string {
	switch {
	case errors.Is(err, ErrWrongResponse):
		return "wrong_response"
	case errors.Is(err, ErrUnknownResponse):
		return "unknown_response"
	}
	return fmt.Sprintf("%T", err)
}

func recordStatement(stmt any) hilton.Attribute {
	const key = "db.query.text"

	switch v := stmt.(type) {
	case string:
		return hilton.Attr(key, truncate(v))
	case []byte:
		return hilton.Attr(key, truncate(string(v)))
	default:
		r, err := json.Marshal(v)
		if err != nil {
			return hilton.Attr(key, "error_marshaling_body")
		}
		return hilton.Attr(key, truncate(string(r)))
	}
}

func truncate(s string) string {
	if len(s) <= maxStatementSize {
		return s
	}

	limit := maxStatementSize
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}

	return s[:limit] + "... [truncated]"
}
