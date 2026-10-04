package elastictest

import (
	"strings"
	"testing"
	"time"
)

func TestEnvWinsOverTheContainer(t *testing.T) {
	t.Setenv("TEST_ES", "http://from-env:9200")
	subject := New(Env("TEST_ES"), Docker("elasticsearch:8.19.7"))

	if err := subject.resolve(t.Context()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if subject.Url() != "http://from-env:9200" || subject.started {
		t.Fatalf("url: %s, started: %v", subject.Url(), subject.started)
	}
}

func TestUrlWinsOverTheContainer(t *testing.T) {
	subject := New(Docker("elasticsearch:8.19.7"), Url("http://explicit:9200"))

	if err := subject.resolve(t.Context()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if subject.Url() != "http://explicit:9200" || subject.started {
		t.Fatalf("url: %s, started: %v", subject.Url(), subject.started)
	}
}

func TestDefaultsAreSharedAcrossServices(t *testing.T) {
	first := New(Docker("elasticsearch:8.19.7"), Prefix("catalog_"))
	second := New(Docker("elasticsearch:8.19.7"), Prefix("search_"))

	if *first.container != *second.container {
		t.Fatalf("containers differ: %+v and %+v", first.container, second.container)
	}
	if first.container.name != defaultContainer || first.container.port != defaultPort {
		t.Fatalf("container: %+v", first.container)
	}
}

func TestContainerOptionsOverrideTheDefaults(t *testing.T) {
	subject := New(Docker("elasticsearch:7.17.29", Container("sherpa-es"), Port(19200), Runtime("podman")))

	want := container{image: "elasticsearch:7.17.29", name: "sherpa-es", port: 19200, runtime: "podman"}
	if *subject.container != want {
		t.Fatalf("container: %+v", subject.container)
	}
}

func TestContainerUrlIsLoopback(t *testing.T) {
	c := &container{port: 59200}

	if c.url() != "http://127.0.0.1:59200" {
		t.Fatalf("url: %s", c.url())
	}
}

func TestMissingRuntimePointsAtTheOtherOptions(t *testing.T) {
	c := &container{runtime: "definitely-not-a-container-runtime"}

	_, err := c.binary()

	if err == nil {
		t.Fatal("binary: want a failure")
	}
	for _, want := range []string{"definitely-not-a-container-runtime", "elastictest.Url"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
}

// The documented configuration lists both, and CI resolves on the env branch:
// waiting for a container nobody started would blame it for an absent server.
func TestAGivenAddressIsAskedOnceAndNotWaitedFor(t *testing.T) {
	t.Setenv("TEST_ES", "http://127.0.0.1:1")
	subject := New(Env("TEST_ES"), Docker("elasticsearch:8.19.7"))
	if err := subject.resolve(t.Context()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	started := time.Now()
	err := subject.await(t.Context())

	if err == nil {
		t.Fatal("await: want a failure")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("await retried a given address for %s", time.Since(started))
	}
}
