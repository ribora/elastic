# gorib/elastic

An Elasticsearch client narrowed to six operations, with tracing and metrics in
front of them and a fixed index prefix behind them.

```go
c, err := elastic.New(host, "prod-", elastic.WithDebugLogger(logger))

raw, err := c.Search(ctx, "products", query)
```

Every method takes an index name without the prefix and returns the raw
response body. Decoding is the caller's: a search response shape depends on the
query, and a generic decode here would either lose information or invent a
model the service does not use.

| Method | Elasticsearch call |
|---|---|
| `Search` | `_search`, always with `track_total_hits` |
| `MultiGet` | `_mget` |
| `Bulk` | `_bulk` |
| `IndexExists` | `HEAD /<index>` |
| `IndexCreate` | `PUT /<index>` with mappings |
| `DeleteByQuery` | `_delete_by_query` |

The prefix passed to `New` is prepended to every index name, which is how one
cluster hosts several environments. It is a plain string concatenation — the
separator, if any, is part of the prefix.

## Errors

Two sentinels, both wrapped with context:

| Error | When |
|---|---|
| `ErrWrongResponse` | Elasticsearch answered with an error status |
| `ErrUnknownResponse` | the error body could not be decoded at all |

When the body decoded, the error is a `*ResponseError` carrying `Status`,
`Type` and `Reason` as fields. It matches `errors.Is(err, ErrWrongResponse)`
like any other, and `Type` is Elasticsearch's own name for the failure
(`index_not_found_exception`, `version_conflict_engine_exception`), which is
what the `error.type` metric attribute reports:

```go
var re *elastic.ResponseError
if errors.As(err, &re) && re.Type == "version_conflict_engine_exception" {
    // retry
}
```

`IndexExists` is the exception: a 404 is `false, nil`, not an error, because
"the index is not there" is the question being asked.

## Telemetry

Every operation is wrapped in a `CLIENT` span named `elastic.<op>` and recorded
into `db.client.operation.duration` (seconds):

| Attribute | Value |
|---|---|
| `db.system.name` | `elasticsearch` |
| `db.operation.name` | `search`, `bulk`, … |
| `db.collection.name` | the index, on the span |
| `error.type` | `wrong_response`, `unknown_response`, or the Go type |

The provider comes from the context per call, and instruments are cached
against it by `hilton.NewInstruments` rather than rebuilt per query.

The query is recorded as `db.query.text` — but only when the span is actually
recording, and truncated to 10 KB on a rune boundary with a `... [truncated]`
marker. A bulk body of a few thousand documents would otherwise be attached to
a span in full.

## Debug logging

`WithDebugLogger` installs a round-trip logger that records method, url,
duration, status and **both bodies**, at debug level (error level for a failed
round trip). Bodies are read and restored, so nothing downstream notices.

It is off by default and should stay off outside development: it duplicates
every request and response into the log. Telemetry is unconditional and does
not depend on it.

## Wiring

waffle registers `elastic.Client` under `config/service/elastic` from `ES_HOST`
and `ES_INDEX` (the prefix), with the debug logger attached — a `di.Fallback()`
registration, so a service that wants different options declares its own.

## What it does not do

- No index management beyond create and exists — no aliases, no templates, no
  refresh, no reindex.
- No document-level API: no single `Get`, `Index` or `Update`; write through
  `Bulk`.
- No pagination, scroll or PIT handling.
- No retry or backoff, and no connection pooling configuration — a single host,
  the defaults of `go-elasticsearch`.
- No authentication options: if the cluster needs credentials, they belong in
  the URL and the client here does not help you keep them out of logs.
