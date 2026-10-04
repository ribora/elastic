package elastictest

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// One container per repository, like postgres: a start takes ~15 s and an index
// ~0.15 s, so the container is kept and the indices are what a run throws away.
const (
	defaultContainer = "gorib-harness-es"
	defaultPort      = 59200

	runtimeTimeout = 30 * time.Second
	// The image is over half a gigabyte, so a first pull inside runtimeTimeout fails.
	pullTimeout = 10 * time.Minute
)

// Last resort, after Url and Env. Security off, so no password exists to keep
// anywhere; a binary rather than testcontainers, to stay out of go.mod.
func Docker(image string, opts ...DockerOption) Option {
	return func(e *Elastic) {
		c := &container{image: image, name: defaultContainer, port: defaultPort}
		for _, opt := range opts {
			opt(c)
		}
		e.container = c
	}
}

type DockerOption func(*container)

func Container(name string) DockerOption {
	return func(c *container) { c.name = name }
}

func Port(port int) DockerOption {
	return func(c *container) { c.port = port }
}

// For a machine with both docker and podman.
func Runtime(binary string) DockerOption {
	return func(c *container) { c.runtime = binary }
}

type container struct {
	image   string
	name    string
	port    int
	runtime string
}

// Does not wait for the cluster to turn yellow: that is the caller's loop,
// which has the HTTP client.
func (c *container) ensure(ctx context.Context) error {
	binary, err := c.binary()
	if err != nil {
		return err
	}

	runtimeCtx, cancel := context.WithTimeout(ctx, runtimeTimeout)
	defer cancel()
	switch running, found := c.state(runtimeCtx, binary); {
	case !found:
		if err = c.pull(ctx, binary); err != nil {
			return err
		}
		return c.start(runtimeCtx, binary)
	case !running:
		if out, err := exec.CommandContext(runtimeCtx, binary, "start", c.name).CombinedOutput(); err != nil {
			return fmt.Errorf("cannot start the container %q: %w: %s", c.name, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func (c *container) pull(ctx context.Context, binary string) error {
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	if exec.CommandContext(ctx, binary, "image", "inspect", c.image).Run() == nil {
		return nil
	}
	if out, err := exec.CommandContext(ctx, binary, "pull", c.image).CombinedOutput(); err != nil {
		return fmt.Errorf("cannot pull %s: %w: %s", c.image, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *container) start(ctx context.Context, binary string) error {
	// Loopback only: with security off, publishing on every interface would be
	// an open elasticsearch on whatever network the laptop is on.
	out, err := exec.CommandContext(ctx, binary, "run", "-d",
		"--name", c.name,
		"-p", "127.0.0.1:"+strconv.Itoa(c.port)+":9200",
		"-e", "discovery.type=single-node",
		"-e", "xpack.security.enabled=false",
		// The default heap is half of what the runtime's VM has.
		"-e", "ES_JAVA_OPTS=-Xms512m -Xmx512m",
		// On a nearly full laptop disk the indices turn read-only mid-run.
		"-e", "cluster.routing.allocation.disk.threshold_enabled=false",
		c.image,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot start %s as %q on port %d: %w: %s",
			c.image, c.name, c.port, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c *container) state(ctx context.Context, binary string) (running, found bool) {
	out, err := exec.CommandContext(ctx, binary, "inspect", "-f", "{{.State.Running}}", c.name).Output()
	if err != nil {
		return false, false
	}
	return strings.TrimSpace(string(out)) == "true", true
}

func (c *container) binary() (string, error) {
	candidates := []string{"docker", "podman"}
	if c.runtime != "" {
		candidates = []string{c.runtime}
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("elastictest: no container runtime found (%s), pass elastictest.Url or elastictest.Env instead",
		strings.Join(candidates, " or "))
}

func (c *container) url() string {
	return "http://127.0.0.1:" + strconv.Itoa(c.port)
}
