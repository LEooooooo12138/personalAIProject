package gateway

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"go.uber.org/zap"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserSessionErrorsAreRecoverableAndTyped(t *testing.T) {
	s, ts := auditServer(t, chain.NewFuncStep("answer", func(_ context.Context, s *chain.ChainState) error { s.FinalAnswer = "answer"; return nil }))
	owner := browserCookie(t, ts)
	conn := dialBrowser(t, ts, owner)
	conn.WriteJSON(clientMessage{SessionID: "expired-sid", Content: "hello"})
	var response map[string]interface{}
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	if response["code"] != "session_not_found" || response["session_id"] != "expired-sid" {
		t.Fatalf("missing recovery identity/code: %#v", response)
	}
	conn.WriteJSON(clientMessage{Content: "fresh question"})
	var identity, answer serverMessage
	if err := conn.ReadJSON(&identity); err != nil {
		t.Fatal(err)
	}
	if err := conn.ReadJSON(&answer); err != nil {
		t.Fatal(err)
	}
	if identity.Type != "session" || answer.Type != "response" {
		t.Fatalf("fresh session failed: %+v %+v", identity, answer)
	}
	cfg := core.DefaultSessionConfig()
	cfg.MaxSessions = 1
	s.sessionMgr = core.NewSessionManager(cfg, zap.NewNop())
	s.sessionStore = core.NewSessionStore(s.sessionMgr, t.TempDir(), nil, zap.NewNop())
	s.sessionMgr.GetOrCreate("internal", "occupied")
	full := dialBrowser(t, ts, owner)
	full.WriteJSON(clientMessage{Content: "full"})
	response = nil
	if err := full.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	if response["code"] != "session_unavailable" {
		t.Fatalf("capacity error must not expire identity: %#v", response)
	}
}

func TestExpiredConnectedSessionCanStartAgain(t *testing.T) {
	s, ts := auditServer(t, chain.NewFuncStep("answer", func(_ context.Context, s *chain.ChainState) error { s.FinalAnswer = "answer"; return nil }))
	cookie := browserCookie(t, ts)
	conn := dialBrowser(t, ts, cookie)
	conn.WriteJSON(clientMessage{Content: "first"})
	var identity, answer serverMessage
	if err := conn.ReadJSON(&identity); err != nil {
		t.Fatal(err)
	}
	if err := conn.ReadJSON(&answer); err != nil {
		t.Fatal(err)
	}
	owner := strings.Split(cookie.Value, ".")[0]
	session, err := s.sessionStore.BrowserSession("webchat", identity.SessionID, owner, false)
	if err != nil {
		t.Fatal(err)
	}
	s.sessionMgr.EndSession(session)
	conn.WriteJSON(clientMessage{SessionID: identity.SessionID, Content: "expired"})
	var failed map[string]interface{}
	if err := conn.ReadJSON(&failed); err != nil {
		t.Fatal(err)
	}
	if failed["code"] != "session_not_found" {
		t.Fatalf("wrong expiry response: %#v", failed)
	}
	conn.WriteJSON(clientMessage{Content: "new conversation"})
	if err := conn.ReadJSON(&identity); err != nil {
		t.Fatal(err)
	}
	if identity.Type != "session" {
		t.Fatalf("socket stayed bound to expired SID: %+v", identity)
	}
	if err := conn.ReadJSON(&answer); err != nil {
		t.Fatal(err)
	}
	if answer.Type != "response" {
		t.Fatalf("new session failed: %+v", answer)
	}
}

func TestSessionStorageFailuresDoNotExpireBrowserIdentity(t *testing.T) {
	for _, failure := range []string{"malformed-json", "read-error"} {
		t.Run(failure, func(t *testing.T) {
			s, ts := auditServer(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error { state.FinalAnswer = "answer"; return nil }))
			dir := t.TempDir()
			s.sessionStore = core.NewSessionStore(s.sessionMgr, dir, nil, zap.NewNop())
			cookie := browserCookie(t, ts)
			owner := strings.Split(cookie.Value, ".")[0]
			session, err := s.sessionStore.BrowserSession("webchat", "persisted", owner, true)
			if err != nil {
				t.Fatal(err)
			}
			s.sessionMgr.AddMessage(session, core.Message{Role: "user", Content: "saved question"})
			s.sessionMgr.AddMessage(session, core.Message{Role: "assistant", Content: "saved answer"})
			if err := s.sessionStore.SaveSession(session); err != nil {
				t.Fatal(err)
			}
			s.sessionMgr.CompleteSession(session)
			file := filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte("webchat:persisted"))))
			valid, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "malformed-json" {
				if err := os.WriteFile(file, []byte(`{"messages":`), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			}
			conn := dialBrowser(t, ts, cookie)
			conn.WriteJSON(clientMessage{SessionID: "persisted", Content: "retry"})
			var response serverMessage
			if err := conn.ReadJSON(&response); err != nil {
				t.Fatal(err)
			}
			if response.Code != "session_unavailable" || response.SessionID != "persisted" {
				t.Errorf("storage failure must preserve client SID: %+v", response)
			}
			request, _ := http.NewRequest("GET", ts.URL+"/sessions/webchat/persisted/messages", nil)
			request.AddCookie(cookie)
			history, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			history.Body.Close()
			if history.StatusCode != http.StatusInternalServerError {
				t.Errorf("storage failure must not be history 404: %d", history.StatusCode)
			}
			// Repair the storage only; the same cookie and SID must recover the same history.
			if failure == "read-error" {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(file, valid, 0600); err != nil {
				t.Fatal(err)
			}
			conn.WriteJSON(clientMessage{SessionID: "persisted", Content: "retry after recovery"})
			if err := conn.ReadJSON(&response); err != nil {
				t.Fatal(err)
			}
			if response.Type != "session" || response.SessionID != "persisted" {
				t.Fatalf("known session was lost: %+v", response)
			}
			if err := conn.ReadJSON(&response); err != nil {
				t.Fatal(err)
			}
			if response.Type != "response" {
				t.Fatalf("recovered session failed: %+v", response)
			}
			messages := s.sessionStore.GetMessages("webchat", "persisted")
			if len(messages) != 4 || messages[0].Content != "saved question" || messages[1].Content != "saved answer" {
				t.Fatalf("persisted history lost on recovery: %#v", messages)
			}
		})
	}
}
