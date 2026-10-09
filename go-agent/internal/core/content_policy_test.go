package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Removing Bootstrap policy injection must expose old, untagged HA rules here.
func TestBootstrapExcludesHAArchiveEvenWhenDisabled(t *testing.T) {
	personal, agent := t.TempDir(), t.TempDir()
	archive := filepath.Join(agent, "smart-home", "rules")
	if err := os.MkdirAll(archive, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archive, "old.md"), []byte("---\ntitle: Orion\ncategory: entity\n---\nHA_ARCHIVE_SECRET_82741"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "public.md"), []byte("Orion PUBLIC_FACT_82741"), 0600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("inference:\n  endpoint: http://127.0.0.1:1\nvaults:\n  personal: %q\n  agent: %q\nsmarthome:\n  enabled: false\n", personal, agent)
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := Bootstrap(path)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Logger.Sync()
	if _, err := app.VaultR.ReadPage(context.Background(), "agent", "smart-home/rules/old.md"); err == nil {
		t.Error("disabled HA archive remains readable")
	}
	hits, err := app.VaultR.Search(context.Background(), "agent", "HA_ARCHIVE_SECRET_82741")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("HA archive leaked through production search: %+v", hits)
	}
	hits, err = app.VaultR.Search(context.Background(), "agent", "PUBLIC_FACT_82741")
	if err != nil || len(hits) != 1 {
		t.Fatalf("public knowledge unavailable: %+v %v", hits, err)
	}
}

// An unsafe archive relationship must fail before session files are initialized.
func TestBootstrapRejectsArchiveContainingVaultBeforeStorage(t *testing.T) {
	for _, relation := range []string{"equal", "ancestor"} {
		t.Run(relation, func(t *testing.T) {
			root := t.TempDir()
			personal := filepath.Join(root, "personal")
			agent := filepath.Join(root, "agent")
			for _, dir := range []string{personal, agent} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			archive := agent
			if relation == "ancestor" {
				archive = root
			}
			config := fmt.Sprintf("inference:\n  endpoint: http://127.0.0.1:1\nvaults:\n  personal: %q\n  agent: %q\nsmarthome:\n  enabled: false\n  agent_vault_path: %q\n", personal, agent, archive)
			path := filepath.Join(root, "agent.yaml")
			if err := os.WriteFile(path, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Bootstrap(path); err == nil {
				t.Fatal("unsafe archive relationship initialized")
			}
			if _, err := os.Stat(filepath.Join(agent, "_sessions")); !os.IsNotExist(err) {
				t.Fatalf("storage initialized before policy rejection: %v", err)
			}
		})
	}
}

// Existing relative Vault configurations remain usable without weakening the
// resulting absolute relationship check, including custom archive locations.
func TestBootstrapPolicySupportsRelativeVaultAndCustomArchive(t *testing.T) {
	for _, where := range []string{"agent", "personal", "external"} {
		t.Run(where, func(t *testing.T) {
			root := t.TempDir()
			personal := filepath.Join(root, "personal")
			agent := filepath.Join(root, "agent")
			for _, dir := range []string{personal, agent} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			archive := filepath.Join(root, where, "operations")
			if where == "external" {
				archive = filepath.Join(root, "external")
			}
			if err := os.MkdirAll(archive, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(archive, "old.md"), []byte("ARCHIVE_SECRET"), 0600); err != nil {
				t.Fatal(err)
			}
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			relPersonal, err := filepath.Rel(cwd, personal)
			if err != nil {
				t.Fatal(err)
			}
			relAgent, err := filepath.Rel(cwd, agent)
			if err != nil {
				t.Fatal(err)
			}
			relArchive, err := filepath.Rel(cwd, archive)
			if err != nil {
				t.Fatal(err)
			}
			config := fmt.Sprintf("inference:\n  endpoint: http://127.0.0.1:1\nvaults:\n  personal: %q\n  agent: %q\nsmarthome:\n  enabled: false\n  agent_vault_path: %q\n", relPersonal, relAgent, relArchive)
			path := filepath.Join(root, "agent.yaml")
			if err := os.WriteFile(path, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			app, err := Bootstrap(path)
			if err != nil {
				t.Fatal(err)
			}
			defer app.Logger.Sync()
			for _, name := range []string{"personal", "agent"} {
				hits, err := app.VaultR.Search(context.Background(), name, "ARCHIVE_SECRET")
				if err != nil || len(hits) != 0 {
					t.Fatalf("archive leaked through %s: %+v %v", name, hits, err)
				}
			}
		})
	}
}
