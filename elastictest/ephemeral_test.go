package elastictest

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func provisioned(t *testing.T, elastic *fake, opts ...Option) *Elastic {
	t.Helper()
	subject := New(append([]Option{Url(server(t, elastic))}, opts...)...)
	if err := subject.Provision(t.Context()); err != nil {
		t.Fatalf("provision: %v", err)
	}
	return subject
}

func TestEphemeralPrefixCarriesTheProcessId(t *testing.T) {
	subject := provisioned(t, &fake{}, Prefix("test_"), Ephemeral(), Index("product", nil))

	if want := "test_" + strconv.Itoa(os.Getpid()) + "_"; subject.Prefix() != want {
		t.Fatalf("prefix: %s, want %s", subject.Prefix(), want)
	}
}

func TestWithoutEphemeralThePrefixIsTakenAsGiven(t *testing.T) {
	subject := provisioned(t, &fake{}, Prefix("test_"), Index("product", nil))

	if subject.Prefix() != "test_" {
		t.Fatalf("prefix: %s", subject.Prefix())
	}
}

func TestMigrateCreatesTheIndexUnderTheRunsPrefix(t *testing.T) {
	elastic := &fake{missing: true}
	subject := provisioned(t, elastic, Prefix("test_"), Ephemeral(), Index("product", nil))

	if err := subject.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	calls := elastic.recorded()
	last := calls[len(calls)-1]
	if last.method != http.MethodPut || last.path != "/"+subject.Prefix()+"product" {
		t.Fatalf("creation: %+v", last)
	}
}

func TestDownDeletesThisRunsIndices(t *testing.T) {
	elastic := &fake{}
	subject := provisioned(t, elastic, Prefix("test_"), Ephemeral(), Index("product", nil), Index("tile", nil))
	before := len(elastic.recorded())

	if err := subject.Down(t.Context()); err != nil {
		t.Fatalf("down: %v", err)
	}

	calls := elastic.recorded()[before:]
	prefix := subject.Prefix()
	if len(calls) != 1 || calls[0].method != http.MethodDelete || calls[0].path != "/"+prefix+"product,"+prefix+"tile" {
		t.Fatalf("calls: %+v", calls)
	}
	if calls[0].query != "ignore_unavailable=true" {
		t.Fatalf("query: %s", calls[0].query)
	}
}

func TestDownWithoutEphemeralLeavesTheIndices(t *testing.T) {
	elastic := &fake{}
	subject := provisioned(t, elastic, Index("product", nil))
	before := len(elastic.recorded())

	if err := subject.Down(t.Context()); err != nil {
		t.Fatalf("down: %v", err)
	}
	if calls := elastic.recorded()[before:]; len(calls) != 0 {
		t.Fatalf("calls: %+v", calls)
	}
}

func TestDownBeforeProvisionDoesNothing(t *testing.T) {
	subject := New(Env("UNSET_TEST_ES"), Ephemeral(), Index("product", nil))

	if err := subject.Down(t.Context()); err != nil {
		t.Fatalf("down: %v", err)
	}
}

func TestSweepTakesOnlyADeadRunsOldIndex(t *testing.T) {
	dead := deadPid(t)
	old := time.Now().Add(-2 * sweepAfter).UnixMilli()
	fresh := time.Now().UnixMilli()
	row := func(name string, created int64) string {
		return fmt.Sprintf(`{"index":%q,"creation.date":"%d"}`, name, created)
	}
	elastic := &fake{listing: "[" + strings.Join([]string{
		row(fmt.Sprintf("test_%d_product", dead), old),
		row(fmt.Sprintf("test_%d_product", dead), fresh),
		row(fmt.Sprintf("test_%d_product", os.Getppid()), old),
		row(fmt.Sprintf("test_%d_product", os.Getpid()), old),
		row(fmt.Sprintf("test_%d_sales", dead), old),
		row("test_product", old),
	}, ",") + "]"}

	provisioned(t, elastic, Prefix("test_"), Ephemeral(), Index("product", nil))

	var deleted []call
	for _, c := range elastic.recorded() {
		if c.method == http.MethodDelete {
			deleted = append(deleted, c)
		}
	}
	if want := fmt.Sprintf("/test_%d_product", dead); len(deleted) != 1 || deleted[0].path != want {
		t.Fatalf("deleted: %+v, want only %s", deleted, want)
	}
}

func TestSweepLeavesARemoteClusterAlone(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://127.0.0.1:9200":      true,
		"http://localhost:9200":      true,
		"http://[::1]:9200":          true,
		"http://elasticsearch:9200":  false,
		"https://es.internal:443":    false,
		"http://user:pw@10.0.0.3:92": false,
	} {
		if got := loopback(raw); got != want {
			t.Errorf("loopback(%s) = %v, want %v", raw, got, want)
		}
	}
}

func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("true: %v", err)
	}
	return cmd.Process.Pid
}
