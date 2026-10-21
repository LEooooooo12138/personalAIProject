package gateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/yuanleyao/ai-agent/internal/inference"
	"go.uber.org/zap"
)

// ChatStage reports actual work only; it is not a percentage or model output.
type ChatStage string

const (
	ChatStageQueued      ChatStage = "queued"
	ChatStagePreparing   ChatStage = "preparing"
	ChatStageClassifying ChatStage = "classifying"
	ChatStageGenerating  ChatStage = "generating"
	ChatStageSaving      ChatStage = "saving"
)

type chatObservation struct {
	mu       sync.Mutex
	once     sync.Once
	start    time.Time
	traceID  string
	logger   *zap.Logger
	timings  map[string]float64
	calls    int
	terminal string
}
type chatObservationKey struct{}

func startChatObservation(ctx context.Context, logger *zap.Logger) (context.Context, *chatObservation) {
	o := &chatObservation{start: time.Now(), traceID: newSessionID(), logger: logger, timings: make(map[string]float64)}
	return context.WithValue(ctx, chatObservationKey{}, o), o
}
func measureChat(ctx context.Context, name string) func() {
	o, _ := ctx.Value(chatObservationKey{}).(*chatObservation)
	start := time.Now()
	var once sync.Once
	return func() {
		once.Do(func() {
			if o != nil {
				o.mu.Lock()
				o.timings[name] += float64(time.Since(start)) / float64(time.Millisecond)
				o.mu.Unlock()
			}
		})
	}
}
func observeChatCalls(ctx context.Context, phase string) context.Context {
	o, _ := ctx.Value(chatObservationKey{}).(*chatObservation)
	if o == nil {
		return ctx
	}
	return inference.WithCallObserver(ctx, func(c inference.CallTrace) {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.calls++
		o.logger.Info("chat provider timing", zap.String("trace_id", o.traceID), zap.String("phase", phase), zap.Int("call_sequence", o.calls),
			zap.Any("dns_ms", c.DNSMS), zap.Any("tcp_ms", c.TCPMS), zap.Any("tls_ms", c.TLSMS), zap.Any("request_written_ms", c.RequestWrittenMS), zap.Any("first_response_byte_ms", c.FirstResponseByteMS), zap.Any("first_body_byte_ms", c.FirstBodyByteMS), zap.Any("total_ms", c.TotalMS),
			zap.Bool("reused", c.Reused), zap.Int("status_code", c.StatusCode), zap.Int64("request_bytes", c.RequestBytes), zap.Int64("response_bytes", c.ResponseBytes), zap.Any("input_tokens", c.InputTokens), zap.Any("output_tokens", c.OutputTokens), zap.String("error_code", c.ErrorCode))
	})
}
func (o *chatObservation) finish(ctx context.Context) {
	o.once.Do(func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		terminal := o.terminal
		if terminal == "" {
			terminal = "incomplete"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				terminal = "inference_timeout"
			} else if ctx.Err() != nil {
				terminal = "cancelled"
			}
		}
		fields := []zap.Field{zap.String("trace_id", o.traceID), zap.Float64("total_ms", float64(time.Since(o.start))/float64(time.Millisecond)), zap.Int("provider_calls", o.calls), zap.String("terminal_code", terminal)}
		for _, name := range []string{"queue", "settings", "personality", "classify", "generate", "filter", "persist", "publish"} {
			var value *float64
			if ms, ok := o.timings[name]; ok {
				value = &ms
			}
			fields = append(fields, zap.Any(name+"_ms", value))
		}
		o.logger.Info("chat round timing", fields...)
	})
}
func (w *wsConn) publishChatStage(ctx context.Context, stage ChatStage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.sessionID == "" || w.requestID == "" {
		return errors.New("chat stage identity unavailable")
	}
	switch stage {
	case ChatStageQueued, ChatStagePreparing, ChatStageClassifying, ChatStageGenerating, ChatStageSaving:
	default:
		return errors.New("invalid chat stage")
	}
	// WithSession serializes this short write against revocation. It must never
	// enclose inference, configuration reads, or persistence for stage events.
	return w.publishChatFrame(ctx, serverMessage{Type: "chat_stage", SessionID: w.sessionID, RequestID: w.requestID, Stage: stage})
}
func (w *wsConn) publishChatFrame(ctx context.Context, frame serverMessage) error {
	if w.options.WithSession != nil {
		return w.options.WithSession(ctx, func(live context.Context) error { return w.writeJSONContext(live, frame) })
	}
	if err := w.validateSession(); err != nil {
		return err
	}
	return w.writeJSONContext(ctx, frame)
}
func (w *wsConn) publishChatTimeout(ctx context.Context) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && w.ctx.Err() == nil {
		_ = w.publishChatFrame(w.ctx, serverMessage{Type: "error", Code: "inference_timeout", Outcome: "failed", Message: "chat generation timed out", SessionID: w.sessionID, RequestID: w.requestID})
	}
}
