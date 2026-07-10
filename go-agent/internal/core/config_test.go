package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_Minimal(t *testing.T) {
	// Write a temporary config file.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "agent.yaml")
	content := `
server:
  port: 9090
inference:
  endpoint: "http://localhost:11434"
vaults:
  personal: "C:\\Users\\Admin\\vaults\\personal"
  agent: "C:\\Users\\Admin\\vaults\\agent"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.Inference.Endpoint != "http://localhost:11434" {
		t.Errorf("endpoint = %s, want http://localhost:11434", cfg.Inference.Endpoint)
	}
	if cfg.Inference.Timeout == 0 {
		t.Error("timeout should have default")
	}
}

func TestLoadConfig_EnvExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "agent.yaml")
	content := `
server:
  port: 8080
  internal_key: ${TEST_AGENT_KEY}
inference:
  endpoint: "http://localhost:11434"
vaults:
  personal: "C:\\Users\\Admin\\vaults\\personal"
  agent: "C:\\Users\\Admin\\vaults\\agent"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("TEST_AGENT_KEY", "secret-abc")
	defer os.Unsetenv("TEST_AGENT_KEY")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Server.InternalKey != "secret-abc" {
		t.Errorf("internal_key = %q, want secret-abc", cfg.Server.InternalKey)
	}
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "agent.yaml")
	content := `
server:
  port: 8080
inference:
  endpoint: ""
vaults:
  personal: ""
  agent: ""
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for empty endpoint")
	}
}

func TestRoute_AlwaysLocal(t *testing.T) {
	tests := []struct {
		hint     string
		expected string
	}{
		{"", "gemma4:12b"},
		{"auto", "gemma4:12b"},
		{"gemma4:12b", "gemma4:12b"},
		{"deepseek-chat", "deepseek-chat"},
	}

	for _, tt := range tests {
		d := Route(tt.hint)
		if d.TargetModel != tt.expected {
			t.Errorf("Route(%q).TargetModel = %q, want %q", tt.hint, d.TargetModel, tt.expected)
		}
		if d.Fallback != "fail" {
			t.Errorf("Route(%q).Fallback = %q, want fail", tt.hint, d.Fallback)
		}
		if d.Reason == "" {
			t.Errorf("Route(%q).Reason is empty", tt.hint)
		}
	}
}
