package elastictest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type call struct {
	method string
	path   string
	query  string
	body   string
}

type fake struct {
	mu      sync.Mutex
	calls   []call
	missing bool // HEAD on an index answers 404
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	f.mu.Lock()
	f.calls = append(f.calls, call{
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		body:   string(body),
	})
	missing := f.missing
	f.mu.Unlock()

	if r.Method == http.MethodHead && missing {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte(`{}`))
}

func (f *fake) recorded() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func server(t *testing.T, handler http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestProvisionChecksThatSomebodyAnswers(t *testing.T) {
	elastic := &fake{}
	subject := New(Url(server(t, elastic)))

	if err := subject.Provision(t.Context()); err != nil {
		t.Fatalf("provision: %v", err)
	}

	calls := elastic.recorded()
	if len(calls) != 1 || calls[0].method != http.MethodGet || calls[0].path != "/" {
		t.Fatalf("calls: %+v", calls)
	}
}

func TestProvisionFailsLoudlyWhenNobodyAnswers(t *testing.T) {
	subject := New(Url("http://127.0.0.1:1"))

	err := subject.Provision(t.Context())

	if err == nil {
		t.Fatal("provision: want a failure")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("provision: %v", err)
	}
}

func TestMigrateCreatesAMissingIndexWithItsMappings(t *testing.T) {
	elastic := &fake{missing: true}
	mappings := map[string]any{"mappings": map[string]any{"properties": map[string]any{"sku": map[string]any{"type": "keyword"}}}}
	subject := New(Url(server(t, elastic)), Prefix("test_"), Index("product", mappings))

	if err := subject.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	calls := elastic.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls: %+v", calls)
	}
	if calls[0].method != http.MethodHead || calls[0].path != "/test_product" {
		t.Fatalf("existence check: %+v", calls[0])
	}
	if calls[1].method != http.MethodPut || calls[1].path != "/test_product" {
		t.Fatalf("creation: %+v", calls[1])
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(calls[1].body), &sent); err != nil {
		t.Fatalf("body: %v", err)
	}
	if _, ok := sent["mappings"]; !ok {
		t.Fatalf("the create body lost the mappings: %s", calls[1].body)
	}
}

func TestMigrateLeavesAnExistingIndexAlone(t *testing.T) {
	elastic := &fake{}
	subject := New(Url(server(t, elastic)), Index("product", map[string]any{}))

	if err := subject.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	calls := elastic.recorded()
	if len(calls) != 1 || calls[0].method != http.MethodHead {
		t.Fatalf("calls: %+v", calls)
	}
}

func TestResetEmptiesTheIndexAndWaitsForIt(t *testing.T) {
	elastic := &fake{}
	subject := New(Url(server(t, elastic)), Prefix("test_"), Index("product", nil))

	if err := subject.Reset(t.Context()); err != nil {
		t.Fatalf("reset: %v", err)
	}

	calls := elastic.recorded()
	if len(calls) != 1 {
		t.Fatalf("calls: %+v", calls)
	}
	if calls[0].method != http.MethodPost || calls[0].path != "/test_product/_delete_by_query" {
		t.Fatalf("reset: %+v", calls[0])
	}
	for _, want := range []string{"refresh=true", "conflicts=proceed"} {
		if !strings.Contains(calls[0].query, want) {
			t.Errorf("query %q is missing %q", calls[0].query, want)
		}
	}
	if !strings.Contains(calls[0].body, "match_all") {
		t.Errorf("body: %s", calls[0].body)
	}
}

func TestRefreshAsksForEveryIndexAtOnce(t *testing.T) {
	elastic := &fake{}
	subject := New(Url(server(t, elastic)), Prefix("test_"), Index("product", nil), Index("tile", nil))

	if err := subject.Refresh(t.Context()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	calls := elastic.recorded()
	if len(calls) != 1 || calls[0].path != "/test_product,test_tile/_refresh" {
		t.Fatalf("calls: %+v", calls)
	}
}

func TestRefreshWithoutIndicesDoesNothing(t *testing.T) {
	elastic := &fake{}
	subject := New(Url(server(t, elastic)))

	if err := subject.Refresh(t.Context()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(elastic.recorded()) != 0 {
		t.Fatalf("calls: %+v", elastic.recorded())
	}
}

func TestServerErrorKeepsTypeAndReason(t *testing.T) {
	elastic := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"resource_already_exists_exception","reason":"index [test_product] already exists","root_cause":[]}}`))
	})
	subject := New(Url(server(t, elastic)), Prefix("test_"), Index("product", nil))

	err := subject.Reset(t.Context())

	if err == nil {
		t.Fatal("reset: want a failure")
	}
	for _, want := range []string{"resource_already_exists_exception", "already exists"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "root_cause") {
		t.Errorf("the whole response leaked into the error: %v", err)
	}
}

func TestUrlWinsOverTheEnvironment(t *testing.T) {
	t.Setenv("TEST_ES", "http://from-env:9200")
	subject := New(Env("TEST_ES"), Url("http://explicit:9200"))

	if err := subject.resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if subject.Url() != "http://explicit:9200" {
		t.Fatalf("url: %s", subject.Url())
	}
}

func TestEnvKeysAreTriedInOrder(t *testing.T) {
	t.Setenv("SECOND_ES", "http://second:9200")
	subject := New(Env("FIRST_ES", "SECOND_ES"))

	if err := subject.resolve(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if subject.Url() != "http://second:9200" {
		t.Fatalf("url: %s", subject.Url())
	}
}

func TestMissingAddressNamesTheVariables(t *testing.T) {
	err := New(Env("MERCATOR_TEST_ES", "FALLBACK_ES")).resolve()

	if err == nil || !strings.Contains(err.Error(), "MERCATOR_TEST_ES or FALLBACK_ES") {
		t.Fatalf("resolve: %v", err)
	}
}

func TestAddressDropsTheCredentials(t *testing.T) {
	reported := address("http://elastic:fake-pw@127.0.0.1:19200")

	if reported != "127.0.0.1:19200" {
		t.Fatalf("address: %s", reported)
	}
}

func TestTrailingSlashInTheAddressDoesNotDoubleUp(t *testing.T) {
	elastic := &fake{}
	subject := New(Url(server(t, elastic)+"/"), Index("product", nil))

	if err := subject.Reset(t.Context()); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if path := elastic.recorded()[0].path; path != "/product/_delete_by_query" {
		t.Fatalf("path: %s", path)
	}
}
