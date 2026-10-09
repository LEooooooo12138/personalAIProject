package smarthome

import (
	"context"
	"errors"
	"net"
)

type clientError struct{ code string }

func (e *clientError) Error() string { return "home assistant: " + e.code }
func newHAError(code string) error   { return &clientError{code: code} }
func HAErrorCode(err error) string {
	var e *clientError
	if errors.As(err, &e) {
		return e.code
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return "ha_timeout"
	}
	return "ha_unavailable"
}
func safeHAError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return newHAError(HAErrorCode(err))
}
func haStatusError(status int) error {
	switch status {
	case 401:
		return newHAError("ha_auth_required")
	case 403:
		return newHAError("ha_forbidden")
	case 408, 504:
		return newHAError("ha_timeout")
	default:
		return newHAError("ha_unavailable")
	}
}
