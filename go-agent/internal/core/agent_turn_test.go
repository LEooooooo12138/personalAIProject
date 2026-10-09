package core

import (
	"context"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/channel"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"go.uber.org/zap"
	"sync"
	"testing"
	"time"
)

type turnTestChannel struct{ failSend bool }

func (*turnTestChannel) ID() string                      { return "turn-test" }
func (*turnTestChannel) Type() channel.Type              { return channel.External }
func (*turnTestChannel) Start(context.Context) error     { return nil }
func (*turnTestChannel) Stop() error                     { return nil }
func (*turnTestChannel) Receive() <-chan channel.Message { return nil }
func (c *turnTestChannel) Send(channel.Message, channel.Response) error {
	if c.failSend {
		return errors.New("send failed")
	}
	return nil
}

func newTurnTestAgent(t *testing.T, step chain.Step, ch *turnTestChannel) *Agent {
	t.Helper()
	log := zap.NewNop()
	cm := channel.NewManager(log)
	cm.Register(ch)
	router := chain.NewChainRouter()
	router.Register("chat", chain.NewChain("chat", "", step))
	return NewAgent(AgentDeps{Config: &Config{Inference: InferenceConfig{Timeout: time.Second}}, Logger: log, SessionMgr: NewSessionManager(DefaultSessionConfig(), log), FilterChain: filter.NewChain(), ChannelMgr: cm, ChainRouter: router, ChainExecutor: chain.NewChainExecutor(log, router)})
}
func TestAgentSharedTurnPreservesHistory(t *testing.T) {
	enteredA, enteredB, completedC := make(chan struct{}), make(chan []map[string]string, 1), make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	a := newTurnTestAgent(t, chain.NewFuncStep("answer", func(ctx context.Context, s *chain.ChainState) error {
		switch s.Query {
		case "A":
			close(enteredA)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		case "B":
			enteredB <- s.Data["conversation_history"].([]map[string]string)
		case "C":
			close(completedC)
		}
		s.FinalAnswer = "answer" + s.Query
		return nil
	}), &turnTestChannel{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	send := func(user, text string) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			a.handleMessageContext(ctx, channel.Message{ChannelID: "turn-test", UserID: user, Content: text})
		}()
	}
	defer func() { unblock(); cancel(); workers.Wait() }()
	send("alice", "A")
	<-enteredA
	send("alice", "B")
	send("bob", "C")
	select {
	case <-completedC:
	case <-time.After(time.Second):
		t.Fatal("other user was blocked")
	}
	select {
	case history := <-enteredB:
		t.Fatalf("B began before A's answer: %#v", history)
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case history := <-enteredB:
		if len(history) != 2 || history[0]["content"] != "A" || history[1]["content"] != "answerA" {
			t.Fatalf("history not a complete preceding turn: %#v", history)
		}
	case <-time.After(time.Second):
		t.Fatal("B did not start")
	}
	workers.Wait()
	s, _ := a.sessionMgr.GetOrCreate("turn-test", "alice")
	messages := CloneSession(s).Messages
	if len(messages) != 4 || messages[0].Content != "A" || messages[1].Content != "answerA" || messages[2].Content != "B" || messages[3].Content != "answerB" {
		t.Fatalf("interleaved history: %#v", messages)
	}
}
func TestAgentTurnReleasesAfterFailure(t *testing.T) {
	for _, mode := range []string{"generation", "send"} {
		t.Run(mode, func(t *testing.T) {
			a := newTurnTestAgent(t, chain.NewFuncStep("answer", func(_ context.Context, s *chain.ChainState) error {
				if mode == "generation" && s.Query == "A" {
					return errors.New("failed")
				}
				s.FinalAnswer = "ok"
				return nil
			}), &turnTestChannel{failSend: mode == "send"})
			a.handleMessageContext(context.Background(), channel.Message{ChannelID: "turn-test", UserID: "alice", Content: "A"})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			release, err := acquireTestTurn(t, a.sessionMgr, ctx, "alice") // independent webchat identity also remains usable
			if err != nil {
				t.Fatal(err)
			}
			release()
			api := interface{}(a.sessionMgr).(turnAcquirer)
			release, err = api.AcquireTurn(ctx, "turn-test", "alice")
			if err != nil {
				t.Fatalf("failed turn kept its lease: %v", err)
			}
			release()
		})
	}
}

func TestCancelledAgentTurnDoesNotAppendQueuedUser(t *testing.T) {
	began := make(chan struct{})
	releaseA := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(releaseA) }) }
	defer unblock()
	a := newTurnTestAgent(t, chain.NewFuncStep("answer", func(ctx context.Context, s *chain.ChainState) error {
		if s.Query == "A" {
			close(began)
			select {
			case <-releaseA:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		s.FinalAnswer = "answer" + s.Query
		return nil
	}), &turnTestChannel{})
	root, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		a.handleMessageContext(root, channel.Message{ChannelID: "turn-test", UserID: "alice", Content: "A"})
	}()
	<-began
	waiting, cancel := context.WithCancel(root)
	defer cancel()
	doneB := make(chan struct{})
	go func() {
		defer close(doneB)
		a.handleMessageContext(waiting, channel.Message{ChannelID: "turn-test", UserID: "alice", Content: "B"})
	}()
	waitTurnRefs(t, a.sessionMgr, turnKey{"turn-test", "alice"}, 2)
	cancel()
	select {
	case <-doneB:
	case <-time.After(time.Second):
		t.Fatal("queued Agent turn ignored cancellation")
	}
	s, _ := a.sessionMgr.GetOrCreate("turn-test", "alice")
	messages := CloneSession(s).Messages
	if len(messages) != 1 || messages[0].Content != "A" {
		t.Fatalf("canceled waiting user polluted history: %#v", messages)
	}
	unblock()
	select {
	case <-doneA:
	case <-time.After(time.Second):
		t.Fatal("active Agent turn leaked")
	}
	a.sessionMgr.turnMu.Lock()
	defer a.sessionMgr.turnMu.Unlock()
	if len(a.sessionMgr.turns) != 0 {
		t.Fatal("completed Agent turn retained its lease")
	}
}
