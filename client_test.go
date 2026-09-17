package elastic

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

func reply(status int, body string) func(context.Context) (*esapi.Response, error) {
	return func(context.Context) (*esapi.Response, error) {
		return &esapi.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
}

const notFound = `{"error":{"type":"index_not_found_exception","reason":"no such index [orders]"},"status":404}`

// Elasticsearch names the failure in the body, and formatting that name into a
// message puts it out of reach of everything but a human reader.
func TestDoKeepsTheNameElasticsearchGave(t *testing.T) {
	c := &baseClient{}

	_, err := c.do(context.Background(), reply(404, notFound))
	if err == nil {
		t.Fatal("an error status produced no error")
	}

	var re *ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("err is %T, want *ResponseError", err)
	}
	if re.Type != "index_not_found_exception" {
		t.Errorf("Type = %q, want index_not_found_exception", re.Type)
	}
	if re.Reason != "no such index [orders]" {
		t.Errorf("Reason = %q, want the reason from the body", re.Reason)
	}
	if got := elasticErrorType(err); got != "index_not_found_exception" {
		t.Errorf("error.type = %q, want index_not_found_exception", got)
	}
}

func TestDoOnABodyItCannotRead(t *testing.T) {
	c := &baseClient{}

	_, err := c.do(context.Background(), reply(500, "not json"))
	if !errors.Is(err, ErrUnknownResponse) {
		t.Errorf("err = %v, want ErrUnknownResponse", err)
	}
	if got := elasticErrorType(err); got != "unknown_response" {
		t.Errorf("error.type = %q, want unknown_response", got)
	}
}

func TestDoOnABodyWithoutAType(t *testing.T) {
	c := &baseClient{}

	_, err := c.do(context.Background(), reply(503, `{"status":503}`))
	if !errors.Is(err, ErrWrongResponse) {
		t.Errorf("err = %v, want ErrWrongResponse", err)
	}
	if got := elasticErrorType(err); got != "wrong_response" {
		t.Errorf("error.type = %q, want wrong_response", got)
	}
}

func TestDoReturnsTheBodyWhenTheStatusIsFine(t *testing.T) {
	c := &baseClient{}

	data, err := c.do(context.Background(), reply(200, `{"hits":[]}`))
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if string(data) != `{"hits":[]}` {
		t.Errorf("data = %s", data)
	}
}
