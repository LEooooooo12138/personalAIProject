package inference

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http/httptrace"
	"sync"
	"time"
)

// CallTrace describes one actual cloud HTTP call. Nil timings mean the hook was
// not observed. Times are milliseconds; response/body milestones are relative
// to call start, while DNS/TCP/TLS are accumulated completed hook durations.
// FirstBodyByteMS measures bytes read by this client, never a model's first token.
type CallTrace struct {
	DNSMS, TCPMS, TLSMS, RequestWrittenMS         *float64
	FirstResponseByteMS, FirstBodyByteMS, TotalMS *float64
	Reused                                        bool
	StatusCode                                    int
	RequestBytes, ResponseBytes                   int64
	InputTokens, OutputTokens                     *int64
	ErrorCode                                     string
}

type callObserverKey struct{}

// WithCallObserver observes each actual cloud call without changing its routing,
// request, retry policy or transport. The callback receives only safe metrics.
func WithCallObserver(ctx context.Context, observe func(CallTrace)) context.Context {
	return context.WithValue(ctx, callObserverKey{}, observe)
}

type callRecorder struct {
	mu       sync.Mutex
	once     sync.Once
	start    time.Time
	trace    CallTrace
	observe  func(CallTrace)
	dns, tls []time.Time
	tcp      map[string][]time.Time
}

func startCallTrace(ctx context.Context, requestBytes int64) (context.Context, *callRecorder) {
	observe, _ := ctx.Value(callObserverKey{}).(func(CallTrace))
	if observe == nil {
		return ctx, nil
	}
	c := &callRecorder{start: time.Now(), observe: observe, trace: CallTrace{RequestBytes: requestBytes}, tcp: make(map[string][]time.Time)}
	hooks := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { c.mu.Lock(); defer c.mu.Unlock(); c.dns = append(c.dns, time.Now()) },
		DNSDone: func(httptrace.DNSDoneInfo) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.dns = completeHook(c.dns, &c.trace.DNSMS)
		},
		ConnectStart: func(network, addr string) {
			c.mu.Lock()
			defer c.mu.Unlock()
			key := network + "\x00" + addr
			c.tcp[key] = append(c.tcp[key], time.Now())
		},
		ConnectDone: func(network, addr string, _ error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			key := network + "\x00" + addr
			c.tcp[key] = completeHook(c.tcp[key], &c.trace.TCPMS)
		},
		TLSHandshakeStart: func() { c.mu.Lock(); defer c.mu.Unlock(); c.tls = append(c.tls, time.Now()) },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.tls = completeHook(c.tls, &c.trace.TLSMS)
		},
		GotConn: func(info httptrace.GotConnInfo) { c.mu.Lock(); defer c.mu.Unlock(); c.trace.Reused = info.Reused },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if info.Err == nil && c.trace.RequestWrittenMS == nil {
				c.trace.RequestWrittenMS = elapsedMS(c.start)
			}
		},
		GotFirstResponseByte: func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.trace.FirstResponseByteMS == nil {
				c.trace.FirstResponseByteMS = elapsedMS(c.start)
			}
		},
	}
	// WithClientTrace composes with any caller trace rather than replacing it.
	return httptrace.WithClientTrace(ctx, hooks), c
}

func elapsedMS(start time.Time) *float64 {
	v := float64(time.Since(start)) / float64(time.Millisecond)
	return &v
}
func completeHook(pending []time.Time, total **float64) []time.Time {
	if len(pending) == 0 {
		return pending
	}
	value := *elapsedMS(pending[0])
	if *total != nil {
		value += **total
	}
	*total = &value
	return pending[1:]
}

func (c *callRecorder) finish(err error) {
	if c == nil {
		return
	}
	c.once.Do(func() {
		c.mu.Lock()
		c.trace.TotalMS = elapsedMS(c.start)
		c.trace.ErrorCode = callErrorCode(err)
		snapshot := c.trace
		c.mu.Unlock()
		c.observe(snapshot)
	})
}

// This closed vocabulary deliberately never uses err.Error() or request data.
func callErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "deadline_exceeded"
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return "upstream_status"
	}
	if errors.Is(err, ErrInvalidResponse) {
		return "invalid_response"
	}
	return "transport_error"
}

type callTraceBody struct {
	io.ReadCloser
	recorder *callRecorder
}

func (b *callTraceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.recorder.mu.Lock()
		if b.recorder.trace.FirstBodyByteMS == nil {
			b.recorder.trace.FirstBodyByteMS = elapsedMS(b.recorder.start)
		}
		b.recorder.trace.ResponseBytes += int64(n)
		b.recorder.mu.Unlock()
	}
	return n, err
}
