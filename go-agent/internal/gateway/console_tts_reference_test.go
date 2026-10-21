package gateway

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/core"
)

func referenceTestWAV() []byte {
	b := make([]byte, 32044)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	return b
}

func TestConsoleVoiceReferenceLifecycleAndAccess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("reference storage requires Windows DPAPI")
	}
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "voice-member")
	memberCSRF, _ := s.consoleStore.CSRFToken(member.Value)
	endpoint := consolePrefix + "/admin/tts-reference"
	initial := consoleJSON(t, consoleRequest(s, http.MethodGet, endpoint, "", cookie, "", "", ""), 200)
	if initial["exists"] != false {
		t.Fatal(initial)
	}
	encoded := base64.StdEncoding.EncodeToString(referenceTestWAV())
	body := func(revision any) string {
		b, _ := json.Marshal(map[string]any{"filename": "reference.wav", "audio_base64": encoded, "expected_revision": revision})
		return string(b)
	}
	consoleJSON(t, consoleRequest(s, http.MethodGet, endpoint, "", member, "", "", ""), 403)
	consoleJSON(t, consoleRequest(s, http.MethodPut, endpoint, body(initial["revision"]), member, memberCSRF, testConsoleOrigin, ""), 403)
	consoleJSON(t, consoleRequest(s, http.MethodPut, endpoint, body(initial["revision"]), cookie, "", testConsoleOrigin, ""), 403)
	for _, bad := range []string{
		strings.Replace(body(initial["revision"]), "reference.wav", "../reference.wav", 1),
		strings.Replace(body(initial["revision"]), "reference.wav", "reference.txt", 1),
		strings.Replace(body(initial["revision"]), encoded, "aHR0cDovL3ByaXZhdGUvYXVkaW8=", 1),
		`{"filename":"sample.wav","audio_base64":"bad","expected_revision":"","extra":true}`,
	} {
		consoleJSON(t, consoleRequest(s, http.MethodPut, endpoint, bad, cookie, csrf, testConsoleOrigin, ""), 400)
	}
	saved := consoleJSON(t, consoleRequest(s, http.MethodPut, endpoint, body(initial["revision"]), cookie, csrf, testConsoleOrigin, ""), 200)
	if saved["exists"] != true || saved["revision"] == "" || saved["size_bytes"] != float64(len(referenceTestWAV())) {
		t.Fatal(saved)
	}
	read := consoleJSON(t, consoleRequest(s, http.MethodGet, endpoint, "", cookie, "", "", ""), 200)
	if read["revision"] != saved["revision"] || strings.Contains(string(bodyWithoutKey(read)), encoded) {
		t.Fatal("metadata leaked or changed reference")
	}
	status := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/status", "", cookie, "", "", ""), 200)
	if status["voice_reference_configured"] != true || status["tts_model"] != "mimo-v2.5-tts" {
		t.Fatal(status)
	}
	consoleJSON(t, consoleRequest(s, http.MethodPut, endpoint, body(initial["revision"]), cookie, csrf, testConsoleOrigin, ""), 409)
	remove, _ := json.Marshal(map[string]any{"expected_revision": saved["revision"]})
	deleted := consoleJSON(t, consoleRequest(s, http.MethodPost, endpoint+"/remove", string(remove), cookie, csrf, testConsoleOrigin, ""), 200)
	if deleted["exists"] != false || deleted["revision"] == saved["revision"] || deleted["revision"] == "" {
		t.Fatal(deleted)
	}
	consoleJSON(t, consoleRequest(s, http.MethodPost, endpoint+"/remove", string(remove), cookie, csrf, testConsoleOrigin, ""), 409)
	consoleJSON(t, consoleRequest(s, http.MethodPut, endpoint, body(initial["revision"]), cookie, csrf, testConsoleOrigin, ""), 409)
}

func TestConsoleCloneMissingSampleDoesNotBlockPrivateLocalSpeech(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	owner := consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["voice"] != "alloy" || payload["model"] != "tts-1" || r.Header.Get("api-key") != "" {
			t.Error("cloud clone entered private local path")
		}
		_, _ = w.Write(referenceTestWAV())
	}))
	defer local.Close()
	v := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/admin/assistant", "", cookie, "", "", ""), 200)
	b, _ := json.Marshal(map[string]any{"expected_revision": v["revision"], "llm_mode": "local", "asr_mode": "local", "tts_mode": "cloud", "local_tts_url": local.URL, "tts_model": "mimo-v2.5-tts-voiceclone", "tts_voice": "冰糖"})
	consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/assistant", string(b), cookie, csrf, testConsoleOrigin, ""), 200)
	session, err := s.sessionStore.BrowserSession("console", "clone-missing", "console:"+owner, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []core.Message{{Role: "assistant", Content: "public reply", AssistantMode: "cloud"}, {Role: "assistant", Content: "private device reply", AssistantMode: "local", EventID: "device-result"}} {
		s.sessionMgr.AddMessage(session, message)
	}
	if err := s.sessionStore.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	request := func(index int, content string) string {
		b, _ := json.Marshal(map[string]any{"kind": "synthesize", "session_id": "clone-missing", "message_index": index, "content_revision": fmt.Sprintf("%x", sha256.Sum256([]byte(content)))})
		return string(b)
	}
	missing := consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", request(0, "public reply"), cookie, csrf, testConsoleOrigin, ""), 409)
	if !strings.Contains(string(bodyWithoutKey(missing)), "voice_reference_required") {
		t.Fatal(missing)
	}
	job := consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", request(1, "private device reply"), cookie, csrf, testConsoleOrigin, ""), 202)
	deadline := time.Now().Add(2 * time.Second)
	for {
		result := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/jobs/"+job["id"].(string), "", cookie, "", "", ""), 200)
		if result["status"] == "succeeded" {
			break
		}
		if result["status"] == "failed" || time.Now().After(deadline) {
			t.Fatal(result)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestConsoleVoiceReferenceUpdateCancelsActiveSpeech(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("reference storage requires Windows DPAPI")
	}
	s := consoleTestServer(t, testConsoleOrigin)
	owner := consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	started, release := make(chan struct{}), make(chan struct{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer local.Close()
	defer close(release)
	v := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/admin/assistant", "", cookie, "", "", ""), 200)
	b, _ := json.Marshal(map[string]any{"expected_revision": v["revision"], "llm_mode": "local", "asr_mode": "local", "tts_mode": "local", "local_tts_url": local.URL})
	consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/assistant", string(b), cookie, csrf, testConsoleOrigin, ""), 200)
	session, err := s.sessionStore.BrowserSession("console", "reference-cancel", "console:"+owner, true)
	if err != nil {
		t.Fatal(err)
	}
	const content = "ordinary saved reply"
	s.sessionMgr.AddMessage(session, core.Message{Role: "assistant", Content: content, AssistantMode: "cloud"})
	if err = s.sessionStore.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(map[string]any{"kind": "synthesize", "session_id": "reference-cancel", "message_index": 0, "content_revision": fmt.Sprintf("%x", sha256.Sum256([]byte(content)))})
	job := consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", string(b), cookie, csrf, testConsoleOrigin, ""), 202)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("speech did not start")
	}
	b, _ = json.Marshal(map[string]any{"filename": "new.wav", "audio_base64": base64.StdEncoding.EncodeToString(referenceTestWAV()), "expected_revision": ""})
	consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/tts-reference", string(b), cookie, csrf, testConsoleOrigin, ""), 200)
	result := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/jobs/"+job["id"].(string), "", cookie, "", "", ""), 200)
	if result["status"] != "cancelled" {
		t.Fatal(result)
	}
}

// A selected cloud voice is retained when the user switches back to local TTS.
// Its unused reference must not break the shared ASR/TTS configuration status.
func TestConsoleLocalSpeechStatusIgnoresUnusedCloneReferenceFailure(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	v := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/admin/assistant", "", cookie, "", "", ""), 200)
	body, _ := json.Marshal(map[string]any{"expected_revision": v["revision"], "llm_mode": "local", "asr_mode": "local", "tts_mode": "local", "tts_model": "mimo-v2.5-tts-voiceclone", "tts_voice": "冰糖"})
	consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/assistant", string(body), cookie, csrf, testConsoleOrigin, ""), 200)
	if err := os.WriteFile(filepath.Join(s.cfg.Console.DataDir, "secrets", "tts-reference.dpapi"), []byte("invalid encrypted fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	status := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/status", "", cookie, "", "", ""), 200)
	if status["asr_mode"] != "local" || status["tts_mode"] != "local" || status["tts_model"] != "mimo-v2.5-tts-voiceclone" || status["voice_reference_configured"] != false {
		t.Fatal("local speech status changed by unused reference", status)
	}
}
