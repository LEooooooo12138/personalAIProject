package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConsoleAssistantSettingsAndPersonality(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	get := func(path string) map[string]any {
		return consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+path, "", cookie, "", "", ""), 200)
	}
	settings := get("/admin/assistant")
	body, _ := json.Marshal(map[string]any{"expected_revision": settings["revision"], "llm_mode": "cloud", "asr_mode": "hybrid", "tts_mode": "local", "local_asr_url": "", "local_tts_url": "", "api_key": "private-test-key"})
	got := consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/assistant", string(body), cookie, csrf, testConsoleOrigin, ""), 200)
	if got["key_configured"] != true || strings.Contains(string(bodyWithoutKey(got)), "private-test-key") {
		t.Fatal("unsafe or missing key configuration")
	}
	consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/assistant", string(body), cookie, csrf, testConsoleOrigin, ""), 409)
	personality := get("/admin/personality")
	upload, _ := json.Marshal(map[string]any{"filename": "character.md", "content": "# 默认人物\n友善而简洁。", "expected_revision": personality["revision"]})
	consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/personality", string(upload), cookie, csrf, testConsoleOrigin, ""), 200)
	data, err := os.ReadFile(filepath.Join(s.cfg.Console.DataDir, "personality", "default.md"))
	if err != nil || !strings.Contains(string(data), "友善") {
		t.Fatalf("fixed file missing: %v", err)
	}
	consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/personality", string(upload), cookie, "", testConsoleOrigin, ""), 403)
	if get("/speech/status")["asr_mode"] != "hybrid" {
		t.Fatal("speech setting not applied")
	}
}
func bodyWithoutKey(v any) []byte { b, _ := json.Marshal(v); return b }
func TestConsoleSpeechRejectsArbitraryTTSAndForeignJobs(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", `{"kind":"synthesize","text":"untrusted arbitrary text"}`, cookie, csrf, testConsoleOrigin, ""), 400)
	consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/jobs/not-owned", "", cookie, "", "", ""), 404)
}

func TestConsoleCloudUsesDirectChainAndPinsExistingConversation(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("local", func(_ context.Context, state *chain.ChainState) error {
		state.FinalAnswer = "local response"
		return nil
	}))
	s.chainRouter.Register("direct", chain.NewChain("direct", "", chain.NewFuncStep("cloud", func(_ context.Context, state *chain.ChainState) error {
		if len(state.Sources) != 0 {
			t.Error("cloud attached knowledge")
		}
		state.FinalAnswer = "cloud response"
		return nil
	})))
	cookie, _, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	updateMode := func(mode assistantconfig.Mode) {
		t.Helper()
		v, _ := s.assistantConfig.Get()
		if _, err := s.assistantConfig.Update(assistantconfig.Update{ExpectedRevision: v.Revision, LLMMode: mode, ASRMode: assistantconfig.Local, TTSMode: assistantconfig.Local}); err != nil {
			t.Fatal(err)
		}
	}
	conn := dialConsole(t, ts, cookie)
	conn.WriteJSON(map[string]string{"content": "first"})
	var m serverMessage
	readChatMessage(conn, &m)
	id := m.SessionID
	readChatMessage(conn, &m)
	if m.Content != "local response" {
		t.Fatalf("initial %v", m)
	}
	updateMode(assistantconfig.Cloud)
	conn.WriteJSON(map[string]string{"content": "same session"})
	readChatMessage(conn, &m)
	if m.Content != "local response" {
		t.Fatalf("existing session changed mode %v", m)
	}
	conn2 := dialConsole(t, ts, cookie)
	conn2.WriteJSON(map[string]string{"content": "new session"})
	readChatMessage(conn2, &m)
	readChatMessage(conn2, &m)
	if m.Content != "cloud response" {
		t.Fatalf("new session did not adopt cloud %v", m)
	}
	p, _ := s.consoleStore.Resolve(cookie.Value)
	messages, err := s.sessionStore.OwnedMessages("console", id, "console:"+p.UserID)
	if err != nil || messages[1].AssistantMode != "local" {
		t.Fatalf("missing provenance %v %v", messages, err)
	}
}

func TestCloudHistoryExcludesControlAndLegacyPairs(t *testing.T) {
	session := &core.Session{Messages: []core.Message{
		{Role: "user", Content: "private legacy"}, {Role: "assistant", Content: "legacy"},
		{Role: "user", Content: "turn on bedroom"}, {Role: "assistant", Content: "device result", Attachments: []core.MessageAttachment{{Kind: "control"}}},
		{Role: "user", Content: "public question"}, {Role: "assistant", Content: "public reply", AssistantMode: "hybrid"},
		{Role: "user", Content: "current question"},
	}}
	got := buildCloudHistory(session)
	if len(got) != 2 || got[0]["content"] != "public question" || got[1]["content"] != "public reply" {
		t.Fatalf("unsafe history %v", got)
	}
}

func TestConsoleTTSUsesOwnedPersistedContentAndEnforcesPrivacy(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	owner := consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["input"] != "Device state" || r.Header.Get("api-key") != "" {
			t.Error("unsafe local TTS request")
		}
		wav := make([]byte, 46)
		copy(wav, "RIFF")
		binary.LittleEndian.PutUint32(wav[4:], 38)
		copy(wav[8:], "WAVEfmt ")
		binary.LittleEndian.PutUint32(wav[16:], 16)
		binary.LittleEndian.PutUint16(wav[20:], 1)
		binary.LittleEndian.PutUint16(wav[22:], 1)
		binary.LittleEndian.PutUint32(wav[24:], 16000)
		binary.LittleEndian.PutUint32(wav[28:], 32000)
		binary.LittleEndian.PutUint16(wav[32:], 2)
		binary.LittleEndian.PutUint16(wav[34:], 16)
		copy(wav[36:], "data")
		binary.LittleEndian.PutUint32(wav[40:], 2)
		w.Write(wav)
	}))
	defer local.Close()
	v, _ := s.assistantConfig.Get()
	if _, err := s.assistantConfig.Update(assistantconfig.Update{ExpectedRevision: v.Revision, LLMMode: assistantconfig.Local, ASRMode: assistantconfig.Local, TTSMode: assistantconfig.Cloud, LocalTTSURL: local.URL}); err != nil {
		t.Fatal(err)
	}
	_, other := consoleMemberLogin(t, s, "other")
	otherCSRF, _ := s.consoleStore.CSRFToken(other.Value)
	session, err := s.sessionStore.BrowserSession("console", "voice-session", "console:"+owner, true)
	if err != nil {
		t.Fatal(err)
	}
	s.sessionMgr.AddMessage(session, core.Message{Role: "assistant", Content: "# Device state", Attachments: []core.MessageAttachment{{Kind: "device"}}})
	if err := s.sessionStore.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("# Device state")))
	body := `{"kind":"synthesize","session_id":"voice-session","message_index":0,"content_revision":"` + hash + `"}`
	consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", body, other, otherCSRF, testConsoleOrigin, ""), 404)
	consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", strings.Replace(body, hash, "stale", 1), cookie, csrf, testConsoleOrigin, ""), 409)
	job := consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", body, cookie, csrf, testConsoleOrigin, ""), 202)
	id := job["id"].(string)
	deadline := time.Now().Add(2 * time.Second)
	for {
		current := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/jobs/"+id, "", cookie, "", "", ""), 200)
		if current["status"] == "succeeded" {
			break
		}
		if current["status"] == "failed" || time.Now().After(deadline) {
			t.Fatalf("sensitive reply did not use local TTS despite cloud preference: %v", current)
		}
		time.Sleep(5 * time.Millisecond)
	}
	consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/jobs/"+id, "", other, "", "", ""), 404)
	consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs/"+id+"/cancel", "{}", other, otherCSRF, testConsoleOrigin, ""), 404)
	consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs/"+id+"/cancel", "{}", cookie, csrf, testConsoleOrigin, ""), 200)
	consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/admin/assistant", "", other, "", "", ""), 403)
}

func TestConsoleSessionRolloverSpeechIndexMatchesPersistedNewSession(t *testing.T) {
	s, ts := consoleChatFixture(t, chain.NewFuncStep("answer", func(_ context.Context, state *chain.ChainState) error {
		state.FinalAnswer = "reply " + state.Query
		return nil
	}))
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["input"] != "reply new question" {
			t.Error("rollover speech used the wrong saved reply")
		}
		_, _ = w.Write(referenceTestWAV())
	}))
	defer local.Close()
	settings, err := s.assistantConfig.Get()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.assistantConfig.Update(assistantconfig.Update{ExpectedRevision: settings.Revision, LLMMode: assistantconfig.Local, ASRMode: assistantconfig.Local, TTSMode: assistantconfig.Local, LocalTTSURL: local.URL}); err != nil {
		t.Fatal(err)
	}
	cfg := core.DefaultSessionConfig()
	cfg.MaxRounds = 1
	s.sessionMgr = core.NewSessionManager(cfg, s.logger)
	dir := t.TempDir()
	s.sessionStore = core.NewSessionStore(s.sessionMgr, dir, nil, s.logger)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	conn := dialConsole(t, ts, cookie)
	if err := conn.WriteJSON(map[string]string{"content": "old question"}); err != nil {
		t.Fatal(err)
	}
	var frame serverMessage
	if err := readChatMessage(conn, &frame); err != nil {
		t.Fatal(err)
	}
	oldSID := frame.SessionID
	if err := readChatMessage(conn, &frame); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(map[string]string{"content": "new question"}); err != nil {
		t.Fatal(err)
	}
	if err := readChatMessage(conn, &frame); err != nil {
		t.Fatal(err)
	}
	newSID := frame.SessionID
	if frame.Type != "session" || newSID == oldSID {
		t.Fatalf("missing rollover: %+v", frame)
	}
	if err := readChatMessage(conn, &frame); err != nil {
		t.Fatal(err)
	}
	principal, err := s.consoleStore.Resolve(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	// A cold store checks persisted content rather than the live session cache.
	cold := core.NewSessionStore(core.NewSessionManager(cfg, s.logger), dir, nil, s.logger)
	messages, err := cold.OwnedMessages("console", newSID, "console:"+principal.UserID)
	if err != nil || len(messages) != 2 || messages[0].Content != "new question" || messages[1].Content != "reply new question" {
		t.Fatalf("new session content: %+v %v", messages, err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(messages[1].Content)))
	body := fmt.Sprintf(`{"kind":"synthesize","session_id":%q,"message_index":1,"content_revision":%q}`, newSID, hash)
	job := consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", body, cookie, csrf, testConsoleOrigin, ""), 202)
	deadline := time.Now().Add(2 * time.Second)
	for {
		current := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/jobs/"+job["id"].(string), "", cookie, "", "", ""), 200)
		if current["status"] == "succeeded" {
			break
		}
		if current["status"] == "failed" || time.Now().After(deadline) {
			t.Fatal("rollover synthesis failed", current)
		}
		time.Sleep(5 * time.Millisecond)
	}
	body = strings.Replace(body, `"message_index":1`, `"message_index":3`, 1)
	consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", body, cookie, csrf, testConsoleOrigin, ""), 404)
	old, err := s.sessionStore.OwnedMessages("console", oldSID, "console:"+principal.UserID)
	if err != nil || len(old) != 2 || old[0].Content != "old question" {
		t.Fatalf("old history changed: %+v %v", old, err)
	}
}
