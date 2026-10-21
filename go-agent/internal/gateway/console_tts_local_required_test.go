package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

func TestConsoleTTSExplainsMissingLocalServiceBeforeStartingJob(t *testing.T) {
	for _, tc := range []struct {
		name, mode, wantCode string
		message              core.Message
	}{
		{"device_event", "cloud", "speech_local_required", core.Message{AssistantMode: "local", EventID: "device-event"}},
		{"device_result", "cloud", "speech_local_required", core.Message{AssistantMode: "cloud", DeviceResult: &smarthome.DeviceQueryResult{}}},
		{"device_attachment", "cloud", "speech_local_required", core.Message{AssistantMode: "hybrid", Attachments: []core.MessageAttachment{{Kind: "control_proposal", ProposalID: "proposal"}}}},
		{"knowledge_or_legacy", "cloud", "speech_local_required", core.Message{}},
		{"device_hybrid", "hybrid", "speech_local_required", core.Message{AssistantMode: "hybrid", EventID: "device-event"}},
		{"ordinary_explicit_local", "local", "speech_local_unconfigured", core.Message{AssistantMode: "local"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := consoleTestServer(t, testConsoleOrigin)
			owner := consoleBootstrap(t, s)
			cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			v := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/admin/assistant", "", cookie, "", "", ""), 200)
			// No provider credential is configured: even the red run cannot contact cloud.
			update, _ := json.Marshal(map[string]any{"expected_revision": v["revision"], "llm_mode": "local", "asr_mode": "local", "tts_mode": tc.mode})
			consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/assistant", string(update), cookie, csrf, testConsoleOrigin, ""), 200)
			session, err := s.sessionStore.BrowserSession("console", "local-tts-required", "console:"+owner, true)
			if err != nil {
				t.Fatal(err)
			}
			message := tc.message
			message.Role = "assistant"
			message.Content = "Saved answer"
			s.sessionMgr.AddMessage(session, message)
			if err = s.sessionStore.SaveSession(session); err != nil {
				t.Fatal(err)
			}
			request, _ := json.Marshal(map[string]any{"kind": "synthesize", "session_id": "local-tts-required", "message_index": 0, "content_revision": fmt.Sprintf("%x", sha256.Sum256([]byte(message.Content)))})
			response := consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", string(request), cookie, csrf, testConsoleOrigin, ""), 409)
			problem, ok := response["error"].(map[string]any)
			if !ok || problem["code"] != tc.wantCode {
				t.Fatal("missing precise local-service reason", response)
			}
			if _, accepted := response["id"]; accepted {
				t.Fatal("rejected request returned an accepted job", response)
			}
			stale, _ := json.Marshal(map[string]any{"kind": "synthesize", "session_id": "local-tts-required", "message_index": 0, "content_revision": "stale"})
			conflict := consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", string(stale), cookie, csrf, testConsoleOrigin, ""), 409)
			if conflict["error"].(map[string]any)["code"] != "revision_conflict" {
				t.Fatal("local-service check bypassed content revision", conflict)
			}
			_, other := consoleMemberLogin(t, s, "other-tts-owner")
			otherCSRF, _ := s.consoleStore.CSRFToken(other.Value)
			consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", string(request), other, otherCSRF, testConsoleOrigin, ""), 404)
		})
	}
}
func TestConsoleOrdinaryCloudTTSDoesNotRequireLocalService(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	owner := consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	v := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/admin/assistant", "", cookie, "", "", ""), 200)
	update, _ := json.Marshal(map[string]any{"expected_revision": v["revision"], "llm_mode": "cloud", "asr_mode": "local", "tts_mode": "cloud"})
	consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/assistant", string(update), cookie, csrf, testConsoleOrigin, ""), 200)
	session, err := s.sessionStore.BrowserSession("console", "cloud-without-local", "console:"+owner, true)
	if err != nil {
		t.Fatal(err)
	}
	message := core.Message{Role: "assistant", Content: "Public answer", AssistantMode: "cloud"}
	s.sessionMgr.AddMessage(session, message)
	if err = s.sessionStore.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]any{"kind": "synthesize", "session_id": "cloud-without-local", "message_index": 0, "content_revision": fmt.Sprintf("%x", sha256.Sum256([]byte(message.Content)))})
	accepted := consoleJSON(t, consoleRequest(s, http.MethodPost, consolePrefix+"/speech/jobs", string(request), cookie, csrf, testConsoleOrigin, ""), 202)
	// A genuine provider configuration failure remains a provider error, not a
	// local prerequisite. Missing key stops before any external network request.
	deadline := time.Now().Add(2 * time.Second)
	for {
		job := consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/speech/jobs/"+accepted["id"].(string), "", cookie, "", "", ""), 200)
		if job["status"] == "failed" {
			if job["error_code"] != "key_unavailable" {
				t.Fatal("cloud route changed", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cloud job did not finish", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
