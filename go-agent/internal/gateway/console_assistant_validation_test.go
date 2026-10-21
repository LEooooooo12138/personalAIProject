package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestConsoleAssistantReportsInvalidLocalURLField(t *testing.T) {
	for _, tc := range []struct{ field, code string }{
		{"local_asr_url", "local_asr_url_invalid"},
		{"local_tts_url", "local_tts_url_invalid"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			s := consoleTestServer(t, testConsoleOrigin)
			consoleBootstrap(t, s)
			cookie, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			get := func() map[string]any {
				return consoleJSON(t, consoleRequest(s, http.MethodGet, consolePrefix+"/admin/assistant", "", cookie, "", "", ""), http.StatusOK)
			}
			before := get()
			body := map[string]any{
				"expected_revision": before["revision"], "llm_mode": "local", "asr_mode": "local", "tts_mode": "local",
				"local_asr_url": "http://127.0.0.1:8000/asr", "local_tts_url": "http://127.0.0.1:8000/tts",
			}
			// This is a syntactically valid URL, so browser native validation cannot catch it.
			body[tc.field] = "http://example.invalid/private-service?credential=do-not-echo"
			encoded, _ := json.Marshal(body)
			response := consoleRequest(s, http.MethodPut, consolePrefix+"/admin/assistant", string(encoded), cookie, csrf, testConsoleOrigin, "")
			if strings.Contains(response.Body.String(), "do-not-echo") || strings.Contains(response.Body.String(), "example.invalid") {
				t.Fatal("validation response exposed submitted URL")
			}
			got := consoleJSON(t, response, http.StatusBadRequest)
			if got["error"].(map[string]any)["code"] != tc.code {
				t.Errorf("field error = %v, want %s", got["error"], tc.code)
			}
			after := get()
			for _, field := range []string{"revision", "local_asr_url", "local_tts_url"} {
				if after[field] != before[field] {
					t.Errorf("invalid request modified %s", field)
				}
			}
			body[tc.field] = "http://127.0.0.1:9000/corrected"
			encoded, _ = json.Marshal(body)
			corrected := consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/assistant", string(encoded), cookie, csrf, testConsoleOrigin, ""), http.StatusOK)
			if corrected[tc.field] != body[tc.field] {
				t.Fatal("corrected address was not saved")
			}
		})
	}
}
