package gateway

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/console"
	"github.com/yuanleyao/ai-agent/internal/core"
	"go.uber.org/zap"
)

func consoleMemberLogin(t *testing.T, s *Server, name string) (console.User, *http.Cookie) {
	t.Helper()
	u, temp, err := s.consoleStore.CreateMember(name, name)
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.consoleStore.Authenticate(name, temp)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.consoleStore.ChangePassword(login.Token, temp, testConsolePassword); err != nil {
		t.Fatal(err)
	}
	cookie, _, _ := consoleLogin(t, s, name, testConsolePassword, testConsoleOrigin)
	return u, cookie
}
func consoleChatFixture(t *testing.T, step chain.Step) (*Server, *httptest.Server) {
	t.Helper()
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	cr := chain.NewChainRouter()
	cr.Register("chat", chain.NewChain("chat", "", step))
	s.chainRouter = cr
	s.chainExecutor = chain.NewChainExecutor(s.logger, cr)
	ts := httptest.NewServer(s.engine)
	t.Cleanup(ts.Close)
	return s, ts
}
func dialConsole(t *testing.T, ts *httptest.Server, cookie *http.Cookie) *websocket.Conn {
	t.Helper()
	h := http.Header{}
	h.Set("Cookie", cookie.String())
	h.Set("Origin", testConsoleOrigin)
	conn, res, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http:", "ws:", 1)+consolePrefix+"/chat/ws", h)
	if err != nil {
		if res != nil {
			t.Fatalf("console websocket status=%d: %v", res.StatusCode, err)
		}
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestConsoleCrossDeviceOwnerIsolation(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error {
		if state.Vault != "agent" || state.Metadata["channel"] != "console" {
			return fmt.Errorf("escaped console boundary: %s %v", state.Vault, state.Metadata)
		}
		state.FinalAnswer = "family answer"
		return nil
	}))
	alice, a := consoleMemberLogin(t, s, "alice")
	_, b := consoleMemberLogin(t, s, "bobby")
	admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	a2, _, identity := consoleLogin(t, s, "alice", testConsolePassword, testConsoleOrigin)
	if !strings.Contains(fmt.Sprint(identity["capabilities"]), "chat:use") || !strings.Contains(fmt.Sprint(identity["capabilities"]), "sessions:read") {
		t.Error("implemented capabilities missing")
	}
	conn := dialConsole(t, ts, a)
	if err := conn.WriteJSON(map[string]string{"content": "family question", "channel": "internal", "vault": "personal", "owner": "attacker"}); err != nil {
		t.Fatal(err)
	}
	var msg serverMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	sid := msg.SessionID
	if sid == "" || strings.HasPrefix(sid, "console:") {
		t.Fatalf("invalid public SID: %+v", msg)
	}
	if err := conn.ReadJSON(&msg); err != nil || msg.Type != "response" {
		t.Fatalf("answer: %+v %v", msg, err)
	}
	for _, cookie := range []*http.Cookie{a, a2} {
		v := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/sessions", "", cookie, "", "", ""), 200)
		list := v["sessions"].([]any)
		if len(list) != 1 || list[0].(map[string]any)["id"] != sid || len(list[0].(map[string]any)) != 6 {
			t.Fatalf("list: %v", v)
		}
		v = consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/sessions/"+sid+"/messages", "", cookie, "", "", ""), 200)
		if v["id"] != sid || len(v["messages"].([]any)) != 2 {
			t.Fatalf("history: %v", v)
		}
	}
	for _, cookie := range []*http.Cookie{b, admin} {
		for _, id := range []string{sid, "missing"} {
			consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/sessions/"+id+"/messages", "", cookie, "", "", ""), 404)
		}
	}
	if err := s.consoleStore.Logout(a.Value); err != nil {
		t.Fatal(err)
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/sessions/"+sid+"/messages", "", a2, "", "", ""), 200)
	session, err := s.sessionStore.BrowserSession("console", sid, "console:"+alice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	s.sessionMgr.CompleteSession(session)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/sessions/"+sid+"/messages", "", a2, "", "", ""), 200)
	file := filepath.Join(s.cfg.Vaults.Agent, "_sessions", fmt.Sprintf("%x.json", sha256.Sum256([]byte("console:"+sid))))
	if err := os.WriteFile(file, []byte(`{"messages":`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/sessions", "/sessions/" + sid + "/messages"} {
		consoleJSON(t, consoleRequest(s, "GET", consolePrefix+path, "", a2, "", "", ""), 500)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/sessions", "/sessions/" + sid + "/messages"} {
		consoleJSON(t, consoleRequest(s, "GET", consolePrefix+path, "", a2, "", "", ""), 500)
	}
}

func TestConsoleFinalPublicationOrdering(t *testing.T) {
	for _, inside := range []bool{false, true} {
		t.Run(fmt.Sprintf("guard_entered=%v", inside), func(t *testing.T) {
			s, _ := consoleChatFixture(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error {
				state.FinalAnswer = "final family answer"
				return nil
			}))
			user, cookie := consoleMemberLogin(t, s, "alice")
			session, err := s.sessionStore.BrowserSession("console", "boundary", "console:"+user.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, r *http.Request) {
				defer close(finished)
				ctx, stop, err := s.consoleStore.BindSession(r.Context(), cookie.Value)
				if err != nil {
					return
				}
				defer stop()
				u := websocket.Upgrader{}
				conn, err := u.Upgrade(writer, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				go func() { <-ctx.Done(); conn.Close() }()
				w := &wsConn{conn: conn, ctx: ctx, owner: "console:" + user.ID, sessionID: "boundary", session: session, logger: s.logger, sessionMgr: s.sessionMgr, sessionStore: s.sessionStore, chainExecutor: s.chainExecutor, chainRouter: s.chainRouter, filter: s.filterChain,
					options: webChatOptions{ChannelID: "console", VaultName: "agent", WithSession: func(ctx context.Context, fn func(context.Context) error) error {
						if !inside {
							close(entered)
							<-release
						}
						return s.consoleStore.WithSession(ctx, cookie.Value, func(ctx context.Context) error {
							if inside {
								close(entered)
								<-release
							}
							return fn(ctx)
						})
					}},
				}
				w.handleChat("boundary question")
			}))
			defer func() { unblock(); ts.Close() }()
			conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http:", "ws:", 1), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("chat bypassed final session guard")
			}
			revoked := make(chan error, 1)
			go func() { revoked <- s.consoleStore.Logout(cookie.Value) }()
			if inside {
				select {
				case err := <-revoked:
					t.Fatalf("revocation crossed publication: %v", err)
				case <-time.After(30 * time.Millisecond):
				}
			} else {
				if err := <-revoked; err != nil {
					t.Fatal(err)
				}
			}
			unblock()
			var response serverMessage
			err = conn.ReadJSON(&response)
			if inside {
				if err != nil || response.Type != "response" || response.Content != "final family answer" {
					t.Fatalf("active publication lost: %+v %v", response, err)
				}
				if err := <-revoked; err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatalf("revoked publication emitted: %+v", response)
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("chat did not finish")
			}
			messages, err := s.sessionStore.OwnedMessages("console", "boundary", "console:"+user.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if inside {
				want = 2
			}
			if len(messages) != want {
				t.Fatalf("assistant append crossed boundary: %+v", messages)
			}
			if inside {
				cold := core.NewSessionStore(core.NewSessionManager(core.DefaultSessionConfig(), s.logger), filepath.Join(s.cfg.Vaults.Agent, "_sessions"), nil, s.logger)
				messages, err := cold.OwnedMessages("console", "boundary", "console:"+user.ID)
				if err != nil || len(messages) != 2 {
					t.Fatalf("published history not durable: %+v %v", messages, err)
				}
			}
		})
	}
}

func TestConsoleHandshakeRevocationRace(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error { state.FinalAnswer = "answer"; return nil }))
	_, cookie := consoleMemberLogin(t, s, "alice")
	start := make(chan struct{})
	results := make(chan error, 17)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h := http.Header{}
			h.Set("Cookie", cookie.String())
			h.Set("Origin", testConsoleOrigin)
			conn, res, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http:", "ws:", 1)+consolePrefix+"/chat/ws", h)
			if err != nil {
				if res != nil {
					res.Body.Close()
					if res.StatusCode == 401 {
						results <- nil
						return
					}
				}
				results <- err
				return
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(time.Second))
			_, _, err = conn.ReadMessage()
			if err == nil {
				results <- fmt.Errorf("unexpected frame after revocation")
				return
			}
			if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
				results <- fmt.Errorf("idle handshake missed revocation")
				return
			}
			results <- nil
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); <-start; results <- s.consoleStore.Logout(cookie.Value) }()
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestConsoleRevokesIdleAndBusySockets(t *testing.T) {
	for _, action := range []string{"logout", "disable", "password", "expiry"} {
		for _, busy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/busy=%v", action, busy), func(t *testing.T) {
				began := make(chan struct{})
				canceled := make(chan struct{})
				s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(ctx context.Context, state *chain.ChainState) error {
					close(began)
					<-ctx.Done()
					close(canceled)
					state.FinalAnswer = "late private answer"
					return nil
				}))
				user, cookie := consoleMemberLogin(t, s, "alice")
				if action == "expiry" {
					// Reopen real persisted auth state with a controllable clock before binding.
					var offset atomic.Int64
					store, err := console.OpenStore(s.cfg.Console.DataDir, func() time.Time { return time.Now().Add(time.Duration(offset.Load())) })
					if err != nil {
						t.Fatal(err)
					}
					s.consoleStore = store
					offset.Store(int64(7*24*time.Hour - 900*time.Millisecond))
				}
				conn := dialConsole(t, ts, cookie)
				sid := ""
				if busy {
					conn.WriteJSON(clientMessage{Content: "question"})
					var identity serverMessage
					if err := conn.ReadJSON(&identity); err != nil {
						t.Fatal(err)
					}
					sid = identity.SessionID
					select {
					case <-began:
					case <-time.After(time.Second):
						t.Fatal("generation not started")
					}
				}
				var err error
				switch action {
				case "logout":
					err = s.consoleStore.Logout(cookie.Value)
				case "disable":
					_, err = s.consoleStore.UpdateMember(user.ID, "Alice", true)
				case "password":
					err = s.consoleStore.ChangePassword(cookie.Value, testConsolePassword, "new-family-password")
				}
				if err != nil {
					t.Fatal(err)
				}
				var output serverMessage
				if err := conn.ReadJSON(&output); err == nil {
					t.Fatalf("revoked socket published: %+v", output)
				} else if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
					t.Fatal("revoked socket remained open until the client read deadline")
				}
				if busy {
					select {
					case <-canceled:
					case <-time.After(time.Second):
						t.Fatal("inference not canceled")
					}
					messages, err := s.sessionStore.OwnedMessages("console", sid, "console:"+user.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, m := range messages {
						if m.Role == "assistant" {
							t.Fatal("revoked generation appended answer")
						}
					}
				}
			})
		}
	}
}

func TestConsoleWebSocketRejectsForeignOrigin(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	cookie, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/chat/ws", "", cookie, "", "https://evil.example", ""), 403)
}

func TestConsoleHistoryRemainsAvailableWithoutChatDependencies(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	user, cookie := consoleMemberLogin(t, s, "alice")
	session, err := s.sessionStore.BrowserSession("console", "offline-history", "console:"+user.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	s.sessionMgr.AddMessage(session, core.Message{Role: "user", Content: "saved while model was available"})
	if err := s.sessionStore.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	s.chainExecutor, s.chainRouter = nil, nil
	identity := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", cookie, "", "", ""), 200)
	caps := fmt.Sprint(identity["capabilities"])
	if strings.Contains(caps, "chat:use") || !strings.Contains(caps, "sessions:read") {
		t.Fatalf("capabilities ignored actual dependencies: %v", caps)
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/chat/ws", "", cookie, "", testConsoleOrigin, ""), 503)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/sessions/offline-history/messages", "", cookie, "", "", ""), 200)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/sessions", "", cookie, "", "", ""), 200)
	s.sessionStore = nil
	identity = consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", cookie, "", "", ""), 200)
	if strings.Contains(fmt.Sprint(identity["capabilities"]), "sessions:read") {
		t.Fatal("advertised unavailable history")
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/sessions", "", cookie, "", "", ""), 503)
}

// A revoked queued device must not append its input or run inference after the
// other device releases the shared turn; the still-valid device keeps working.
func TestConsoleQueuedRevocationDoesNotAppendOrGenerate(t *testing.T) {
	began, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var queuedCalls atomic.Int32
	s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(ctx context.Context, state *chain.ChainState) error {
		if state.Query == "first" {
			close(began)
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
	user, firstCookie := consoleMemberLogin(t, s, "alice")
	queuedCookie, _, _ := consoleLogin(t, s, "alice", testConsolePassword, testConsoleOrigin)
	first := dialConsole(t, ts, firstCookie)
	if err := first.WriteJSON(clientMessage{Content: "first"}); err != nil {
		t.Fatal(err)
	}
	var identity, response serverMessage
	if err := first.ReadJSON(&identity); err != nil {
		t.Fatal(err)
	}
	sid := identity.SessionID
	select {
	case <-began:
	case <-time.After(time.Second):
		t.Fatal("first inference did not start")
	}
	queued := dialConsole(t, ts, queuedCookie)
	if err := queued.WriteJSON(clientMessage{SessionID: sid, Content: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err := queued.ReadJSON(&identity); err != nil || identity.Type != "session" {
		t.Fatalf("queued resume: %+v %v", identity, err)
	}
	if err := s.consoleStore.Logout(queuedCookie.Value); err != nil {
		t.Fatal(err)
	}
	if err := queued.ReadJSON(&response); err == nil {
		t.Fatalf("revoked queued socket emitted %+v", response)
	} else if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
		t.Fatal("queued socket remained open after logout")
	}
	unblock()
	if err := first.ReadJSON(&response); err != nil || response.Content != "answer first" {
		t.Fatalf("other login interrupted: %+v %v", response, err)
	}
	// A subsequent turn also proves the canceled waiter releases its turn slot.
	if err := first.WriteJSON(clientMessage{Content: "after"}); err != nil {
		t.Fatal(err)
	}
	if err := first.ReadJSON(&response); err != nil || response.Content != "answer after" {
		t.Fatalf("turn remained blocked: %+v %v", response, err)
	}
	messages, err := s.sessionStore.OwnedMessages("console", sid, "console:"+user.ID)
	if err != nil || len(messages) != 4 || queuedCalls.Load() != 0 {
		t.Fatalf("revoked queued work escaped: messages=%+v calls=%d err=%v", messages, queuedCalls.Load(), err)
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, "queued") {
			t.Fatal("revoked queued input entered history")
		}
	}
}

func TestConsoleWriteRespectsPublicationDeadline(t *testing.T) {
	result := make(chan error, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{}
		conn, err := u.Upgrade(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		defer conn.Close()
		ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
		defer cancel()
		writer := &wsConn{conn: conn, logger: zap.NewNop()}
		result <- writer.writeJSONContext(ctx, serverMessage{Type: "response", Content: strings.Repeat("private", 8*1024*1024)})
	}))
	defer ts.Close()
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(ts.URL, "http:", "ws:", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Do not read: a full socket buffer must not hold the account lock forever.
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("unread publication unexpectedly completed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publication ignored its deadline")
	}
}
