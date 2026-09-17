package elastic

import (
	"errors"
	"fmt"
)

var (
	ErrUnknownResponse = errors.New("unknown elastic response")
	ErrWrongResponse   = errors.New("wrong elastic response")
)

type ResponseError struct {
	Status string
	Type   string
	Reason string
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("%s: [%s] {%s}: {%s}", ErrWrongResponse, e.Status, orUnknown(e.Type), orUnknown(e.Reason))
}

func (e *ResponseError) Unwrap() error { return ErrWrongResponse }

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
