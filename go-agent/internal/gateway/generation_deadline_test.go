package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
)

// Exercise the shared turn path with a short generation context while keeping
// the socket/login context alive. The production wrapper uses its usual limit.
func TestGenerationDeadlineTerminalFrame(t *testing.T) {
	for _, channel := range []string{"webchat", "console"} {
		t.Run(channel, func(t *testing.T) {
			s, _ := consoleChatFixture(t, chain.NewFuncStep("answer", func(ctx context.Context, state *chain.ChainState) error {
				if state.Query == "first" {
					<-ctx.Done()
					state.FinalAnswer = "late private answer"
					return nil
				}
				state.FinalAnswer = "answer " + state.Query
				return nil
			}))
			user, cookie := consoleMemberLogin(t, s, "alice")
			owner := "browser-owner"
			if channel == "console" {
				owner = "console:" + user.ID
			}
			const sid = "deadline-session"
			if _, err := s.sessionStore.BrowserSession(channel, sid, owner, true); err != nil {
				t.Fatal(err)
			}
			finished := make(chan struct{})
			ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, r *http.Request) {
				defer close(finished)
				ctx, cancel := context.WithCancel(r.Context())
				defer cancel()
				options := webChatOptions{ChannelID: channel, VaultName: "agent"}
				if channel == "console" {
					bound, stop, err := s.consoleStore.BindSession(ctx, cookie.Value)
					if err != nil {
						t.Error(err)
						return
					}
					defer stop()
					ctx = bound
					options.WithSession = func(ctx context.Context, fn func(context.Context) error) error {
						return s.consoleStore.WithSession(ctx, cookie.Value, fn)
					}
					options.ValidateSession = func(context.Context) error {
						_, err := s.consoleStore.Resolve(cookie.Value)
						return err
					}
				}
				u := websocket.Upgrader{}
				conn, err := u.Upgrade(writer, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				w := &wsConn{conn: conn, ctx: ctx, owner: owner, sessionID: sid, options: options, logger: s.logger, sessionMgr: s.sessionMgr, sessionStore: s.sessionStore, chainExecutor: s.chainExecutor, chainRouter: s.chainRouter, filter: s.filterChain}
				for i := 0; i < 2; i++ {
					var input clientMessage
					if err := conn.ReadJSON(&input); err != nil {
						return
					}
					if i == 0 {
						generation, stop := context.WithTimeout(ctx, 40*time.Millisecond)
						w.handleChatContext(generation, input.Content)
						stop()
					} else {
						w.handleChat(input.Content)
					}
					if w.sessionID != sid {
						t.Errorf("generation changed SID: %q", w.sessionID)
					}
				}
			}))
			defer ts.Close()
			conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http:", "ws:", 1), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			if err := conn.WriteJSON(clientMessage{Content: "first"}); err != nil {
				t.Fatal(err)
			}
			var output serverMessage
			if err := conn.ReadJSON(&output); err != nil || output.Type != "error" || output.Message == "" || output.Code == "session_not_found" || strings.Contains(output.Message, "private") {
				t.Fatalf("deadline must terminate with a safe error frame: %+v, %v", output, err)
			}
			messages, err := s.sessionStore.OwnedMessages(channel, sid, owner)
			if err != nil || len(messages) != 1 || messages[0].Role != "user" || messages[0].Content != "first" {
				t.Fatalf("timeout appended an assistant or replayed input: %+v, %v", messages, err)
			}
			lockCtx, stop := context.WithTimeout(context.Background(), time.Second)
			release, err := s.sessionMgr.AcquireTurn(lockCtx, channel, sid)
			stop()
			if err != nil {
				t.Fatalf("timeout retained the turn lock: %v", err)
			}
			release()
			if err := conn.WriteJSON(clientMessage{Content: "second"}); err != nil {
				t.Fatal(err)
			}
			output = serverMessage{}
			if err := conn.ReadJSON(&output); err != nil || output.Type != "response" || output.Content != "answer second" {
				t.Fatalf("same socket did not recover: %+v, %v", output, err)
			}
			<-finished
			messages, err = s.sessionStore.OwnedMessages(channel, sid, owner)
			if err != nil || len(messages) != 3 || messages[1].Content != "second" || messages[2].Role != "assistant" {
				t.Fatalf("recovery changed history or replayed first turn: %+v, %v", messages, err)
			}
			cold := core.NewSessionStore(core.NewSessionManager(core.DefaultSessionConfig(), s.logger), s.cfg.Vaults.Agent+"/_sessions", nil, s.logger)
			if messages, err := cold.OwnedMessages(channel, sid, owner); err != nil || len(messages) != 3 {
				t.Fatalf("recovery was not saved under the original SID: %+v, %v", messages, err)
			}
		})
	}
}
