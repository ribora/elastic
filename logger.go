package elastic

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/ribora/pry"
)

func NewDebugLogger(l pry.Logger) *logger {
	return &logger{
		logger: l,
	}
}

type logger struct {
	logger pry.Logger
}

func (l *logger) RequestBodyEnabled() bool {
	return true
}

func (l *logger) ResponseBodyEnabled() bool {
	return true
}

func (l *logger) LogRoundTrip(req *http.Request, res *http.Response, err error, start time.Time, dur time.Duration) error {
	var reqBody, resBody string
	var method, url string
	ctx := context.Background()
	status := 0

	if req != nil {
		method = req.Method
		url = req.URL.String()
		ctx = req.Context()
		if req.Body != nil {
			buf, _ := io.ReadAll(req.Body)
			reqBody = string(buf)
			req.Body = io.NopCloser(bytes.NewBuffer(buf))
		}
	}

	if err != nil {
		l.logger.Error("ES_DEBUG: Request failed",
			pry.Field("method", method),
			pry.Field("url", url),
			pry.Field("duration", dur),
			pry.Field("request", reqBody),
			pry.Err(err),
			pry.Ctx(ctx),
		)
		return nil
	}

	if res != nil {
		status = res.StatusCode
		if res.Body != nil {
			buf, _ := io.ReadAll(res.Body)
			resBody = string(buf)
			res.Body = io.NopCloser(bytes.NewBuffer(buf))
		}
	}

	l.logger.Debug("ES_DEBUG: Request completed",
		pry.Field("method", method),
		pry.Field("url", url),
		pry.Field("duration", dur),
		pry.Field("status", status),
		pry.Field("request", reqBody),
		pry.Field("response", resBody),
		pry.Ctx(ctx),
	)
	return nil
}
