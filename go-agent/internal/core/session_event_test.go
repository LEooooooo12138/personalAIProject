package core

import (
	"encoding/json"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
	"os"
	"testing"
	"time"
)

func TestDeviceMessageMetadataSurvivesCloneAndStorage(t *testing.T) {
	var msg Message
	json.Unmarshal([]byte(`{"role":"assistant","content":"observed","event_id":"request:r","personality_revision":"rev","presentation_version":"v1","device_result":{"entity_id":"sensor.t","name":"温度","state":"20","device_class":"temperature","unit":"°C"}}`), &msg)
	mgr := NewSessionManager(DefaultSessionConfig(), zap.NewNop())
	ss := NewSessionStore(mgr, t.TempDir(), nil, zap.NewNop())
	s, _ := ss.BrowserSession("console", "s", "owner", true)
	mgr.AddMessage(s, msg)
	if err := ss.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	messages, err := ss.OwnedMessages("console", "s", "owner")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(messages[0])
	var got map[string]any
	json.Unmarshal(data, &got)
	if got["event_id"] != "request:r" || got["device_result"] == nil || got["personality_revision"] != "rev" {
		t.Fatalf("device event metadata lost: %s", data)
	}
}

func TestOwnedEventDurabilityIsolationAndEndedSession(t *testing.T) {
	mgr := NewSessionManager(DefaultSessionConfig(), zap.NewNop())
	store := NewSessionStore(mgr, t.TempDir(), nil, zap.NewNop())
	session, _ := store.BrowserSession("console", "s", "owner", true)
	mgr.AddMessage(session, Message{Role: "user", Content: "question"})
	mgr.EndSession(session)
	unit, class := "C", "temperature"
	msg := Message{Role: "assistant", Content: "original", EventID: "event", DeviceResult: &smarthome.DeviceQueryResult{State: "20", Unit: &unit, DeviceClass: &class}}
	store.writeSession = func(*os.File, []byte) (int, error) { return 0, errors.New("disk full") }
	if index, saved, err := store.SaveOwnedEvent("console", "s", "owner", msg); err == nil || saved != nil || index != -1 {
		t.Fatalf("failed save published index/message: %d %v %v", index, saved, err)
	}
	if len(CloneSession(session).Messages) != 1 {
		t.Fatal("failed write contaminated live history")
	}
	store.writeSession = (*os.File).Write
	index, saved, err := store.SaveOwnedEvent("console", "s", "owner", msg)
	if err != nil || index != 1 {
		t.Fatalf("save: %d %v", index, err)
	}
	unit = "mutated"
	*saved.DeviceResult.DeviceClass = "mutated"
	saved.DeviceResult.State = "bad"
	clone := CloneSession(session)
	if *clone.Messages[1].DeviceResult.Unit != "C" || *clone.Messages[1].DeviceResult.DeviceClass != "temperature" || clone.Messages[1].DeviceResult.State != "20" {
		t.Fatal("device result pointers alias caller")
	}
	*clone.Messages[1].DeviceResult.Unit = "bad clone"
	msg.Content = "replacement"
	index, saved, err = store.SaveOwnedEvent("console", "s", "owner", msg)
	if err != nil || index != 1 || saved.Content != "original" || *saved.DeviceResult.Unit != "C" || len(CloneSession(session).Messages) != 2 {
		t.Fatal("duplicate changed persisted event")
	}
	if _, _, err = store.SaveOwnedEvent("console", "s", "foreign", msg); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("foreign owner appended event")
	}
	mgr.CompleteSession(session)
	msg.EventID = "second"
	msg.Content = "later"
	index, _, err = store.SaveOwnedEvent("console", "s", "owner", msg)
	if err != nil || index != 2 {
		t.Fatalf("cold ended result: %d %v", index, err)
	}
	messages, err := store.OwnedMessages("console", "s", "owner")
	if err != nil || len(messages) != 3 || messages[1].Content != "original" {
		t.Fatal("cold append lost prior messages")
	}
}

func TestOwnedEventConcurrentCompletionAndResumeCannotLoadStaleHistory(t *testing.T) {
	mgr := NewSessionManager(DefaultSessionConfig(), zap.NewNop())
	store := NewSessionStore(mgr, t.TempDir(), nil, zap.NewNop())
	session, _ := store.BrowserSession("console", "s", "owner", true)
	mgr.AddMessage(session, Message{Role: "user", Content: "earlier"})
	if err := store.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	mgr.EndSession(session)
	writing, release := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	store.writeSession = func(f *os.File, data []byte) (int, error) { close(writing); <-release; return f.Write(data) }
	saved := make(chan error, 1)
	go func() {
		_, _, err := store.SaveOwnedEvent("console", "s", "owner", Message{Role: "assistant", Content: "result", EventID: "event"})
		saved <- err
	}()
	<-writing
	restored := make(chan *Session, 1)
	resumeErr := make(chan error, 1)
	go func() {
		mgr.CompleteSession(session)
		next, err := store.BrowserSession("console", "s", "owner", false)
		resumeErr <- err
		restored <- next
	}()
	select {
	case <-restored:
		t.Fatal("resume loaded old disk history while event save was in progress")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	released = true
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	next := <-restored
	if err := <-resumeErr; err != nil {
		t.Fatal(err)
	}
	messages := CloneSession(next).Messages
	if len(messages) != 2 || messages[1].EventID != "event" {
		t.Fatalf("resumed history lost committed event: %+v", messages)
	}
	// A subsequent ordinary turn must retain the event instead of overwriting it.
	store.writeSession = (*os.File).Write
	mgr.AddMessage(next, Message{Role: "user", Content: "next question"})
	if err := store.SaveSession(next); err != nil {
		t.Fatal(err)
	}
	_, event, err := store.OwnedEvent("console", "s", "owner", "event")
	if err != nil || event == nil {
		t.Fatalf("next turn overwrote acknowledged event: %v", err)
	}
}
