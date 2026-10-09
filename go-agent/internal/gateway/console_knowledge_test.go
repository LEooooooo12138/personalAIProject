package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func knowledgeFixture(t *testing.T) *Server {
	t.Helper()
	s := consoleTestServer(t, testConsoleOrigin)
	installed := false
	for _, r := range s.engine.Routes() {
		if r.Path == consolePrefix+"/knowledge/search" {
			installed = true
		}
	}
	// Optional registration makes RED a real 404 rather than an undefined symbol.
	if !installed {
		if setup, ok := any(s).(interface{ setupConsoleKnowledgeRoutes(*gin.RouterGroup) }); ok {
			setup.setupConsoleKnowledgeRoutes(s.engine.Group(consolePrefix))
		}
	}
	consoleBootstrap(t, s)
	return s
}
func knowledgeWrite(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

type knowledgeResponse struct {
	code        int
	body, cache string
}

func knowledgeGet(s *Server, route string, cookie *http.Cookie) *knowledgeResponse {
	w := consoleRequest(s, "GET", consolePrefix+route, "", cookie, "", "", "")
	return &knowledgeResponse{w.Code, w.Body.String(), w.Header().Get("Cache-Control")}
}
func knowledgePost(s *Server, route, body string, cookie *http.Cookie, csrf string) *knowledgeResponse {
	w := consoleRequest(s, "POST", consolePrefix+route, body, cookie, csrf, testConsoleOrigin, "")
	return &knowledgeResponse{w.Code, w.Body.String(), w.Header().Get("Cache-Control")}
}
func (r *knowledgeResponse) want(t *testing.T, status int) map[string]any {
	t.Helper()
	if r.code != status {
		t.Fatalf("status=%d want=%d body=%s", r.code, status, r.body)
	}
	if r.cache != "no-store" {
		t.Fatal("missing no-store")
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(r.body), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

// Removing fixed-vault/page visibility exposes private sentinels and counts.
func TestConsoleKnowledgePublicAndPrivateBoundaries(t *testing.T) {
	s := knowledgeFixture(t)
	knowledgeWrite(t, s.cfg.Vaults.Agent, "public.md", "---\ntitle: Orion\n---\nOrion PUBLIC_FACT_109")
	knowledgeWrite(t, s.cfg.Vaults.Personal, "public.md", "---\ntitle: Private Orion\n---\nPERSONAL_SECRET_109")
	knowledgeWrite(t, s.cfg.Vaults.Agent, "private.md", "---\ntitle: INTERNAL_SECRET_109\ntags: [internal]\n---\nOrion INTERNAL_SECRET_109")
	knowledgeWrite(t, s.cfg.Vaults.Agent, "broken.md", "---\ntitle: [invalid]\n---\nOrion BROKEN_SECRET_109")
	archive, err := filepath.Rel(s.cfg.Vaults.Agent, s.cfg.SmartHome.AgentVaultPath)
	if err != nil {
		t.Fatal(err)
	}
	knowledgeWrite(t, s.cfg.Vaults.Agent, filepath.Join(archive, "old.md"), "---\ntitle: ARCHIVE_SECRET_109\n---\nOrion ARCHIVE_SECRET_109")
	_, member := consoleMemberLogin(t, s, "reader")
	admin, _, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	knowledgeGet(s, "/knowledge/page?path=public.md", nil).want(t, 401)
	page := knowledgeGet(s, "/knowledge/page?path=public.md", member).want(t, 200)
	if len(page) != 3 || page["path"] != "public.md" || page["title"] != "Orion" || page["body"] != "Orion PUBLIC_FACT_109" {
		t.Fatalf("page=%v", page)
	}
	response := knowledgeGet(s, "/knowledge/search?q=Orion", member)
	data := response.want(t, 200)
	if data["count"] != float64(1) || len(data["results"].([]any)) != 1 {
		t.Fatalf("results=%v", data)
	}
	for _, secret := range []string{"PERSONAL_SECRET", "INTERNAL_SECRET", "ARCHIVE_SECRET", "BROKEN_SECRET", s.cfg.Vaults.Agent} {
		if strings.Contains(response.body, secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	for _, p := range []string{"private.md", "broken.md", filepath.ToSlash(filepath.Join(archive, "old.md")), "../personal/public.md", "C:/outside.md", "index.md", "missing.md"} {
		knowledgeGet(s, "/knowledge/page?path="+url.QueryEscape(p), member).want(t, 404)
	}
	for _, p := range []string{"/admin/vault/status", "/admin/vault/search?q=Orion", "/admin/vault/page?path=public.md"} {
		knowledgeGet(s, p, member).want(t, 403)
	}
	privateSearch := knowledgeGet(s, "/admin/vault/search?q=Orion", admin)
	privateResults := privateSearch.want(t, 200)
	if privateResults["count"] != float64(1) || !strings.Contains(privateSearch.body, "PERSONAL_SECRET_109") || strings.Contains(privateSearch.body, "PUBLIC_FACT_109") {
		t.Fatalf("private search used the wrong Vault: %s", privateSearch.body)
	}
	if p := knowledgeGet(s, "/admin/vault/page?path=public.md", admin).want(t, 200); p["body"] != "PERSONAL_SECRET_109" {
		t.Fatalf("private=%v", p)
	}
	response = knowledgeGet(s, "/admin/vault/status", admin)
	status := response.want(t, 200)
	if len(status) != 3 || status["vault"] != "personal" || status["page_count"] != float64(1) || strings.Contains(response.body, s.cfg.Vaults.Personal) {
		t.Fatalf("status=%s", response.body)
	}
	if data := knowledgeGet(s, "/knowledge/search?q=missing", member).want(t, 200); data["count"] != float64(0) || len(data["results"].([]any)) != 0 {
		t.Fatalf("empty=%v", data)
	}
	for _, q := range []string{"", strings.Repeat("中", 201)} {
		knowledgeGet(s, "/knowledge/search?q="+url.QueryEscape(q), member).want(t, 422)
	}
	if err := os.Symlink(filepath.Join(s.cfg.Vaults.Personal, "public.md"), filepath.Join(s.cfg.Vaults.Agent, "alias.md")); err != nil {
		t.Fatal(err)
	}
	knowledgeGet(s, "/knowledge/page?path=alias.md", member).want(t, 404)
}

type knowledgeReader struct {
	vault.Reader
	search func(context.Context, string, string) ([]vault.SearchResult, error)
	read   func(context.Context, string, string) (*vault.Page, error)
}

func (r knowledgeReader) Search(ctx context.Context, v, q string) ([]vault.SearchResult, error) {
	if r.search != nil {
		return r.search(ctx, v, q)
	}
	return r.Reader.Search(ctx, v, q)
}

// Cached candidate title/snippet must disappear when the file turns internal.
func TestConsoleKnowledgeSearchRevalidatesCurrentPages(t *testing.T) {
	s := knowledgeFixture(t)
	reader := s.vaultR
	knowledgeWrite(t, s.cfg.Vaults.Agent, "public.md", "---\ntitle: Orion\n---\nOrion PUBLIC_NOW")
	knowledgeWrite(t, s.cfg.Vaults.Agent, "changed.md", "---\ntitle: Orion old\n---\nOrion OLD_SECRET")
	s.vaultR = knowledgeReader{Reader: reader, search: func(ctx context.Context, v, q string) ([]vault.SearchResult, error) {
		results, err := reader.Search(ctx, v, q)
		knowledgeWrite(t, s.cfg.Vaults.Agent, "changed.md", "---\ntitle: HIDDEN_TITLE\ntags: [internal]\n---\nHIDDEN_BODY")
		return results, err
	}}
	_, member := consoleMemberLogin(t, s, "reader")
	response := knowledgeGet(s, "/knowledge/search?q=Orion", member)
	data := response.want(t, 200)
	if data["count"] != float64(1) || strings.Contains(response.body, "OLD_SECRET") || strings.Contains(response.body, "HIDDEN") {
		t.Fatalf("stale private candidate: %s", response.body)
	}
	s.vaultR = knowledgeReader{Reader: reader, search: func(context.Context, string, string) ([]vault.SearchResult, error) {
		return nil, errors.New("private path /credential token=SECRET")
	}}
	response = knowledgeGet(s, "/knowledge/search?q=Orion", member)
	response.want(t, 503)
	if strings.Contains(response.body, "SECRET") {
		t.Fatal("raw search error")
	}
}

type knowledgeInference struct {
	chat func(context.Context, json.RawMessage) (json.RawMessage, error)
}

func (f knowledgeInference) Chat(ctx context.Context, b json.RawMessage) (json.RawMessage, error) {
	return f.chat(ctx, b)
}
func (f knowledgeInference) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("dense not used in sparse fixture")
}
func (f knowledgeInference) ListModels(context.Context) ([]inference.ModelInfo, error) {
	return nil, nil
}
func knowledgeChains(t *testing.T, s *Server, infer inference.Client, logger *zap.Logger) {
	t.Helper()
	router, err := chain.BuildRAGChatChain(s.vaultR, infer, "fixture-model", nil, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	wiki, err := chain.BuildWikiIngestChain(s.vaultR, s.vaultW, infer, "fixture-model", s.cfg.Vaults.Personal, s.cfg.Vaults.Agent, logger)
	if err != nil {
		t.Fatal(err)
	}
	c, err := wiki.Route("wiki-ingest")
	if err != nil {
		t.Fatal(err)
	}
	router.Register("wiki-ingest", c)
	s.chainExecutor = chain.NewChainExecutor(logger, router)
}

// Real chain steps with external model IO replaced; wrong Vault breaks context.
func TestConsoleKnowledgePrivateChainsAndNoHistory(t *testing.T) {
	s := knowledgeFixture(t)
	knowledgeWrite(t, s.cfg.Vaults.Personal, "orion.md", "---\ntitle: Orion\n---\nOrion PRIVATE_FACT_56321")
	knowledgeWrite(t, s.cfg.Vaults.Agent, "orion.md", "---\ntitle: Orion\n---\nOrion WRONG_VAULT_56321")
	var sent []string
	infer := knowledgeInference{func(_ context.Context, b json.RawMessage) (json.RawMessage, error) {
		sent = append(sent, string(b))
		var req struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.Unmarshal(b, &req); err != nil {
			return nil, err
		}
		if len(req.Messages) == 1 {
			return json.RawMessage(`{"choices":[{"message":{"content":"---\ntitle: Imported Fixture\ncategory: concepts\n---\nIMPORTED_PRIVATE_109"}}]}`), nil
		}
		return json.RawMessage(`{"choices":[{"message":{"content":"PRIVATE_FACT_56321"}}]}`), nil
	}}
	logCore, logs := observer.New(zap.InfoLevel)
	knowledgeChains(t, s, infer, zap.New(logCore))
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	response := knowledgePost(s, "/admin/knowledge/query", `{"query":"Orion PRIVATE_QUERY_783"}`, admin, csrf)
	data := response.want(t, 200)
	sources := data["sources"].([]any)
	if len(data) != 2 || data["answer"] != "PRIVATE_FACT_56321" || len(sources) != 1 {
		t.Fatalf("answer=%v", data)
	}
	src := sources[0].(map[string]any)
	if len(src) != 3 || src["path"] != "orion.md" || src["title"] != "Orion" {
		t.Fatalf("source=%v", src)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "PRIVATE_FACT_56321") || !strings.Contains(sent[0], "Orion") || strings.Contains(sent[0], "WRONG_VAULT") {
		t.Fatal("wrong actual model context")
	}
	if strings.Contains(fmt.Sprint(logs.All()), "PRIVATE_QUERY_783") {
		t.Fatal("private query logged at Info")
	}
	if sessions := knowledgeGet(s, "/sessions", admin).want(t, 200)["sessions"].([]any); len(sessions) != 0 {
		t.Fatal("private query persisted chat history")
	}
	if _, err := os.Stat(filepath.Join(s.cfg.Vaults.Agent, "index.md")); !os.IsNotExist(err) {
		t.Fatalf("query mutated agent index: %v", err)
	}
	response = knowledgePost(s, "/admin/wiki/ingest", `{"content":"IMPORT_INPUT_109","source_title":"fixture"}`, admin, csrf)
	data = response.want(t, 200)
	if len(data) != 3 || data["vault"] != "personal" || data["title"] != "Imported Fixture" {
		t.Fatalf("ingest=%v", data)
	}
	p := data["path"].(string)
	page, err := s.vaultR.ReadPage(context.Background(), "personal", p)
	if err != nil || page.Body != "IMPORTED_PRIVATE_109" {
		t.Fatalf("page=%v err=%v", page, err)
	}
	if _, err := s.vaultR.ReadPage(context.Background(), "agent", p); err == nil {
		t.Fatal("import published agent page")
	}
	if len(sent) != 2 || !strings.Contains(sent[1], "IMPORT_INPUT_109") {
		t.Fatal("raw content absent in ingest request")
	}
}
func TestConsoleKnowledgeMutationValidationAndFailures(t *testing.T) {
	s := knowledgeFixture(t)
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "reader")
	for _, route := range []string{"/admin/knowledge/query", "/admin/wiki/ingest"} {
		knowledgePost(s, route, `{"query":"x"}`, member, csrf).want(t, 403)
		knowledgePost(s, route, `{"query":"x"}`, admin, "").want(t, 403)
	}
	for _, body := range []string{`null`, `{"query":""}`, `{"query":"x","vault":"agent"}`, `{"query":"x"} {}`} {
		knowledgePost(s, "/admin/knowledge/query", body, admin, csrf).want(t, 422)
	}
	for _, body := range []string{`{"content":""}`, `{"content":"x","source_title":123}`, `{"content":"x","owner":"other"}`} {
		knowledgePost(s, "/admin/wiki/ingest", body, admin, csrf).want(t, 422)
	}
	knowledgeChains(t, s, knowledgeInference{func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("private token=MODEL_SECRET /absolute/path")
	}}, zap.NewNop())
	for _, tc := range []struct{ route, body string }{{"/admin/knowledge/query", `{"query":"Orion"}`}, {"/admin/wiki/ingest", `{"content":"fixture"}`}} {
		response := knowledgePost(s, tc.route, tc.body, admin, csrf)
		response.want(t, 503)
		if strings.Contains(response.body, "MODEL_SECRET") || strings.Contains(response.body, "/absolute") {
			t.Fatal("raw chain failure exposed")
		}
	}
}

// A model result completing after logout must not publish private business JSON.
func TestConsoleKnowledgeRejectsRevokedInFlightPrivateAnswer(t *testing.T) {
	s := knowledgeFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	knowledgeChains(t, s, knowledgeInference{func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		select {
		case <-release:
			return json.RawMessage(`{"choices":[{"message":{"content":"LATE_PRIVATE_SECRET"}}]}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}, zap.NewNop())
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	finished := make(chan *knowledgeResponse, 1)
	go func() { finished <- knowledgePost(s, "/admin/knowledge/query", `{"query":"Orion"}`, admin, csrf) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach model")
	}
	if err := s.consoleStore.Logout(admin.Value); err != nil {
		t.Fatal(err)
	}
	close(release)
	response := <-finished
	response.want(t, 401)
	if strings.Contains(response.body, "LATE_PRIVATE_SECRET") {
		t.Fatal("revoked answer published")
	}
}

func (r knowledgeReader) ReadPage(ctx context.Context, v, p string) (*vault.Page, error) {
	if r.read != nil {
		return r.read(ctx, v, p)
	}
	return r.Reader.ReadPage(ctx, v, p)
}

// Dropping a failed candidate must not make an IO outage look like no results.
func TestConsoleKnowledgeSearchReadFailureIsNotEmptySuccess(t *testing.T) {
	s := knowledgeFixture(t)
	reader := s.vaultR
	knowledgeWrite(t, s.cfg.Vaults.Agent, "public.md", "---\ntitle: Orion\n---\nOrion")
	s.vaultR = knowledgeReader{Reader: reader, read: func(context.Context, string, string) (*vault.Page, error) {
		return nil, errors.New("fixture IO outage /private SECRET")
	}}
	_, member := consoleMemberLogin(t, s, "reader")
	response := knowledgeGet(s, "/knowledge/search?q=Orion", member)
	response.want(t, 503)
	if strings.Contains(response.body, "SECRET") {
		t.Fatal("raw read error exposed")
	}
}

func TestConsoleKnowledgeLengthLimitsAndRestrictedIdentity(t *testing.T) {
	s := knowledgeFixture(t)
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	calls := 0
	knowledgeChains(t, s, knowledgeInference{func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"choices":[{"message":{"content":"fixture answer"}}]}`), nil
	}}, zap.NewNop())
	for _, tc := range []struct {
		route string
		input any
	}{{"/admin/knowledge/query", map[string]string{"query": strings.Repeat("中", 4001)}}, {"/admin/wiki/ingest", map[string]string{"content": strings.Repeat("中", 100001)}}, {"/admin/wiki/ingest", map[string]string{"content": "x", "source_title": strings.Repeat("中", 201)}}} {
		body, _ := json.Marshal(tc.input)
		knowledgePost(s, tc.route, string(body), admin, csrf).want(t, 422)
	}
	if calls != 0 {
		t.Fatal("overlength input reached model")
	}
	body, _ := json.Marshal(map[string]string{"query": strings.Repeat("中", 4000)})
	knowledgePost(s, "/admin/knowledge/query", string(body), admin, csrf).want(t, 200)
	_, password, err := s.consoleStore.CreateMember("restricted", "restricted")
	if err != nil {
		t.Fatal(err)
	}
	cookie, _, _ := consoleLogin(t, s, "restricted", password, testConsoleOrigin)
	knowledgeGet(s, "/knowledge/search?q=Orion", cookie).want(t, 403)
	knowledgeGet(s, "/knowledge/page?path=public.md", cookie).want(t, 403)
}

func TestConsoleKnowledgePrivateModelTimeoutCancelsRequest(t *testing.T) {
	s := knowledgeFixture(t)
	s.cfg.Server.ChainTimeout = 25 * time.Millisecond
	canceled := make(chan struct{}, 1)
	knowledgeChains(t, s, knowledgeInference{func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		canceled <- struct{}{}
		return nil, ctx.Err()
	}}, zap.NewNop())
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	knowledgePost(s, "/admin/knowledge/query", `{"query":"fixture"}`, admin, csrf).want(t, 503)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("model not canceled")
	}
}

// Logout must cancel the actual model request before production ingest writes.
func TestConsoleKnowledgeRevocationCancelsIngestBeforeWrite(t *testing.T) {
	s := knowledgeFixture(t)
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	knowledgeChains(t, s, knowledgeInference{func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		select {
		case <-ctx.Done():
			close(canceled)
			return nil, ctx.Err()
		case <-release:
			return json.RawMessage(`{"choices":[{"message":{"content":"---\ntitle: Should Never Write\ncategory: concepts\n---\nREVOKED_PRIVATE"}}]}`), nil
		}
	}}, zap.NewNop())
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	finished := make(chan *knowledgeResponse, 1)
	go func() {
		finished <- knowledgePost(s, "/admin/wiki/ingest", `{"content":"private fixture"}`, admin, csrf)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("ingest never reached model")
	}
	if err := s.consoleStore.Logout(admin.Value); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(100 * time.Millisecond):
		t.Error("session revocation did not cancel real ingest request")
	}
	close(release)
	response := <-finished
	response.want(t, 401)
	if _, err := os.Stat(filepath.Join(s.cfg.Vaults.Personal, "concepts", "should-never-write.md")); !os.IsNotExist(err) {
		t.Errorf("revoked ingest wrote private page: %v", err)
	}
	if strings.Contains(response.body, "REVOKED_PRIVATE") {
		t.Error("revoked ingest published result")
	}
}

// A source invalidated during generation cannot be silently removed while its
// now-unreadable private body survives in the generated answer.
func TestConsoleKnowledgePrivateAnswerFailsClosedOnInvalidSource(t *testing.T) {
	s := knowledgeFixture(t)
	knowledgeWrite(t, s.cfg.Vaults.Personal, "orion.md", "---\ntitle: Orion\n---\nOrion PRIVATE_FACT_1029")
	knowledgeChains(t, s, knowledgeInference{func(context.Context, json.RawMessage) (json.RawMessage, error) {
		knowledgeWrite(t, s.cfg.Vaults.Personal, "orion.md", "---\ntitle: [invalid]\n---\nMALFORMED")
		return json.RawMessage(`{"choices":[{"message":{"content":"PRIVATE_FACT_1029"}}]}`), nil
	}}, zap.NewNop())
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	response := knowledgePost(s, "/admin/knowledge/query", `{"query":"Orion"}`, admin, csrf)
	response.want(t, 503)
	if strings.Contains(response.body, "PRIVATE_FACT_1029") {
		t.Fatal("unreadable source answer published")
	}
}
