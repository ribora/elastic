package elastic

import (
	"errors"
)

var (
	ErrUnknownResponse = errors.New("unknown elastic response")
	ErrWrongResponse   = errors.New("wrong elastic response")
)
