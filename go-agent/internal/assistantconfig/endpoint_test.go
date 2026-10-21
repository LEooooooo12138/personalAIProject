package assistantconfig

import (
	"encoding/json"
	"errors"
	"runtime"
	"testing"
)

// The store must retain endpoint choice without resubmitting a secret; stale
// clients that omit the new field must not reset a configured region.
func TestMiMoEndpointPersistenceAndValidation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var in Update
	if err := json.Unmarshal([]byte(`{"llm_mode":"cloud","asr_mode":"local","tts_mode":"local","mimo_endpoint":"token_sgp"}`), &in); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Update(in)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(saved)
	var view map[string]any
	_ = json.Unmarshal(raw, &view)
	if view["mimo_endpoint"] != "token_sgp" {
		t.Fatalf("region not persisted: %s", raw)
	}
	in.ExpectedRevision = saved.Revision
	_ = json.Unmarshal([]byte(`{"mimo_endpoint":"https://evil.example/v1"}`), &in)
	if _, err := s.Update(in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("arbitrary credential destination accepted: %v", err)
	}
	saved, err = s.Update(Update{ExpectedRevision: saved.Revision, LLMMode: Cloud, ASRMode: Local, TTSMode: Local})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(saved)
	_ = json.Unmarshal(raw, &view)
	if view["mimo_endpoint"] != "token_sgp" {
		t.Fatal("legacy client reset region")
	}
}

func TestMiMoMismatchedKeyDoesNotReplaceSavedRecord(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI")
	}
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var in Update
	_ = json.Unmarshal([]byte(`{"llm_mode":"cloud","asr_mode":"local","tts_mode":"local","mimo_endpoint":"payg","api_key":"tp-test-not-real"}`), &in)
	if _, err := s.Update(in); err == nil {
		t.Fatal("Token Plan key accepted for pay-as-you-go endpoint")
	}
	initial, _ := s.Get()
	if initial.KeyConfigured {
		t.Fatal("failed update persisted key")
	}
}

func TestMiMoEndpointAllowlistAndKeyFamilies(t *testing.T) {
	for _, tc := range []struct{ selected, key, want string }{
		{"auto", "tp-test", "https://token-plan-cn.xiaomimimo.com/v1"},
		{"", "ttp-test", "https://token-plan-cn.xiaomimimo.com/v1"},
		{"auto", "sk-test", "https://api.xiaomimimo.com/v1"},
		{"token_sgp", "tp-test", "https://token-plan-sgp.xiaomimimo.com/v1"},
		{"token_ams", "ttp-test", "https://token-plan-ams.xiaomimimo.com/v1"},
		{"token_cn", "", "https://token-plan-cn.xiaomimimo.com/v1"},
	} {
		got, err := MiMoBaseURL(tc.selected, tc.key)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %s %v", tc.selected, got, err)
		}
	}
	for _, tc := range []struct{ selected, key string }{{"payg", "tp-test"}, {"token_cn", "sk-test"}, {"https://evil.example", "tp-test"}, {"token_us", "tp-test"}} {
		if _, err := MiMoBaseURL(tc.selected, tc.key); err == nil {
			t.Fatalf("invalid endpoint/key accepted: %s", tc.selected)
		}
	}
}
