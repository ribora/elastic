package elastic

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"gitlab.com/gorib/hilton"
)

// An unbounded value here is what breaks the histogram.
func TestElasticErrorType(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			"elasticsearch named it",
			&ResponseError{Status: "404 Not Found", Type: "index_not_found_exception", Reason: "no such index [orders]"},
			"index_not_found_exception",
		},
		{
			"wrapped, still named",
			fmt.Errorf("search: %w", &ResponseError{Status: "400", Type: "mapper_parsing_exception"}),
			"mapper_parsing_exception",
		},
		{
			"a body with no type",
			&ResponseError{Status: "503 Service Unavailable"},
			"wrong_response",
		},
		{"no body to read", fmt.Errorf("%w: failed to decode", ErrUnknownResponse), "unknown_response"},
		{"an error status with no body", fmt.Errorf("%w: indices.exists [500]", ErrWrongResponse), "wrong_response"},
		{"transport", errors.New("dial tcp 10.0.0.5:9200: connect: refused"), otherErrorType},
		{"cancelled", context.Canceled, otherErrorType},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := elasticErrorType(c.err); got != c.want {
				t.Errorf("elasticErrorType(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// The reason carries the index name and the offending document.
func TestReasonDoesNotSplitTheSeries(t *testing.T) {
	first := elasticErrorType(&ResponseError{Type: "version_conflict_engine_exception", Reason: "[doc-1]"})
	second := elasticErrorType(&ResponseError{Type: "version_conflict_engine_exception", Reason: "[doc-2]"})
	if first != second {
		t.Errorf("%q != %q", first, second)
	}
}

// Callers match on the sentinel.
func TestResponseErrorIsTheSentinel(t *testing.T) {
	err := error(&ResponseError{Status: "404 Not Found", Type: "index_not_found_exception", Reason: "no such index"})
	if !errors.Is(err, ErrWrongResponse) {
		t.Error("errors.Is(err, ErrWrongResponse) = false")
	}
	want := "wrong elastic response: [404 Not Found] {index_not_found_exception}: {no such index}"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

// An empty field keeps the metric off a bucket that means nothing, but a
// reader still gets the word the message always carried.
func TestResponseErrorReadsUnknownWhenTheBodySaidNothing(t *testing.T) {
	err := error(&ResponseError{Status: "503 Service Unavailable"})
	want := "wrong elastic response: [503 Service Unavailable] {unknown}: {unknown}"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
	if got := elasticErrorType(err); got != "wrong_response" {
		t.Errorf("error.type = %q, want wrong_response", got)
	}
}

func recordedErrorType(t *testing.T, err error) any {
	t.Helper()

	rec := hilton.Recorder()
	ctx := hilton.ContextWithProvider(context.Background(), rec)
	_, _ = execute(ctx, "search", "orders", nil, func(context.Context) ([]byte, error) {
		return nil, err
	})

	for _, m := range rec.Measurements() {
		if m.Instrument != "db.client.operation.duration" {
			continue
		}
		for _, a := range m.Attributes {
			if a.Key() == "error.type" {
				return a.Value()
			}
		}
		return nil
	}
	t.Fatal("db.client.operation.duration was never recorded")
	return nil
}

// A key present on every success is a key nobody can group by.
func TestSuccessCarriesNoErrorType(t *testing.T) {
	if got := recordedErrorType(t, nil); got != nil {
		t.Errorf("error.type = %v, want it absent", got)
	}
}

func TestErrorTypeReachesTheHistogram(t *testing.T) {
	err := &ResponseError{Status: "404 Not Found", Type: "index_not_found_exception"}
	if got := recordedErrorType(t, err); got != any("index_not_found_exception") {
		t.Errorf("error.type = %v, want index_not_found_exception", got)
	}
}
