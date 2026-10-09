package vault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type triggerProvider interface {
	TriggerEntities(context.Context, string) ([]string, error)
}

func TestTriggersAreScopedAndDynamic(t *testing.T) {
	personal, agent := t.TempDir(), t.TempDir()
	reader := NewFileReader(personal, agent)
	provider, ok := interface{}(reader).(triggerProvider)
	if !ok {
		t.Fatal("reader has no scoped dynamic trigger provider")
	}
	write := func(root, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(personal, "Secret.md", "---\ntitle: Secret\ncategory: entity\n---\nprivate marker")
	check := func(want []string) {
		t.Helper()
		got, err := provider.TriggerEntities(context.Background(), "agent")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("entities=%v want %v err=%v", got, want, err)
		}
	}
	check([]string{})
	write(agent, "one.md", "---\ntitle: Orion\ncategory: entity\n---\npublic")
	check([]string{"Orion"})
	info, err := os.Stat(filepath.Join(agent, "one.md"))
	if err != nil {
		t.Fatal(err)
	}
	write(agent, "one.md", "---\ntitle: Lyrae\ncategory: entity\n---\npublic")
	if err := os.Chtimes(filepath.Join(agent, "one.md"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	check([]string{"Lyrae"})
	if err := os.Remove(filepath.Join(agent, "one.md")); err != nil {
		t.Fatal(err)
	}
	check([]string{})
	for _, meta := range []string{"tags: [internal]", "tags: internal"} {
		write(agent, "one.md", "---\ntitle: Orion\ncategory: entity\n"+meta+"\n---\nprivate")
		check([]string{})
	}
	write(agent, "one.md", "---\ntitle: Orion\ncategory: entity\n---\npublic")
	check([]string{"Orion"})
	write(agent, "two.md", "---\ntitle: Orion\ncategory: entity\n---\npublic")
	check([]string{"Orion"})
	for _, dir := range []string{".hidden", "_private"} {
		if err := os.Mkdir(filepath.Join(agent, dir), 0700); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(agent, dir), "bad.md", "---\ntitle: Hidden\n---\nsecret")
	}
	write(agent, "index.md", "---\ntitle: System\n---\nsecret")
	check([]string{"Orion"})
	if err := os.RemoveAll(personal); err != nil {
		t.Fatal(err)
	}
	check([]string{"Orion"})
	if _, err := provider.TriggerEntities(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown vault accepted")
	}
	if _, err := provider.TriggerEntities(context.Background(), "personal"); err == nil {
		t.Fatal("missing vault failure swallowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.TriggerEntities(ctx, "agent"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
func TestTriggersRejectSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("---\ntitle: Secret\n---\nsecret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	provider, ok := interface{}(NewFileReader(root, root)).(triggerProvider)
	if !ok {
		t.Fatal("missing provider")
	}
	if entities, err := provider.TriggerEntities(context.Background(), "agent"); err == nil || len(entities) != 0 {
		t.Fatalf("outside link not rejected: %v %v", entities, err)
	}
}

func TestTriggersDoNotPromoteFilenameDisplayFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Plain.md"), []byte("normal markdown"), 0600); err != nil {
		t.Fatal(err)
	}
	entities, err := NewFileReader(root, root).TriggerEntities(context.Background(), "agent")
	if err != nil || len(entities) != 0 {
		t.Fatalf("display fallback became trigger: %v %v", entities, err)
	}
}
