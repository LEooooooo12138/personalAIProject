package channel

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// mockChannel implements the Channel interface for testing.
type mockChannel struct {
	id       string
	typ      Type
	msgCh    chan Message
	startErr error
	started  bool
	stopped  bool
	mu       sync.Mutex
}

func newMockChannel(id string, typ Type) *mockChannel {
	return &mockChannel{
		id:    id,
		typ:   typ,
		msgCh: make(chan Message, 10),
	}
}

func (m *mockChannel) ID() string              { return m.id }
func (m *mockChannel) Type() Type              { return m.typ }
func (m *mockChannel) Receive() <-chan Message { return m.msgCh }
func (m *mockChannel) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return m.startErr
}
func (m *mockChannel) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	return nil
}
func (m *mockChannel) Send(msg Message, resp Response) error { return nil }

func (m *mockChannel) isStarted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

func (m *mockChannel) isStopped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopped
}

func testLogger() *zap.Logger {
	return zap.NewNop()
}

func TestRegisterAndGet(t *testing.T) {
	mgr := NewManager(testLogger())
	ch := newMockChannel("test", Internal)
	mgr.Register(ch)

	got, err := mgr.Get("test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != ch {
		t.Error("Get returned wrong channel")
	}
}

func TestGet_NotFound(t *testing.T) {
	mgr := NewManager(testLogger())
	_, err := mgr.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent channel")
	}
}

func TestStartAll_Success(t *testing.T) {
	mgr := NewManager(testLogger())
	ch1 := newMockChannel("ch1", Internal)
	ch2 := newMockChannel("ch2", External)
	mgr.Register(ch1)
	mgr.Register(ch2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := mgr.StartAll(ctx)
	if err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	if !ch1.isStarted() {
		t.Error("ch1 should be started")
	}
	if !ch2.isStarted() {
		t.Error("ch2 should be started")
	}
}

func TestStopAll(t *testing.T) {
	mgr := NewManager(testLogger())
	ch1 := newMockChannel("ch1", Internal)
	mgr.Register(ch1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.StartAll(ctx)

	mgr.StopAll()
	if !ch1.isStopped() {
		t.Error("ch1 should be stopped")
	}
}

func TestRun_FanIn(t *testing.T) {
	mgr := NewManager(testLogger())
	ch := newMockChannel("ch1", Internal)
	mgr.Register(ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan Message, 1)
	handler := func(msg Message) {
		received <- msg
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel() // cancel after sending message
	}()

	// Send a message.
	go func() {
		time.Sleep(10 * time.Millisecond)
		ch.msgCh <- Message{ChannelID: "ch1", UserID: "u1", Content: "hello"}
	}()

	// StartAll first so the channel has started.
	mgr.StartAll(ctx)
	mgr.Run(ctx, handler)

	select {
	case msg := <-received:
		if msg.Content != "hello" {
			t.Errorf("received content = %q, want hello", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for message")
	}
}

func TestRun_NoChannels(t *testing.T) {
	mgr := NewManager(testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	handler := func(msg Message) {}
	// Should not panic with no channels.
	mgr.Run(ctx, handler)
}

func TestMultipleRegistrations(t *testing.T) {
	mgr := NewManager(testLogger())
	ch := newMockChannel("ch1", Internal)
	mgr.Register(ch)
	mgr.Register(ch) // re-register should overwrite

	got, err := mgr.Get("ch1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != ch {
		t.Error("re-registration should keep the last one")
	}
}

func TestRunWaitsForHandlersAfterCancellation(t *testing.T) {
	mgr := NewManager(testLogger())
	ch := newMockChannel("ch1", Internal)
	mgr.Register(ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		mgr.Run(ctx, func(Message) { close(started); <-release })
		close(finished)
	}()
	ch.msgCh <- Message{Content: "work"}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case <-finished:
		close(release)
		t.Fatal("Run returned while its message handler was still running")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("Run could not stop an idle, still-open source channel")
	}
}
