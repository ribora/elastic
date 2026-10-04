package elastictest

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// For runs that were killed before Down. Generous because a process id can be
// another namespace's: "dead and older than this" must be impossible for a
// merely slow run.
const sweepAfter = time.Hour

func (e *Elastic) personalize() {
	if !e.ephemeral || e.base != "" {
		return
	}
	e.base = e.prefix
	e.prefix = e.base + strconv.Itoa(os.Getpid()) + "_"
}

// Down does not run on Ctrl+C, a test timeout or a cancelled CI job. Elastic
// tracks no connections per index, so the process id in the name stands in for
// one — and it is only comparable on this machine.
func (e *Elastic) sweep(ctx context.Context) error {
	if !e.ephemeral || len(e.indices) == 0 || !loopback(e.url) {
		return nil
	}
	data, err := e.request(ctx, http.MethodGet, "/_cat/indices/"+e.base+"*?format=json&h=index,creation.date", nil)
	if err != nil {
		return err
	}
	var listed []struct {
		Index   string `json:"index"`
		Created string `json:"creation.date"`
	}
	if err = json.Unmarshal(data, &listed); err != nil {
		return err
	}

	var stale []string
	for _, idx := range listed {
		pid, ok := e.owner(idx.Index)
		if !ok || pid == os.Getpid() {
			continue
		}
		created, err := strconv.ParseInt(idx.Created, 10, 64)
		if err != nil || time.Since(time.UnixMilli(created)) < sweepAfter || alive(pid) {
			continue
		}
		stale = append(stale, idx.Index)
	}
	if len(stale) > 0 {
		// Best effort: a concurrent sweep may have taken them already.
		_, _ = e.request(ctx, http.MethodDelete, "/"+strings.Join(stale, ",")+"?ignore_unavailable=true", nil)
	}
	return nil
}

// Only the declared names: another service's index can share the base prefix
// and happen to continue with digits.
func (e *Elastic) owner(name string) (int, bool) {
	rest, found := strings.CutPrefix(name, e.base)
	if !found {
		return 0, false
	}
	digits, tail, found := strings.Cut(rest, "_")
	if !found || !slices.ContainsFunc(e.indices, func(i index) bool { return i.name == tail }) {
		return 0, false
	}
	pid, err := strconv.Atoi(digits)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func alive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH)
}

func loopback(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
