package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
)

// Bootstrap supports environment overrides; keep every external/account path
// on test-owned data even when run from a configured household service shell.
func pinHistoryPrivacyEnvironment(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"agent", "personal", "accounts"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range map[string]string{
		"INFERENCE_ENDPOINT": "http://127.0.0.1:1",
		"VAULTS_AGENT":       filepath.Join(root, "agent"), "VAULTS_PERSONAL": filepath.Join(root, "personal"),
		"CONSOLE_DATA_DIR": filepath.Join(root, "accounts"), "CONSOLE_ENABLED": "true", "CONSOLE_PUBLIC_ORIGIN": testConsoleOrigin,
		"SERVER_INTERNAL_KEY": "test-key", "SMARTHOME_ENABLED": "false", "SMARTHOME_BASE_URL": "", "SMARTHOME_TOKEN": "",
		"SMARTHOME_OAUTH_CREDENTIALS_FILE": "", "SMARTHOME_CONTROL_ENABLED": "false", "CHANNELS_WECOM_ENABLED": "false",
	} {
		t.Setenv(key, value)
	}
}

func sensitiveSpeechHistories() []struct {
	name    string
	message core.Message
} {
	return []struct {
		name    string
		message core.Message
	}{
		{"event", core.Message{AssistantMode: "local", EventID: "device-event"}},
		{"result", core.Message{AssistantMode: "local", DeviceResult: &smarthome.DeviceQueryResult{EntityID: "binary_sensor.test_door", Name: "测试门", State: "on"}}},
		{"attachment", core.Message{AssistantMode: "local", Attachments: []core.MessageAttachment{{Kind: "control_proposal", ProposalID: "test-proposal"}}}},
		{"legacy", core.Message{}},
	}
}

func updateHistorySpeechConfig(t *testing.T, s *Server, mode assistantconfig.Mode, localURL string) {
	t.Helper()
	settings, err := s.assistantConfig.Get()
	if err != nil {
		t.Fatal(err)
	}
	// Missing cloud key guarantees a wrongly selected cloud route cannot contact a provider.
	_, err = s.assistantConfig.Update(assistantconfig.Update{ExpectedRevision: settings.Revision, LLMMode: assistantconfig.Local, ASRMode: assistantconfig.Local, TTSMode: mode, LocalTTSURL: localURL})
	if err != nil {
		t.Fatal(err)
	}
}

func requestHistorySpeech(t *testing.T, s *Server, cookie *http.Cookie, csrf, sid string, index int, message core.Message) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"kind": "synthesize", "session_id": sid, "message_index": index, "content_revision": fmt.Sprintf("%x", sha256.Sum256([]byte(message.Content)))})
	if err != nil {
		t.Fatal(err)
	}
	return consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", string(body), cookie, csrf, testConsoleOrigin, "")
}

func waitHistorySpeech(t *testing.T, s *Server, cookie *http.Cookie, accepted map[string]any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		job := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/jobs/"+accepted["id"].(string), "", cookie, "", "", ""), 200)
		if job["status"] != "queued" && job["status"] != "running" {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatal("speech job did not finish", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Removing sensitive provenance inheritance must expose both later ordinary
// replies to cloud speech, which these HTTP409 and local routing assertions catch.
func TestConsoleSensitiveHistoryKeepsFollowupSpeechLocal(t *testing.T) {
	for _, route := range []struct {
		name                 string
		mode                 assistantconfig.Mode
		configured           bool
		status               int
		wantStatus, wantCode string
	}{
		{"cloud_without_local", assistantconfig.Cloud, false, 0, "", "speech_local_required"},
		{"cloud_with_local", assistantconfig.Cloud, true, 200, "succeeded", ""},
		{"hybrid_local_failure", assistantconfig.Hybrid, true, 503, "failed", "speech_unavailable"},
	} {
		t.Run(route.name, func(t *testing.T) {
			pinHistoryPrivacyEnvironment(t)
			requests := make(chan string, 2)
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				requests <- string(body)
				fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"Followup includes the saved device state."}}]}`)
			}))
			defer model.Close()
			client := inference.NewOllamaClient(model.URL, time.Second)
			s, ts := consoleChatFixture(t, chain.NewLLMAnswerStep(client, "test-local", 0.7, zap.NewNop()))
			cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			principal, err := s.consoleStore.Resolve(cookie.Value)
			if err != nil {
				t.Fatal(err)
			}
			var localCalls atomic.Int32
			localURL := ""
			if route.configured {
				local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					localCalls.Add(1)
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload["input"] != "Followup includes the saved device state." || r.Header.Get("api-key") != "" || r.Header.Get("Authorization") != "" {
						t.Error("speech used wrong saved text or cloud credentials")
					}
					if route.status != 200 {
						w.WriteHeader(route.status)
						return
					}
					_, _ = w.Write(referenceTestWAV())
				}))
				defer local.Close()
				localURL = local.URL
			}
			updateHistorySpeechConfig(t, s, route.mode, localURL)
			for _, source := range sensitiveSpeechHistories() {
				t.Run(source.name, func(t *testing.T) {
					sid := route.name + "-" + source.name
					session, err := s.sessionStore.BrowserSession("console", sid, "console:"+principal.UserID, true)
					if err != nil {
						t.Fatal(err)
					}
					session.PinAssistantMode("local")
					s.sessionMgr.AddMessage(session, core.Message{Role: "user", Content: "What is the test door state?"})
					message := source.message
					message.Role, message.Content = "assistant", "SENSITIVE_SAVED_TEST_DOOR_STATE"
					s.sessionMgr.AddMessage(session, message)
					if err := s.sessionStore.SaveSession(session); err != nil {
						t.Fatal(err)
					}
					conn := dialConsole(t, ts, cookie)
					before := localCalls.Load()
					for round := 0; round < 2; round++ {
						if err := conn.WriteJSON(clientMessage{SessionID: sid, Content: "Please repeat the previous answer."}); err != nil {
							t.Fatal(err)
						}
						var frame serverMessage
						if round == 0 {
							if err := readChatMessage(conn, &frame); err != nil {
								t.Fatal(err)
							}
							if frame.Type != "session" || frame.SessionID != sid {
								t.Fatalf("wrong session: %+v", frame)
							}
						}
						if err := readChatMessage(conn, &frame); err != nil {
							t.Fatal(err)
						}
						if frame.Type != "response" || frame.Content != "Followup includes the saved device state." {
							t.Fatalf("wrong reply: %+v", frame)
						}
						select {
						case payload := <-requests:
							if !strings.Contains(payload, "SENSITIVE_SAVED_TEST_DOOR_STATE") {
								t.Fatal("sensitive history did not reach the real local model chain")
							}
							if round == 1 && !strings.Contains(payload, "Followup includes the saved device state.") {
								t.Fatal("first followup absent from next model request")
							}
						case <-time.After(time.Second):
							t.Fatal("missing local model request")
						}
						// Reopen a cold store to check the provenance survives persistence.
						cold := core.NewSessionStore(core.NewSessionManager(core.DefaultSessionConfig(), s.logger), s.cfg.Vaults.Agent+"/_sessions", nil, s.logger)
						messages, err := cold.OwnedMessages("console", sid, "console:"+principal.UserID)
						if err != nil {
							t.Fatal(err)
						}
						index := len(messages) - 1
						reply := messages[index]
						response := requestHistorySpeech(t, s, cookie, csrf, sid, index, reply)
						if !route.configured {
							problem := consoleJSON(t, response, 409)
							if problem["error"].(map[string]any)["code"] != "speech_local_required" {
								t.Fatal("wrong speech privacy reason", problem)
							}
							if _, ok := problem["id"]; ok {
								t.Fatal("rejected speech created a job")
							}
						} else {
							job := waitHistorySpeech(t, s, cookie, consoleJSON(t, response, 202))
							code, _ := job["error_code"].(string)
							if job["status"] != route.wantStatus || code != route.wantCode {
								t.Fatalf("sensitive reply changed speech route: %v", job)
							}
							if localCalls.Load()-before != int32(round+1) {
								t.Fatal("sensitive reply did not invoke local TTS exactly once")
							}
						}
						if reply.AssistantMode != "" {
							t.Fatalf("sensitive provenance was not durably inherited: %+v", reply)
						}
					}
				})
			}
		})
	}
}

// Old buggy replies have an ordinary local marker. The speech boundary must
// recover their local-only ancestry without changing stored files or text.
func TestConsoleSavedLocalReplyRetainsEarlierSensitiveHistory(t *testing.T) {
	for _, source := range sensitiveSpeechHistories() {
		t.Run(source.name, func(t *testing.T) {
			pinHistoryPrivacyEnvironment(t)
			s := consoleTestServer(t, testConsoleOrigin)
			owner := consoleBootstrap(t, s)
			cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			updateHistorySpeechConfig(t, s, assistantconfig.Cloud, "")
			const sid = "saved-local-followup"
			session, err := s.sessionStore.BrowserSession("console", sid, "console:"+owner, true)
			if err != nil {
				t.Fatal(err)
			}
			prior := source.message
			prior.Role, prior.Content = "assistant", "Sensitive state"
			s.sessionMgr.AddMessage(session, prior)
			s.sessionMgr.AddMessage(session, core.Message{Role: "user", Content: "Repeat it"})
			reply := core.Message{Role: "assistant", Content: "Saved unmarked local followup", AssistantMode: "local"}
			s.sessionMgr.AddMessage(session, reply)
			if err := s.sessionStore.SaveSession(session); err != nil {
				t.Fatal(err)
			}
			problem := consoleJSON(t, requestHistorySpeech(t, s, cookie, csrf, sid, 2, reply), 409)
			if problem["error"].(map[string]any)["code"] != "speech_local_required" {
				t.Fatal("old sensitive local followup was allowed to leave the device", problem)
			}
		})
	}
}

func TestConsoleOrdinaryLocalReplyStillAllowsCloudSpeech(t *testing.T) {
	for _, laterSensitiveEvent := range []bool{false, true} {
		t.Run(fmt.Sprintf("later_event=%v", laterSensitiveEvent), func(t *testing.T) {
			pinHistoryPrivacyEnvironment(t)
			s := consoleTestServer(t, testConsoleOrigin)
			owner := consoleBootstrap(t, s)
			cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			updateHistorySpeechConfig(t, s, assistantconfig.Cloud, "")
			const sid = "ordinary-local-cloud-speech"
			session, err := s.sessionStore.BrowserSession("console", sid, "console:"+owner, true)
			if err != nil {
				t.Fatal(err)
			}
			s.sessionMgr.AddMessage(session, core.Message{Role: "user", Content: "Public question"})
			reply := core.Message{Role: "assistant", Content: "Public answer", AssistantMode: "local"}
			s.sessionMgr.AddMessage(session, reply)
			if laterSensitiveEvent {
				s.sessionMgr.AddMessage(session, core.Message{Role: "assistant", Content: "Later device state", AssistantMode: "local", EventID: "later-event"})
			}
			if err := s.sessionStore.SaveSession(session); err != nil {
				t.Fatal(err)
			}
			job := waitHistorySpeech(t, s, cookie, consoleJSON(t, requestHistorySpeech(t, s, cookie, csrf, sid, 1, reply), 202))
			if job["status"] != "failed" || job["error_code"] != "key_unavailable" {
				t.Fatal("ordinary local answer did not keep the configured cloud route", job)
			}
		})
	}
}

func TestConsoleOrdinaryLocalHistoryAllowsFollowupCloudSpeech(t *testing.T) {
	pinHistoryPrivacyEnvironment(t)
	requests := make(chan string, 2)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		requests <- string(body)
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"Public local model reply."}}]}`)
	}))
	defer model.Close()
	client := inference.NewOllamaClient(model.URL, time.Second)
	s, ts := consoleChatFixture(t, chain.NewLLMAnswerStep(client, "test-local", 0.7, zap.NewNop()))
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	principal, err := s.consoleStore.Resolve(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	updateHistorySpeechConfig(t, s, assistantconfig.Cloud, "")
	conn := dialConsole(t, ts, cookie)
	sid := ""
	for round := 0; round < 2; round++ {
		if err := conn.WriteJSON(clientMessage{Content: "Public ordinary question"}); err != nil {
			t.Fatal(err)
		}
		var frame serverMessage
		if round == 0 {
			if err := readChatMessage(conn, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Type != "session" {
				t.Fatalf("missing session: %+v", frame)
			}
			sid = frame.SessionID
		}
		if err := readChatMessage(conn, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type != "response" {
			t.Fatalf("missing ordinary reply: %+v", frame)
		}
		select {
		case payload := <-requests:
			if round == 1 && !strings.Contains(payload, "Public local model reply.") {
				t.Fatal("ordinary history missing from the real local chain")
			}
		case <-time.After(time.Second):
			t.Fatal("missing local model request")
		}
		messages, err := s.sessionStore.OwnedMessages("console", sid, "console:"+principal.UserID)
		if err != nil {
			t.Fatal(err)
		}
		index := len(messages) - 1
		if messages[index].AssistantMode != "local" {
			t.Fatalf("ordinary local reply was incorrectly marked sensitive: %+v", messages[index])
		}
		job := waitHistorySpeech(t, s, cookie, consoleJSON(t, requestHistorySpeech(t, s, cookie, csrf, sid, index, messages[index]), 202))
		if job["status"] != "failed" || job["error_code"] != "key_unavailable" {
			t.Fatal("ordinary generated reply lost configured cloud speech", job)
		}
	}
}
