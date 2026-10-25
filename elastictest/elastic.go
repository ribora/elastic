// Package elastictest runs a component test against a real elasticsearch.
// See github.com/specrig/specrig-go/waffle/docs/harness.md.
package elastictest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gitlab.com/gorib/elastic"
)

// Measured at ~15 s on a laptop.
const startupTimeout = 2 * time.Minute

type Option func(*Elastic)

func Url(address string) Option {
	return func(e *Elastic) { e.url = address }
}

// First non-empty wins.
func Env(keys ...string) Option {
	return func(e *Elastic) { e.envKeys = append(e.envKeys, keys...) }
}

// A container that builds its own client instead of taking Client has to pass
// the Prefix method's answer, or the resource manages indices the service never
// reads.
func Prefix(prefix string) Option {
	return func(e *Elastic) { e.prefix = prefix }
}

// The process id in the prefix is what lets `go test ./...` run two packages
// at once. So the prefix given is not the prefix used.
func Ephemeral() Option {
	return func(e *Elastic) { e.ephemeral = true }
}

// mappings is the whole create-index body, as the service passes it to
// IndexCreate.
func Index(name string, mappings any) Option {
	return func(e *Elastic) { e.indices = append(e.indices, index{name: name, mappings: mappings}) }
}

func Timeout(timeout time.Duration) Option {
	return func(e *Elastic) { e.client.Timeout = timeout }
}

func New(opts ...Option) *Elastic {
	e := &Elastic{client: &http.Client{Timeout: 30 * time.Second}}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

type index struct {
	name     string
	mappings any
}

type Elastic struct {
	url       string
	envKeys   []string
	prefix    string
	indices   []index
	ephemeral bool
	container *container
	// Not container != nil: with Env and Docker both set, CI resolves on the env
	// branch and nothing is coming up to wait for.
	started bool
	base    string // the prefix before the process id, for the sweep

	client *http.Client
	search elastic.Client
	fault  fault
}

func (e *Elastic) Name() string { return "elasticsearch" }

// The service's own constructor: a client built another way here would put the
// harness's idea of a connection under the test instead of the service's.
func (e *Elastic) Client() elastic.Client {
	if e.search == nil {
		return nil
	}
	return &faultyClient{inner: e.search, fault: &e.fault}
}

// Every Client handed out fails with err until Reset; nil reads as a refused connection.
func (e *Elastic) FailWith(err error) { e.fault.fail(err) }

func (e *Elastic) Provision(ctx context.Context) error {
	if err := e.resolve(ctx); err != nil {
		return err
	}
	if err := e.await(ctx); err != nil {
		return err
	}
	e.personalize()
	if err := e.sweep(ctx); err != nil {
		return err
	}
	search, err := elastic.New(e.url, e.prefix)
	if err != nil {
		return fmt.Errorf("cannot build a client for %s: %w", address(e.url), err)
	}
	e.search = search
	return nil
}

// An existing index is left alone: a mapping change is a migration of its own,
// and guessing at one silently is worse than failing on the first query.
func (e *Elastic) Migrate(ctx context.Context) error {
	for _, idx := range e.indices {
		name := e.prefix + idx.name
		exists, err := e.exists(ctx, name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err = e.request(ctx, http.MethodPut, "/"+name, idx.mappings); err != nil {
			return fmt.Errorf("cannot create index %q: %w", name, err)
		}
	}
	return nil
}

// Documents go, the mapping stays. Without refresh=true the next scenario can
// still read these rows.
func (e *Elastic) Reset(ctx context.Context) error {
	e.fault.heal()
	for _, idx := range e.indices {
		name := e.prefix + idx.name
		path := "/" + name + "/_delete_by_query?refresh=true&conflicts=proceed"
		body := map[string]any{"query": map[string]any{"match_all": map[string]any{}}}
		if _, err := e.request(ctx, http.MethodPost, path, body); err != nil {
			return fmt.Errorf("cannot empty index %q: %w", name, err)
		}
	}
	return nil
}

// Elasticsearch is near-real-time: a fixture that seeds through Bulk and then
// searches finds nothing without this, intermittently.
func (e *Elastic) Refresh(ctx context.Context) error {
	if len(e.indices) == 0 {
		return nil
	}
	names := make([]string, 0, len(e.indices))
	for _, idx := range e.indices {
		names = append(names, e.prefix+idx.name)
	}
	path := "/" + strings.Join(names, ",") + "/_refresh"
	if _, err := e.request(ctx, http.MethodPost, path, nil); err != nil {
		return fmt.Errorf("cannot refresh %s: %w", strings.Join(names, ", "), err)
	}
	return nil
}

// The container, if one was started, is left running: starting it is the only
// part that costs anything, and the next run reuses it.
func (e *Elastic) Down(ctx context.Context) error {
	if !e.ephemeral || e.search == nil || len(e.indices) == 0 {
		return nil
	}
	// Teardown outlives what triggered it: a cancelled run is exactly when the
	// indices have to go. The client timeout still bounds the request.
	ctx = context.WithoutCancel(ctx)

	names := make([]string, 0, len(e.indices))
	for _, idx := range e.indices {
		names = append(names, e.prefix+idx.name)
	}
	path := "/" + strings.Join(names, ",") + "?ignore_unavailable=true"
	if _, err := e.request(ctx, http.MethodDelete, path, nil); err != nil {
		return fmt.Errorf("cannot delete %s: %w", strings.Join(names, ", "), err)
	}
	return nil
}

func (e *Elastic) Url() string { return e.url }

// With Ephemeral, known only after Provision.
func (e *Elastic) Prefix() string { return e.prefix }

// Explicit, environment, container — the container last, so CI and anyone with
// their own server never start one.
func (e *Elastic) resolve(ctx context.Context) error {
	if e.url != "" {
		return nil
	}
	for _, key := range e.envKeys {
		if value := os.Getenv(key); value != "" {
			e.url = value
			return nil
		}
	}
	if e.container != nil {
		if err := e.container.ensure(ctx); err != nil {
			return err
		}
		e.started = true
		e.url = e.container.url()
		return nil
	}
	if len(e.envKeys) == 0 {
		return errors.New("elastictest: no address given, pass elastictest.Url, elastictest.Env or elastictest.Docker")
	}
	return fmt.Errorf("elastictest: no address given, set %s", strings.Join(e.envKeys, " or "))
}

// The wait is only for a container this run started. An address that was
// already there — CI, or a server of your own — fails now rather than in two
// minutes.
func (e *Elastic) await(ctx context.Context) error {
	if !e.started {
		_, err := e.request(ctx, http.MethodGet, "/", nil)
		return err
	}
	deadline := time.Now().Add(startupTimeout)
	for {
		_, err := e.request(ctx, http.MethodGet, "/_cluster/health?wait_for_status=yellow&timeout=1s", nil)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the container %q did not turn yellow within %s: %w",
				e.container.name, startupTimeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (e *Elastic) exists(ctx context.Context, name string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimSuffix(e.url, "/")+"/"+name, nil)
	if err != nil {
		return false, err
	}
	response, err := e.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("no elasticsearch at %s: %w", address(e.url), err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)

	switch {
	case response.StatusCode == http.StatusNotFound:
		return false, nil
	case response.StatusCode < 300:
		return true, nil
	default:
		return false, fmt.Errorf("cannot tell whether index %q exists: %s", name, response.Status)
	}
}

// HTTP and not elastic.Client: emptying and seeding both need ?refresh.
func (e *Elastic) request(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(e.url, "/")+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := e.client.Do(request)
	if err != nil {
		// Same shape as Provision and exists: net/http strips the password, but
		// not the user or the path.
		return nil, fmt.Errorf("no elasticsearch at %s: %w", address(e.url), err)
	}
	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", response.Status, reason(data))
	}
	return data, nil
}

// Keeps type and reason, drops the rest — mostly the query that failed.
func reason(data []byte) string {
	var parsed struct {
		Error struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil || parsed.Error.Type == "" {
		return strings.TrimSpace(string(data))
	}
	return parsed.Error.Type + ": " + parsed.Error.Reason
}

// Host only: the URL can carry basic auth, and errors reach logs and CI output.
func address(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "the configured address"
	}
	return parsed.Host
}
