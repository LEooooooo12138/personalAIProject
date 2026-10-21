package gateway

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

// Removing either device marker from the gateway's local-only policy must fail
// this test, even when an ordinary-conversation marker is also present.
func TestConsoleTTSKeepsDeviceMarkersLocalWithoutAttachments(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	owner := consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	var localCalls atomic.Int32
	var localStatus atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localCalls.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["input"] != "Device state" || r.Header.Get("api-key") != "" {
			t.Error("device speech must use the saved text without cloud credentials")
		}
		if localStatus.Load() != http.StatusOK {
			w.WriteHeader(int(localStatus.Load()))
			return
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
		_, _ = w.Write(wav)
	}))
	defer local.Close()
	for _, route := range []struct {
		name       string
		mode       assistantconfig.Mode
		status     int
		wantStatus string
		wantError  string
	}{
		{"cloud_preference", assistantconfig.Cloud, http.StatusOK, "succeeded", ""},
		{"hybrid_local_failure", assistantconfig.Hybrid, http.StatusServiceUnavailable, "failed", "speech_unavailable"},
	} {
		t.Run(route.name, func(t *testing.T) {
			localStatus.Store(int32(route.status))
			v, err := s.assistantConfig.Get()
			if err != nil {
				t.Fatal(err)
			}
			// Deliberately omit a cloud key: an incorrect cloud route fails
			// before network I/O, so the red run never contacts a real provider.
			_, err = s.assistantConfig.Update(assistantconfig.Update{ExpectedRevision: v.Revision, LLMMode: assistantconfig.Local, ASRMode: assistantconfig.Local, TTSMode: route.mode, LocalTTSURL: local.URL})
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range []struct {
				name    string
				message core.Message
			}{
				{"event_only", core.Message{AssistantMode: "local", EventID: "device-clarification"}},
				{"result_only", core.Message{AssistantMode: "local", DeviceResult: &smarthome.DeviceQueryResult{}}},
				{"event_mislabeled_cloud", core.Message{AssistantMode: "cloud", EventID: "device-query"}},
				{"result_mislabeled_hybrid", core.Message{AssistantMode: "hybrid", DeviceResult: &smarthome.DeviceQueryResult{}}},
			} {
				t.Run(marker.name, func(t *testing.T) {
					sid := route.name + "-" + marker.name
					session, err := s.sessionStore.BrowserSession("console", sid, "console:"+owner, true)
					if err != nil {
						t.Fatal(err)
					}
					msg := marker.message
					msg.Role = "assistant"
					msg.Content = "# Device state"
					s.sessionMgr.AddMessage(session, msg)
					if err := s.sessionStore.SaveSession(session); err != nil {
						t.Fatal(err)
					}
					hash := fmt.Sprintf("%x", sha256.Sum256([]byte(msg.Content)))
					body, _ := json.Marshal(map[string]any{"kind": "synthesize", "session_id": sid, "message_index": 0, "content_revision": hash})
					before := localCalls.Load()
					job := consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", string(body), cookie, csrf, testConsoleOrigin, ""), 202)
					deadline := time.Now().Add(2 * time.Second)
					for {
						current := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/jobs/"+job["id"].(string), "", cookie, "", "", ""), 200)
						if current["status"] != "queued" && current["status"] != "running" {
							gotError, _ := current["error_code"].(string)
							if current["status"] != route.wantStatus || gotError != route.wantError {
								t.Errorf("device TTS changed route: status=%v error=%q; want %s %q", current["status"], gotError, route.wantStatus, route.wantError)
							}
							if calls := localCalls.Load() - before; calls != 1 {
								t.Errorf("local calls=%d, want exactly 1", calls)
							}
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("speech job did not finish")
						}
						time.Sleep(5 * time.Millisecond)
					}
				})
			}
		})
	}
}
