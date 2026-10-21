package inference

import (
	"context"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"io"
	"net"
)

var (
	ErrKeyUnavailable  = errors.New("inference key unavailable")
	ErrInvalidRequest  = errors.New("invalid inference request")
	ErrInvalidResponse = errors.New("invalid inference response")
)

// ErrorCode exposes only an allowlisted diagnosis, never provider text or URLs.
func ErrorCode(err error) string {
	if errors.Is(err, assistantconfig.ErrEndpointMismatch) {
		return "mimo_endpoint_mismatch"
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		switch upstream.Status {
		case 421:
			return "inference_content_filtered"
		case 408, 504:
			return "inference_timeout"
		case 401:
			return "inference_auth_failed"
		case 403:
			return "inference_forbidden"
		case 402:
			return "inference_quota_exceeded"
		case 429:
			return "inference_rate_limited"
		case 404:
			return "inference_model_unavailable"
		case 400, 405, 413, 415, 422:
			return "inference_invalid_request"
		default:
			return "inference_unavailable"
		}
	}
	if errors.Is(err, ErrKeyUnavailable) {
		return "key_unavailable"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "inference_timeout"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "inference_timeout"
	}
	if errors.Is(err, ErrInvalidRequest) {
		return "inference_invalid_request"
	}
	if errors.Is(err, ErrInvalidResponse) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "inference_invalid_response"
	}
	if errors.Is(err, ErrCloudUnavailable) || errors.As(err, &network) {
		return "inference_unavailable"
	}
	return ""
}

// Transport errors keep their cause available to errors.Is/As while their public
// text stays bounded and cannot disclose request URLs or credentials.
type transportError struct{ cause error }

func (e transportError) Error() string        { return "inference transport failed" }
func (e transportError) Unwrap() error        { return e.cause }
func (e transportError) Is(target error) bool { return target == ErrCloudUnavailable }
