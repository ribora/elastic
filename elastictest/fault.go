package elastictest

import (
	"context"
	"net"
	"os"
	"sync/atomic"
	"syscall"

	"github.com/ribora/elastic"
)

// What a server that went away looks like from the client.
var errRefused error = &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}

type fault struct {
	err atomic.Pointer[error]
}

func (f *fault) fail(err error) {
	if err == nil {
		err = errRefused
	}
	f.err.Store(&err)
}

func (f *fault) heal() { f.err.Store(nil) }

func (f *fault) current() error {
	if err := f.err.Load(); err != nil {
		return *err
	}
	return nil
}

type faultyClient struct {
	inner elastic.Client
	fault *fault
}

func (c *faultyClient) MultiGet(ctx context.Context, index string, query any) ([]byte, error) {
	if err := c.fault.current(); err != nil {
		return nil, err
	}
	return c.inner.MultiGet(ctx, index, query)
}

func (c *faultyClient) Search(ctx context.Context, index string, query any) ([]byte, error) {
	if err := c.fault.current(); err != nil {
		return nil, err
	}
	return c.inner.Search(ctx, index, query)
}

func (c *faultyClient) Bulk(ctx context.Context, index string, body []byte) ([]byte, error) {
	if err := c.fault.current(); err != nil {
		return nil, err
	}
	return c.inner.Bulk(ctx, index, body)
}

func (c *faultyClient) IndexExists(ctx context.Context, index string) (bool, error) {
	if err := c.fault.current(); err != nil {
		return false, err
	}
	return c.inner.IndexExists(ctx, index)
}

func (c *faultyClient) IndexCreate(ctx context.Context, index string, mappings any) ([]byte, error) {
	if err := c.fault.current(); err != nil {
		return nil, err
	}
	return c.inner.IndexCreate(ctx, index, mappings)
}

func (c *faultyClient) IndexDelete(ctx context.Context, index string) ([]byte, error) {
	if err := c.fault.current(); err != nil {
		return nil, err
	}
	return c.inner.IndexDelete(ctx, index)
}

func (c *faultyClient) DeleteByQuery(ctx context.Context, index string, query any) ([]byte, error) {
	if err := c.fault.current(); err != nil {
		return nil, err
	}
	return c.inner.DeleteByQuery(ctx, index, query)
}
