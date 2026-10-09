package gateway

import (
	"context"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"go.uber.org/zap"
	"sync"
	"testing"
	"time"
)

func TestSharedWebSocketSessionSerializesTurns(t *testing.T) {
	for _, maxRounds := range []int{20, 2} {
		t.Run(fmt.Sprint(maxRounds), func(t *testing.T) {
			beganA := make(chan struct{})
			beganB := make(chan []map[string]string, 1)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			s, ts := auditServer(t, chain.NewFuncStep("answer", func(ctx context.Context, state *chain.ChainState) error {
				if state.Query == "A" {
					close(beganA)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if state.Query == "B" {
					beganB <- state.Data["conversation_history"].([]map[string]string)
				}
				state.FinalAnswer = "answer" + state.Query
				return nil
			}))
			cfg := core.DefaultSessionConfig()
			cfg.MaxRounds = maxRounds
			s.sessionMgr = core.NewSessionManager(cfg, zap.NewNop())
			sessionDir := t.TempDir()
			s.sessionStore = core.NewSessionStore(s.sessionMgr, sessionDir, nil, zap.NewNop())
			cookie := browserCookie(t, ts)
			first := dialBrowser(t, ts, cookie)
			first.WriteJSON(clientMessage{Content: "seed"})
			var identity, response serverMessage
			if err := first.ReadJSON(&identity); err != nil {
				t.Fatal(err)
			}
			sid := identity.SessionID
			if err := first.ReadJSON(&response); err != nil {
				t.Fatal(err)
			}
			first.WriteJSON(clientMessage{Content: "A"})
			select {
			case <-beganA:
			case <-time.After(time.Second):
				t.Fatal("A did not start")
			}
			second := dialBrowser(t, ts, cookie)
			second.WriteJSON(clientMessage{SessionID: sid, Content: "B"})
			if err := second.ReadJSON(&identity); err != nil {
				t.Fatal(err)
			}
			if identity.Type != "session" {
				t.Fatalf("shared owner failed to resume: %+v", identity)
			}
			other := dialBrowser(t, ts, cookie)
			other.WriteJSON(clientMessage{Content: "C"})
			if err := other.ReadJSON(&identity); err != nil {
				t.Fatal(err)
			}
			if err := other.ReadJSON(&response); err != nil {
				t.Fatal(err)
			}
			if response.Content != "answerC" {
				t.Fatalf("unrelated session blocked: %+v", response)
			}
			select {
			case history := <-beganB:
				unblock()
				t.Fatalf("shared session overlapped: %#v", history)
			case <-time.After(50 * time.Millisecond):
			}
			unblock()
			if err := first.ReadJSON(&response); err != nil {
				t.Fatal(err)
			}
			if response.Content != "answerA" {
				t.Fatalf("last answer lost: %+v", response)
			}
			select {
			case history := <-beganB:
				if maxRounds == 20 && (len(history) != 4 || history[2]["content"] != "A" || history[3]["content"] != "answerA") {
					t.Fatalf("B saw incomplete history: %#v", history)
				}
				if maxRounds == 2 && len(history) != 0 {
					t.Fatalf("new conversation inherited previous history: %#v", history)
				}
			case <-time.After(time.Second):
				t.Fatal("B stayed blocked")
			}
			if err := second.ReadJSON(&response); err != nil {
				t.Fatal(err)
			}
			newSID := sid
			if maxRounds == 2 {
				if response.Type != "session" || response.SessionID == sid {
					t.Fatalf("missing round rotation: %+v", response)
				}
				newSID = response.SessionID
				if err := second.ReadJSON(&response); err != nil {
					t.Fatal(err)
				}
			}
			if response.Content != "answerB" {
				t.Fatalf("B answer lost: %+v", response)
			}
			// A cold store proves ordering was persisted, not merely correct in memory.
			diskStore := core.NewSessionStore(core.NewSessionManager(cfg, zap.NewNop()), sessionDir, nil, zap.NewNop())
			messages := diskStore.GetMessages("webchat", sid)
			want := 6
			if maxRounds == 2 {
				want = 4
			}
			if len(messages) != want || messages[2].Content != "A" || messages[3].Content != "answerA" {
				t.Fatalf("saved history is interleaved: %#v", messages)
			}
			if maxRounds == 2 {
				messages = diskStore.GetMessages("webchat", newSID)
				if len(messages) != 2 || messages[0].Content != "B" || messages[1].Content != "answerB" {
					t.Fatalf("new round incomplete: %#v", messages)
				}
			}
		})
	}
}
