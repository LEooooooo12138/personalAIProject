package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/core"
)

type consoleModelRoundTrip func(*http.Request) (*http.Response, error)

func (f consoleModelRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type consoleModelCall struct {
	Path string
	Body map[string]any
}

// Uses production Bootstrap, chains, inference clients, authentication and WebSocket
// handling. Only model HTTP responses are simulated; no model or chain is replaced.
// Do not parallelize: DefaultTransport is temporarily a fail-closed test boundary.
func TestConsoleProductionModelHTTPContract(t *testing.T) {
	const localModel = "gemma4:12b"
	const embeddingModel = "bge-m3"
	const localReply = "本地模型契约回复：Nebula资料已读取。"
	const cloudReply = "云端模型契约回复：新会话使用云端模式。"
	const publicFact = "PUBLIC_MODEL_CONTRACT_FACT_91573"
	const fakeKey = "tp-contract-test-only-not-a-real-key"
	const cloudURL = "https://token-plan-sgp.xiaomimimo.com/v1/chat/completions"
	var mu sync.Mutex
	var localCalls []consoleModelCall
	var cloudCalls []map[string]any
	var blocked []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("invalid local model method/content type: %s %s", r.Method, r.Header.Get("Content-Type"))
			http.Error(w, "invalid request", 400)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid JSON", 400)
			return
		}
		mu.Lock()
		localCalls = append(localCalls, consoleModelCall{r.URL.Path, body})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/embeddings":
			if body["model"] != embeddingModel || body["input"] == nil {
				t.Errorf("embedding contract: %v", body)
			}
			io.WriteString(w, `{"data":[{"embedding":[1,0]}]}`)
		case "/v1/chat/completions":
			if body["model"] != localModel || body["stream"] != false || body["max_tokens"] == nil {
				t.Errorf("local chat contract: %v", body)
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": localReply}, "finish_reason": "stop"}}})
		default:
			t.Errorf("unapproved local model path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(model.Close)
	modelURL, err := url.Parse(model.URL)
	if err != nil {
		t.Fatal(err)
	}
	// No proxy, no arbitrary DNS/dials: even the delegated transport may connect
	// only to this exact dynamically allocated mock model socket.
	localTransport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != modelURL.Host {
			return nil, fmt.Errorf("test network dial blocked")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
	}}
	originalTransport := http.DefaultTransport
	gate := consoleModelRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == cloudURL && r.Method == http.MethodPost {
			if r.Header.Get("api-key") != fakeKey || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" {
				t.Error("MiMo header contract differs")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, err
			}
			mu.Lock()
			cloudCalls = append(cloudCalls, body)
			mu.Unlock()
			payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": cloudReply}, "finish_reason": "stop"}}})
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(payload)), Request: r}, nil
		}
		if r.URL.Scheme == "http" && r.URL.Host == modelURL.Host && r.URL.RawQuery == "" && r.URL.User == nil && r.Method == http.MethodPost && (r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/embeddings") {
			return localTransport.RoundTrip(r)
		}
		mu.Lock()
		blocked = append(blocked, r.URL.Host)
		mu.Unlock()
		return nil, fmt.Errorf("test network request blocked")
	})
	http.DefaultTransport = gate
	t.Cleanup(func() { http.DefaultTransport = originalTransport; localTransport.CloseIdleConnections() })
	// Exercise the hard block without dialing or reading runtime credentials.
	request, _ := http.NewRequest(http.MethodGet, "https://unapproved.invalid/", nil)
	if _, err := gate.RoundTrip(request); err == nil {
		t.Fatal("network allowlist failed open")
	}

	configPath := consoleTestConfigPath(t, testConsoleOrigin)
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = []byte(strings.Replace(string(config), "  endpoint: http://127.0.0.1:1", fmt.Sprintf("  endpoint: %s\n  timeout: 3s\n  models:\n    local: %s\n    embedding: %s", model.URL, localModel, embeddingModel), 1))
	config = append(config, []byte("smarthome:\n  enabled: false\n  base_url: ''\n  token: ''\n  oauth_credentials_file: ''\n  control:\n    enabled: false\nchannels:\n  wecom:\n    enabled: false\n")...)
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(configPath)
	// LoadConfig supports environment overrides. Pin endpoint/path/credential-bearing
	// fields to test-owned values before Bootstrap, without reading existing secrets.
	for key, value := range map[string]string{
		"INFERENCE_ENDPOINT": model.URL, "INFERENCE_MODELS_LOCAL": localModel, "INFERENCE_MODELS_EMBEDDING": embeddingModel,
		"VAULTS_AGENT": filepath.Join(root, "agent"), "VAULTS_PERSONAL": filepath.Join(root, "personal"),
		"CONSOLE_DATA_DIR": filepath.Join(root, "accounts"), "CONSOLE_ENABLED": "true", "CONSOLE_PUBLIC_ORIGIN": testConsoleOrigin,
		"SERVER_INTERNAL_KEY": "test-key", "SMARTHOME_ENABLED": "false", "SMARTHOME_BASE_URL": "", "SMARTHOME_TOKEN": "",
		"SMARTHOME_OAUTH_CREDENTIALS_FILE": "", "SMARTHOME_CONTROL_ENABLED": "false", "CHANNELS_WECOM_ENABLED": "false",
	} {
		t.Setenv(key, value)
	}
	if err := os.WriteFile(filepath.Join(root, "agent", "nebula.md"), []byte("---\ntitle: Nebula\ncategory: entity\n---\nNebula "+publicFact), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := core.Bootstrap(configPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Logger.Sync() })
	server := NewServerFromApp(app)
	consoleBootstrap(t, server)
	cookie, csrf, _ := consoleLogin(t, server, "Owner", testConsolePassword, testConsoleOrigin)
	gateway := httptest.NewServer(server.engine)
	t.Cleanup(gateway.Close)
	connection := dialConsole(t, gateway, cookie)
	send := func(t *testing.T, conn *websocket.Conn, content string) string {
		t.Helper()
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(map[string]string{"content": content}); err != nil {
			t.Fatal(err)
		}
		sid := ""
		for {
			var frame serverMessage
			if err := readChatMessage(conn, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Type == "session" {
				sid = frame.SessionID
			}
			if frame.Type == "error" {
				t.Fatalf("production chat rejected: %s", frame.Code)
			}
			if frame.Type == "response" {
				if strings.TrimSpace(frame.Content) == "" {
					t.Fatal("empty ordinary reply")
				}
				want := localReply
				if conn != connection {
					want = cloudReply
				}
				if frame.Content != want {
					t.Fatalf("reply=%q want=%q", frame.Content, want)
				}
				return sid
			}
		}
	}
	counts := func() (chat, embed, cloud int) {
		mu.Lock()
		defer mu.Unlock()
		for _, call := range localCalls {
			if call.Path == "/v1/chat/completions" {
				chat++
			} else {
				embed++
			}
		}
		return chat, embed, len(cloudCalls)
	}
	var localSID string
	t.Run("local_bootstrap_chat_and_embeddings", func(t *testing.T) {
		localSID = send(t, connection, "Nebula有哪些公开资料？")
		if localSID == "" {
			t.Fatal("missing persisted local SID")
		}
		chats, embeddings, clouds := counts()
		if chats != 1 || embeddings < 2 || clouds != 0 {
			t.Fatalf("local HTTP counts chat=%d embedding=%d cloud=%d", chats, embeddings, clouds)
		}
		mu.Lock()
		defer mu.Unlock()
		for _, call := range localCalls {
			if call.Path == "/v1/chat/completions" {
				encoded, _ := json.Marshal(call.Body)
				if !strings.Contains(string(encoded), publicFact) {
					t.Error("real local RAG fact missing from model context")
				}
			}
		}
	})
	t.Run("api_settings_pin_old_sid_and_route_new_sid_to_mimo", func(t *testing.T) {
		settings := consoleJSON(t, consoleRequest(server, http.MethodGet, consolePrefix+"/admin/assistant", "", cookie, "", "", ""), 200)
		update, _ := json.Marshal(map[string]any{"expected_revision": settings["revision"], "llm_mode": "cloud", "asr_mode": "local", "tts_mode": "local", "local_asr_url": "", "local_tts_url": "", "mimo_endpoint": "token_sgp", "api_key": fakeKey})
		updated := consoleJSON(t, consoleRequest(server, http.MethodPut, consolePrefix+"/admin/assistant", string(update), cookie, csrf, testConsoleOrigin, ""), 200)
		encoded, _ := json.Marshal(updated)
		if updated["llm_mode"] != "cloud" || updated["mimo_endpoint"] != "token_sgp" || updated["key_configured"] != true || strings.Contains(string(encoded), fakeKey) {
			t.Fatal("settings contract did not persist safely")
		}
		send(t, connection, "Nebula请继续说明")
		chats, embeddings, clouds := counts()
		if chats != 2 || clouds != 0 {
			t.Fatalf("old SID changed provider: local=%d cloud=%d", chats, clouds)
		}
		next := dialConsole(t, gateway, cookie)
		cloudSID := send(t, next, "Nebula新会话问题")
		if cloudSID == "" || cloudSID == localSID {
			t.Fatal("new mode did not use a new SID")
		}
		afterChat, afterEmbed, afterCloud := counts()
		if afterChat != chats || afterEmbed != embeddings || afterCloud != 1 {
			t.Fatalf("cloud unexpectedly invoked local/RAG: chat=%d embedding=%d cloud=%d", afterChat, afterEmbed, afterCloud)
		}
		mu.Lock()
		payload := cloudCalls[0]
		mu.Unlock()
		if payload["model"] != "mimo-v2.6-flash" || payload["stream"] != false || payload["max_completion_tokens"] != float64(1024) || payload["max_tokens"] != nil || payload["metadata"] != nil {
			t.Fatalf("MiMo JSON contract: %v", payload)
		}
		thinking, ok := payload["thinking"].(map[string]any)
		if !ok || thinking["type"] != "disabled" {
			t.Fatal("MiMo thinking contract changed")
		}
		content, _ := json.Marshal(payload["messages"])
		if !strings.Contains(string(content), "Nebula新会话问题") || strings.Contains(string(content), publicFact) || strings.Contains(string(content), localReply) {
			t.Fatal("new cloud context has wrong user text or leaked local context")
		}
		owner, err := server.consoleStore.Resolve(cookie.Value)
		if err != nil {
			t.Fatal(err)
		}
		// Reopen with a fresh manager to verify durable state, not only live memory.
		reopened := core.NewSessionStore(core.NewSessionManager(core.SessionConfig{MaxSessions: 2}, app.Logger), filepath.Join(root, "agent", "_sessions"), nil, app.Logger)
		for sid, mode := range map[string]string{localSID: "local", cloudSID: "cloud"} {
			session, err := reopened.BrowserSession("console", sid, "console:"+owner.UserID, false)
			if err != nil {
				t.Fatalf("reopen persisted %s session: %v", mode, err)
			}
			snapshot := core.CloneSession(session)
			wantCount, wantReply, wantMessageMode := 2, cloudReply, "cloud"
			if mode == "local" {
				// RAG provenance intentionally stays unclassified to exclude it from
				// later cloud history, while the session itself remains pinned local.
				wantCount, wantReply, wantMessageMode = 4, localReply, ""
			}
			if snapshot.AssistantMode != mode || len(snapshot.Messages) != wantCount {
				t.Fatalf("persisted %s session: mode=%s messages=%d", mode, snapshot.AssistantMode, len(snapshot.Messages))
			}
			last := snapshot.Messages[len(snapshot.Messages)-1]
			if last.Role != "assistant" || last.Content != wantReply || last.AssistantMode != wantMessageMode {
				t.Fatalf("persisted %s reply/provenance: %+v", mode, last)
			}
		}
	})
	verifiedChat, verifiedEmbed, verifiedCloud := counts()
	mu.Lock()
	defer mu.Unlock()
	if len(blocked) != 1 || blocked[0] != "unapproved.invalid" {
		t.Fatalf("unexpected network attempted: %v", blocked)
	}
	t.Logf("production Bootstrap + gateway WS preserved; local chat=%d embeddings=%d intercepted MiMo=%d; no paid/external network", verifiedChat, verifiedEmbed, verifiedCloud)
}
