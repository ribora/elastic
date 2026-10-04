package elastictest

import (
	"errors"
	"syscall"
	"testing"
)

func TestFailWithReachesAClientHandedOutBeforeAndNeverTheServer(t *testing.T) {
	elastic := &fake{}
	subject := New(Url(server(t, elastic)))
	if err := subject.Provision(t.Context()); err != nil {
		t.Fatalf("provision: %v", err)
	}
	client := subject.Client()
	before := len(elastic.recorded())
	gone := errors.New("gone")

	subject.FailWith(gone)

	if _, err := client.Search(t.Context(), "product", map[string]any{}); !errors.Is(err, gone) {
		t.Fatalf("search: %v, want %v", err, gone)
	}
	if _, err := client.IndexExists(t.Context(), "product"); !errors.Is(err, gone) {
		t.Fatalf("index exists: %v, want %v", err, gone)
	}
	if calls := elastic.recorded(); len(calls) != before {
		t.Fatalf("a failed client reached the server: %+v", calls[before:])
	}
}

func TestFailWithNilRefusesTheConnection(t *testing.T) {
	subject := New(Url(server(t, &fake{})))
	if err := subject.Provision(t.Context()); err != nil {
		t.Fatalf("provision: %v", err)
	}

	subject.FailWith(nil)

	if _, err := subject.Client().Bulk(t.Context(), "product", nil); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("bulk: %v, want a refused connection", err)
	}
}

func TestResetHeals(t *testing.T) {
	subject := New(Url(server(t, &fake{})))
	if err := subject.Provision(t.Context()); err != nil {
		t.Fatalf("provision: %v", err)
	}
	subject.FailWith(nil)

	if err := subject.Reset(t.Context()); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := subject.Client().Search(t.Context(), "product", map[string]any{}); errors.Is(err, errRefused) {
		t.Fatalf("search after reset: %v", err)
	}
}
