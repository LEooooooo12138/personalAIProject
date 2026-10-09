package channel

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSameUserMessagesStayFIFO(t *testing.T) {
	mgr := NewManager(testLogger())
	ch := newMockChannel("test", External)
	mgr.Register(ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan string, 8)
	release := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		mgr.Run(ctx, func(msg Message) {
			entered <- msg.Content
			if msg.Content == "A" {
				<-release
			}
		})
		close(done)
	}()
	ch.msgCh <- Message{ChannelID: "test", UserID: "alice", Content: "A"}
	if got := <-entered; got != "A" {
		t.Fatal(got)
	}
	ch.msgCh <- Message{ChannelID: "test", UserID: "alice", Content: "B"}
	ch.msgCh <- Message{ChannelID: "test", UserID: "bob", Content: "C"}
	select {
	case got := <-entered:
		if got != "C" {
			t.Fatalf("same-user handler started before prior answer: %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("other user blocked")
	}
	select {
	case got := <-entered:
		t.Fatalf("same-user overlap: %s", got)
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case got := <-entered:
		if got != "B" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("queued turn never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not finish")
	}
}

func TestFIFOCancellationDropsQueuedHandlersAndWaitsForActive(t *testing.T) {
	mgr := NewManager(testLogger())
	ch := newMockChannel("test", External)
	mgr.Register(ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan string, 8)
	release := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		mgr.Run(ctx, func(msg Message) {
			entered <- msg.Content
			if msg.Content == "A" {
				<-release
			}
		})
		close(done)
	}()
	ch.msgCh <- Message{ChannelID: "test", UserID: "alice", Content: "A"}
	<-entered
	ch.msgCh <- Message{ChannelID: "test", UserID: "alice", Content: "B"}
	ch.msgCh <- Message{ChannelID: "test", UserID: "bob", Content: "C"}
	select {
	case got := <-entered:
		if got != "C" {
			t.Fatalf("queued handler started: %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("other user blocked")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("Run returned before active handler")
	default:
	}
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation leaked queued handlers")
	}
	select {
	case got := <-entered:
		t.Fatalf("canceled queued handler ran: %s", got)
	default:
	}
}
