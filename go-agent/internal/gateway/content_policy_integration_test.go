package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
	"time"
)

// Missing production directory filtering leaks an untagged archive via ordinary
// authenticated family chat, even with a cache written by the previous version.
func TestFamilyChatExcludesHAArchiveFromActualModelContext(t *testing.T) {
	requests := make(chan string, 4)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/embeddings":
			io.WriteString(w, `{"data":[{"embedding":[1,0]}]}`)
		case "/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			requests <- string(body)
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"fixture response"},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer model.Close()
	path := consoleTestConfigPath(t, testConsoleOrigin)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "http://127.0.0.1:1", model.URL, 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := core.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ root, name, body string }{
		{cfg.Vaults.Agent, "public.md", "---\ntitle: Orion\ncategory: entity\n---\nOrion PUBLIC_FACT_82741"},
		{cfg.Vaults.Agent, "smart-home/rules/old.md", "---\ntitle: Orion\ncategory: entity\n---\nOrion HA_ARCHIVE_SECRET_82741"},
		{cfg.Vaults.Agent, "private.md", "---\ntitle: Orion\ntags: [internal]\n---\nOrion INTERNAL_SECRET_82741"},
		{cfg.Vaults.Personal, "personal.md", "---\ntitle: Orion\ncategory: entity\n---\nOrion OTHER_VAULT_SECRET_82741"},
	} {
		full := filepath.Join(p.root, p.name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(p.body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldReader := vault.NewFileReader(cfg.Vaults.Personal, cfg.Vaults.Agent)
	if _, err := oldReader.Search(context.Background(), "agent", "Orion"); err != nil {
		t.Fatal(err)
	}
	oldStore := vault.NewScopedEmbeddingStore(&ragHTTPInference{}, oldReader, cfg.Vaults.Agent, "agent", "bge-m3", zap.NewNop())
	if _, err := oldStore.Search(context.Background(), "Orion", 10); err != nil {
		t.Fatal(err)
	}
	app, err := core.Bootstrap(path)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Logger.Sync()
	app.Logger = zap.NewNop()
	s := NewServerFromApp(app)
	consoleBootstrap(t, s)
	_, cookie := consoleMemberLogin(t, s, "Reader")
	ts := httptest.NewServer(s.engine)
	defer ts.Close()
	conn := dialConsole(t, ts, cookie)
	defer conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	if err := conn.WriteJSON(map[string]string{"content": "Orion"}); err != nil {
		t.Fatal(err)
	}
	for {
		var message serverMessage
		if err := readChatMessage(conn, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "error" {
			t.Fatalf("chat failed: %+v", message)
		}
		if message.Type == "response" {
			break
		}
	}
	var request struct {
		Messages []struct{ Role, Content string }
	}
	if err := json.Unmarshal([]byte(<-requests), &request); err != nil {
		t.Fatal(err)
	}
	var knowledgeContext string
	referenceCount := 0
	for i, m := range request.Messages {
		// Privacy applies to every outbound message, regardless of role or format.
		for _, secret := range []string{"HA_ARCHIVE_SECRET_82741", "INTERNAL_SECRET_82741", "OTHER_VAULT_SECRET_82741"} {
			if strings.Contains(m.Content, secret) {
				t.Errorf("private unique fact %s reached actual outbound message %d (%s)", secret, i, m.Role)
			}
		}
		if m.Role == "system" && strings.Contains(m.Content, "PUBLIC_FACT_82741") {
			t.Error("raw retrieved knowledge reached system instructions")
		}
		if m.Role != "user" {
			continue
		}
		var reference struct {
			ContextType      string `json:"context_type"`
			KnowledgeContext string `json:"knowledge_context"`
		}
		if err := json.Unmarshal([]byte(m.Content), &reference); err != nil || reference.ContextType != "assistant_reference" {
			continue
		}
		referenceCount++
		knowledgeContext = reference.KnowledgeContext
	}
	if referenceCount != 1 {
		t.Fatalf("want one user reference message, got %d", referenceCount)
	}
	if !strings.Contains(knowledgeContext, "PUBLIC_FACT_82741") {
		t.Fatalf("public unique fact missing from reference knowledge_context: %s", knowledgeContext)
	}
	t.Log("Actual authenticated ordinary chat model context includes PUBLIC_FACT_82741 and excludes archive/internal/other-vault unique facts")
}
