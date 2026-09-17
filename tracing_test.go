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
		{"no body to read", fmt.Errorf("%w: failed to decode", ErrUnknownResponse), "unknown_response"},
		{"an error status", fmt.Errorf("%w: indices.exists [500]", ErrWrongResponse), "wrong_response"},
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
