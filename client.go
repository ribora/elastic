package elastic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"

	"gitlab.com/gorib/pry"
)

func newClient(host string, prefix string, debugLogger pry.Logger) (*baseClient, error) {
	config := elasticsearch.Config{Addresses: []string{host}}
	if debugLogger != nil {
		config.Logger = NewDebugLogger(debugLogger)
	}
	c, err := elasticsearch.NewClient(config)
	if err != nil {
		return nil, err
	}
	return &baseClient{
		client: c,
		prefix: prefix,
	}, nil
}

type baseClient struct {
	client *elasticsearch.Client
	prefix string
}

func (c *baseClient) Search(ctx context.Context, index string, query any) ([]byte, error) {
	return c.do(ctx, func(ct context.Context) (*esapi.Response, error) {
		payload, err := json.Marshal(query)
		if err != nil {
			return nil, err
		}
		return c.client.Search(
			c.client.Search.WithContext(ct),
			c.client.Search.WithIndex(c.prefix+index),
			c.client.Search.WithBody(bytes.NewReader(payload)),
			c.client.Search.WithTrackTotalHits(true),
		)
	})
}

func (c *baseClient) MultiGet(ctx context.Context, index string, query any) ([]byte, error) {
	return c.do(ctx, func(ct context.Context) (*esapi.Response, error) {
		payload, err := json.Marshal(query)
		if err != nil {
			return nil, err
		}
		return c.client.Mget(
			bytes.NewReader(payload),
			c.client.Mget.WithContext(ct),
			c.client.Mget.WithIndex(c.prefix+index),
		)
	})
}

func (c *baseClient) Bulk(ctx context.Context, index string, body []byte) ([]byte, error) {
	return c.do(ctx, func(ct context.Context) (*esapi.Response, error) {
		return c.client.Bulk(
			bytes.NewReader(body),
			c.client.Bulk.WithIndex(c.prefix+index),
			c.client.Bulk.WithContext(ct),
		)
	})
}

func (c *baseClient) IndexExists(ctx context.Context, index string) (bool, error) {
	resp, err := c.client.Indices.Exists(
		[]string{c.prefix + index},
		c.client.Indices.Exists.WithContext(ctx),
	)
	if err != nil {
		return false, err
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)
	if resp.StatusCode == 404 {
		return false, nil
	}
	if resp.IsError() {
		return false, fmt.Errorf("%w: indices.exists [%s]", ErrWrongResponse, resp.Status())
	}
	return true, nil
}

func (c *baseClient) IndexCreate(ctx context.Context, index string, mappings any) ([]byte, error) {
	return c.do(ctx, func(ct context.Context) (*esapi.Response, error) {
		var body bytes.Buffer
		if mappings != nil {
			if err := json.NewEncoder(&body).Encode(mappings); err != nil {
				return nil, err
			}
		}
		return c.client.Indices.Create(
			c.prefix+index,
			c.client.Indices.Create.WithContext(ct),
			c.client.Indices.Create.WithBody(&body),
		)
	})
}

func (c *baseClient) DeleteByQuery(ctx context.Context, index string, query any) ([]byte, error) {
	return c.do(ctx, func(ct context.Context) (*esapi.Response, error) {
		payload, err := json.Marshal(query)
		if err != nil {
			return nil, err
		}
		return c.client.DeleteByQuery(
			[]string{c.prefix + index},
			bytes.NewReader(payload),
			c.client.DeleteByQuery.WithContext(ct),
		)
	})
}

func (c *baseClient) do(ctx context.Context, worker func(ctx context.Context) (*esapi.Response, error)) ([]byte, error) {
	result, err := worker(ctx)
	if err != nil {
		return nil, err
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(result.Body)

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, err
	}

	if result.IsError() {
		var e map[string]any
		if err = json.Unmarshal(data, &e); err != nil {
			return nil, fmt.Errorf("%w: failed to decode: %w", ErrUnknownResponse, err)
		}
		var errType, errReason string
		if inner, ok := e["error"].(map[string]any); ok {
			if v, ok := inner["type"].(string); ok {
				errType = v
			}
			if v, ok := inner["reason"].(string); ok {
				errReason = v
			}
		}
		return nil, &ResponseError{Status: result.Status(), Type: errType, Reason: errReason}
	}

	return data, nil
}
