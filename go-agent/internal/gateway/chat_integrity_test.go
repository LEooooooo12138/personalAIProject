package gateway

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// A real peer close must cancel the lease waiter before the current turn ends.
// A synchronous socket read loop fails here even though inference later sees a write error.
func TestChatPeerCloseCancelsQueuedTurn(t *testing.T) {
	for _, graceful := range []bool{false, true} {
		t.Run(fmt.Sprint(graceful), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var queuedCalls atomic.Int32
			s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(ctx context.Context, state *chain.ChainState) error {
				if state.Query == "holder" {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if state.Query == "queued" {
					queuedCalls.Add(1)
				}
				state.FinalAnswer = "answer " + state.Query
				return nil
			}))
			logCore, logs := observer.New(zap.InfoLevel)
			s.logger = zap.New(logCore)
			user, cookie := consoleMemberLogin(t, s, "alice")
			first := dialConsole(t, ts, cookie)
			if err := first.WriteJSON(clientMessage{RequestID: "holder", Entry: ChatEntryChat, Content: "holder"}); err != nil {
				t.Fatal(err)
			}
			var identity serverMessage
			if err := readChatMessage(first, &identity); err != nil {
				t.Fatal(err)
			}
			sid := identity.SessionID
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("holder did not start")
			}
			second := dialConsole(t, ts, cookie)
			if err := second.WriteJSON(clientMessage{SessionID: sid, RequestID: "queued", Entry: ChatEntryChat, Content: "queued"}); err != nil {
				t.Fatal(err)
			}
			for {
				var frame serverMessage
				if err := second.ReadJSON(&frame); err != nil {
					t.Fatal(err)
				}
				if frame.Type == "chat_stage" && frame.Stage == ChatStageQueued {
					break
				}
			}
			if graceful {
				if err := second.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			} else {
				second.Close()
			}
			deadline := time.Now().Add(time.Second)
			for logs.FilterMessage("chat round timing").FilterField(zap.String("terminal_code", "cancelled")).Len() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if logs.FilterMessage("chat round timing").FilterField(zap.String("terminal_code", "cancelled")).Len() != 1 {
				t.Fatal("peer close did not cancel queued turn while holder remained active")
			}
			unblock()
			var response serverMessage
			if err := readChatMessage(first, &response); err != nil {
				t.Fatal(err)
			}
			if response.Type != "response" {
				t.Fatalf("holder terminal: %+v", response)
			}
			session, err := s.sessionStore.BrowserSession("console", sid, "console:"+user.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := core.CloneSession(session)
			if len(snapshot.Messages) != 2 || snapshot.Messages[0].Content != "holder" || queuedCalls.Load() != 0 {
				t.Fatalf("closed queued work escaped: %+v calls=%d", snapshot.Messages, queuedCalls.Load())
			}
			if _, bound := snapshot.RequestEntries["queued"]; bound {
				t.Fatal("closed queued request persisted entry binding")
			}
		})
	}
}

// The real failed file replacement must not publish the answer through the live
// history endpoint, a resumed socket's model history, or a cold session store.
func TestChatFailedSaveNeverPublishesLiveHistory(t *testing.T) {
	var s *Server
	var failedPath, backupPath string
	var failureCalls atomic.Int32
	var recoveredHistory []map[string]string
	s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error {
		if state.Query == "fail" {
			failureCalls.Add(1)
			if err := os.Rename(failedPath, backupPath); err != nil {
				return err
			}
			if err := os.Mkdir(failedPath, 0700); err != nil {
				return err
			}
			state.FinalAnswer = "UNSAVED ANSWER"
		} else {
			if state.Query == "recover" {
				recoveredHistory = state.Data["conversation_history"].([]map[string]string)
			}
			state.FinalAnswer = "saved answer"
		}
		return nil
	}))
	dir := t.TempDir()
	s.sessionStore = core.NewSessionStore(s.sessionMgr, dir, nil, zap.NewNop())
	user, cookie := consoleMemberLogin(t, s, "alice")
	conn := dialConsole(t, ts, cookie)
	if err := conn.WriteJSON(clientMessage{RequestID: "seed", Entry: ChatEntryChat, Content: "seed"}); err != nil {
		t.Fatal(err)
	}
	var frame serverMessage
	if err := readChatMessage(conn, &frame); err != nil {
		t.Fatal(err)
	}
	sid := frame.SessionID
	if err := readChatMessage(conn, &frame); err != nil || frame.Type != "response" {
		t.Fatalf("seed: %+v %v", frame, err)
	}
	failedPath = filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte("console:"+sid))))
	backupPath = failedPath + ".backup"
	if err := conn.WriteJSON(clientMessage{RequestID: "fail", Entry: ChatEntryChat, Content: "fail"}); err != nil {
		t.Fatal(err)
	}
	if err := readChatMessage(conn, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != "error" || frame.Code != "chat_publish_failed" || frame.Outcome != "unknown" {
		t.Fatalf("save failure terminal: %+v", frame)
	}
	if err := os.Remove(failedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backupPath, failedPath); err != nil {
		t.Fatal(err)
	}
	history := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/sessions/"+sid+"/messages", "", cookie, "", "", ""), 200)
	messages := history["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("failed answer became visible or input was lost: %+v", messages)
	}
	for _, message := range messages {
		if message.(map[string]any)["content"] == "UNSAVED ANSWER" {
			t.Fatal("HTTP recovery exposed unsaved answer")
		}
	}
	cold := core.NewSessionStore(core.NewSessionManager(core.DefaultSessionConfig(), zap.NewNop()), dir, nil, zap.NewNop())
	durable, err := cold.OwnedMessages("console", sid, "console:"+user.ID)
	if err != nil || len(durable) != 2 {
		t.Fatalf("failed save changed durable history: %+v %v", durable, err)
	}
	reloaded, err := cold.BrowserSession("console", sid, "console:"+user.ID, false)
	if err != nil || core.CloneSession(reloaded).RequestEntries["fail"].Entry != "chat" {
		t.Fatalf("failed result lost durable request binding: %v", err)
	}
	conn.Close()
	resumed := dialConsole(t, ts, cookie)
	if err := resumed.WriteJSON(clientMessage{SessionID: sid, RequestID: "recover", Entry: ChatEntryChat, Content: "recover"}); err != nil {
		t.Fatal(err)
	}
	if err := readChatMessage(resumed, &frame); err != nil || frame.Type != "session" {
		t.Fatalf("resume: %+v %v", frame, err)
	}
	if err := readChatMessage(resumed, &frame); err != nil || frame.Type != "response" {
		t.Fatalf("recovery terminal: %+v %v", frame, err)
	}
	if len(recoveredHistory) != 3 || recoveredHistory[2]["content"] != "fail" {
		t.Fatalf("reconnected generation saw unsaved answer or lost input: %+v", recoveredHistory)
	}
	if failureCalls.Load() != 1 {
		t.Fatalf("failed generation repeated: %d", failureCalls.Load())
	}
}

// Reading ahead must not let a later message overwrite the active request's
// identity or enter generation before its predecessor is fully saved.
func TestChatPipelinedFramesRemainSequential(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	histories := make(chan []map[string]string, 3)
	s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(ctx context.Context, state *chain.ChainState) error {
		if state.Query == "A" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		histories <- state.Data["conversation_history"].([]map[string]string)
		state.FinalAnswer = "answer " + state.Query
		return nil
	}))
	user, cookie := consoleMemberLogin(t, s, "alice")
	conn := dialConsole(t, ts, cookie)
	if err := conn.WriteJSON(clientMessage{RequestID: "A", Entry: ChatEntryChat, Content: "A"}); err != nil {
		t.Fatal(err)
	}
	var frame serverMessage
	if err := readChatMessage(conn, &frame); err != nil {
		t.Fatal(err)
	}
	sid := frame.SessionID
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("A did not start")
	}
	for _, content := range []string{"B", "C"} {
		if err := conn.WriteJSON(clientMessage{RequestID: content, Entry: ChatEntryChat, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	unblock()
	for i, content := range []string{"A", "B", "C"} {
		if err := readChatMessage(conn, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type != "response" || frame.RequestID != content || frame.SessionID != sid || frame.Content != "answer "+content {
			t.Fatalf("pipelined identity/order changed: %+v", frame)
		}
		history := <-histories
		if len(history) != i*2 {
			t.Fatalf("generation %s saw incomplete predecessor: %+v", content, history)
		}
		if i > 0 && history[len(history)-1]["content"] != "answer "+[]string{"A", "B"}[i-1] {
			t.Fatalf("predecessor out of order: %+v", history)
		}
	}
	messages, err := s.sessionStore.OwnedMessages("console", sid, "console:"+user.ID)
	if err != nil || len(messages) != 6 {
		t.Fatalf("pipelined history not complete: %+v %v", messages, err)
	}
}
