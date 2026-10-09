package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConsoleConfig(t *testing.T, key, origin, data, listen string, insecure bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := fmt.Sprintf("server:\n  internal_key: %q\n  listen_address: %q\ninference:\n  endpoint: http://127.0.0.1:1\nvaults:\n  personal: %q\n  agent: %q\nconsole:\n  enabled: true\n  data_dir: %q\n  public_origin: %q\n  allow_insecure_http: %t\n", key, listen, filepath.Join(dir, "personal"), filepath.Join(dir, "agent"), data, origin, insecure)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConsoleBootstrapRequiresManagementKey(t *testing.T) {
	for _, key := range []string{"", "   ", "${UNSET_CONSOLE_TEST_KEY}", "prefix-${KEY}"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("UNSET_CONSOLE_TEST_KEY", "")
			path := writeConsoleConfig(t, key, "https://family.example", t.TempDir(), "127.0.0.1:8080", false)
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("enabled console accepted invalid management key")
			}
		})
	}
}

func TestConsoleConfigValidation(t *testing.T) {
	for _, origin := range []string{"", "//family.example", "ftp://family.example", "HTTPS://family.example", "https://2001:db8::1", "https://user@family.example", "https://family.example/", "https://family.example?", "https://family.example#x", "https://family.example:0", "https://family.example:70000", "http://family.example"} {
		t.Run(origin, func(t *testing.T) {
			path := writeConsoleConfig(t, "key", origin, t.TempDir(), "", false)
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("accepted invalid origin")
			}
		})
	}
	for _, listen := range []string{"localhost", "http://localhost:8080", "localhost:abc", "localhost:70000"} {
		t.Run(listen, func(t *testing.T) {
			path := writeConsoleConfig(t, "key", "https://family.example", t.TempDir(), listen, false)
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("accepted invalid listen address")
			}
		})
	}
	for _, origin := range []string{"https://family.example:8443", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		path := writeConsoleConfig(t, "key", origin, t.TempDir(), "127.0.0.1:8080", true)
		if _, err := LoadConfig(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range []string{"personal", "agent", "static"} {
		t.Run("inside-"+root, func(t *testing.T) {
			path := writeConsoleConfig(t, "key", "https://family.example", t.TempDir(), "", false)
			data, _ := os.ReadFile(path)
			target := filepath.Join(filepath.Dir(path), root, "accounts")
			if root == "static" {
				target = filepath.Join("static", "accounts")
			}
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, "  data_dir:") {
					lines[i] = fmt.Sprintf("  data_dir: %q", target)
				}
			}
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("accepted exposed account directory")
			}
		})
	}
}

func TestConsoleBootstrapRejectsCorruptStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"broken":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := writeConsoleConfig(t, "key", "https://family.example", dir, "", false)
	if _, err := Bootstrap(path); err == nil {
		t.Fatal("corrupt account storage allowed bootstrap")
	}
}

func TestConsoleBootstrapRejectsMissingDataDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing-accounts")
	path := writeConsoleConfig(t, "key", "https://family.example", dir, "", false)
	if _, err := Bootstrap(path); err == nil {
		t.Error("bootstrap silently created an empty account store for a missing directory")
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("bootstrap must leave the missing account directory untouched, stat error=%v", err)
	}
}

func TestConsoleConfigResolvesDirectorySymlinks(t *testing.T) {
	for _, root := range []string{"personal", "agent", "static"} {
		t.Run(root, func(t *testing.T) {
			path := writeConsoleConfig(t, "key", "https://family.example", t.TempDir(), "", false)
			protected := filepath.Join(filepath.Dir(path), root)
			if root == "static" {
				protected = filepath.Join(t.TempDir(), "web")
				t.Chdir(filepath.Dir(protected))
				protected = "static"
			}
			if err := os.MkdirAll(protected, 0700); err != nil {
				t.Fatal(err)
			}
			absolute, err := filepath.Abs(protected)
			if err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "alias")
			if err := os.Symlink(absolute, alias); err != nil {
				t.Skipf("directory symlinks unavailable: %v", err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Console.DataDir = filepath.Join(alias, "not-yet-created")
			if err := cfg.ValidateConsole(); err == nil {
				t.Fatal("symlink child exposed account state")
			}
		})
	}
}
