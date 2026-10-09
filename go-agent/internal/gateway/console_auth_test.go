package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/core"
)

const testConsoleOrigin = "https://family.example:8443"
const testConsolePassword = "correct-password-123"

func consoleTestConfigPath(t *testing.T, origin string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"personal", "agent", "accounts"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("server:\n  internal_key: test-key\ninference:\n  endpoint: http://127.0.0.1:1\nvaults:\n  personal: %q\n  agent: %q\nconsole:\n  enabled: true\n  data_dir: %q\n  public_origin: %q\n  allow_insecure_http: true\n", filepath.Join(dir, "personal"), filepath.Join(dir, "agent"), filepath.Join(dir, "accounts"), origin)
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func consoleTestServer(t *testing.T, origin string) *Server {
	t.Helper()
	app, err := core.Bootstrap(consoleTestConfigPath(t, origin))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Logger.Sync() })
	return NewServerFromApp(app)
}

func consoleRequest(s *Server, method, path, body string, cookie *http.Cookie, csrf, origin, bearer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.10:4321"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.engine.ServeHTTP(w, r)
	return w
}

func consoleJSON(t *testing.T, w *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
	}
	v := map[string]any{}
	if w.Code != 204 {
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
	}
	return v
}

func consoleBootstrap(t *testing.T, s *Server) string {
	t.Helper()
	v := consoleJSON(t, consoleRequest(s, "POST", "/internal/console/bootstrap", `{"username":"Owner","display_name":"家长","password":"`+testConsolePassword+`"}`, nil, "", "", "test-key"), 201)
	return v["user"].(map[string]any)["id"].(string)
}

func consoleLogin(t *testing.T, s *Server, username, password, origin string) (*http.Cookie, string, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	w := consoleRequest(s, "POST", "/api/console/v1/auth/login", string(body), nil, "", origin, "")
	v := consoleJSON(t, w, 200)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%v", cookies)
	}
	if _, ok := v["token"]; ok || strings.Contains(w.Body.String(), cookies[0].Value) {
		t.Fatal("login token leaked in JSON")
	}
	return cookies[0], v["csrf_token"].(string), v
}

func TestConsoleAuthHTTP(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	status := consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/auth/status", "", nil, "", "", ""), 200)
	if len(status) != 1 || status["initialized"] != false {
		t.Fatalf("status=%v", status)
	}
	adminID := consoleBootstrap(t, s)
	admin, csrf, identity := consoleLogin(t, s, "OWNER", testConsolePassword, testConsoleOrigin)
	user := identity["user"].(map[string]any)
	if len(user) != 5 || user["username"] != "owner" || user["role"] != "admin" {
		t.Fatalf("identity=%v", identity)
	}
	routes := []struct{ method, path, body string }{
		{"GET", "/api/console/v1/auth/me", ""}, {"POST", "/api/console/v1/auth/logout", ""}, {"POST", "/api/console/v1/auth/password", `{"old_password":"x","new_password":"long-password-123"}`},
		{"GET", "/api/console/v1/admin/members", ""}, {"POST", "/api/console/v1/admin/members", `{"username":"kid","display_name":"孩子"}`}, {"PATCH", "/api/console/v1/admin/members/unknown", `{"disabled":true}`}, {"POST", "/api/console/v1/admin/members/unknown/reset-password", ""},
	}
	for _, tc := range routes {
		t.Run("anonymous"+tc.path, func(t *testing.T) {
			v := consoleJSON(t, consoleRequest(s, tc.method, tc.path, tc.body, nil, "", testConsoleOrigin, ""), 401)
			e, ok := v["error"].(map[string]any)
			if !ok || e["request_id"] == "" {
				t.Fatalf("unsafe error=%v", v)
			}
		})
	}
	v := consoleJSON(t, consoleRequest(s, "POST", "/api/console/v1/admin/members", `{"username":"Kid","display_name":"孩子"}`, admin, csrf, testConsoleOrigin, ""), 201)
	memberID := v["user"].(map[string]any)["id"].(string)
	temp := v["temporary_password"].(string)
	member, memberCSRF, memberIdentity := consoleLogin(t, s, "kid", temp, testConsoleOrigin)
	if memberIdentity["user"].(map[string]any)["must_change_password"] != true {
		t.Fatal("temporary login is unrestricted")
	}
	consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/auth/me", "", member, "", "", ""), 200)
	for _, path := range []string{"/api/console/v1/sessions", "/api/console/v1/home", "/api/console/v1/chat/ws"} {
		consoleJSON(t, consoleRequest(s, "GET", path, "", member, "", testConsoleOrigin, ""), 403)
	}
	consoleJSON(t, consoleRequest(s, "POST", "/api/console/v1/auth/password", `{"old_password":"`+temp+`","new_password":"member-password-123"}`, member, memberCSRF, testConsoleOrigin, ""), 204)
	consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/auth/me", "", member, "", "", ""), 401)
	member, memberCSRF, _ = consoleLogin(t, s, "kid", "member-password-123", testConsoleOrigin)
	for _, tc := range routes[3:] {
		t.Run("member"+tc.path, func(t *testing.T) {
			consoleJSON(t, consoleRequest(s, tc.method, tc.path, tc.body, member, memberCSRF, testConsoleOrigin, ""), 403)
		})
	}
	list := consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/admin/members", "", admin, "", "", ""), 200)
	if strings.Contains(fmt.Sprint(list), temp) || len(list["members"].([]any)) != 1 {
		t.Fatalf("list=%v", list)
	}
	consoleJSON(t, consoleRequest(s, "PATCH", "/api/console/v1/admin/members/"+memberID, `{"disabled":true}`, admin, csrf, testConsoleOrigin, ""), 200)
	consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/auth/me", "", member, "", "", ""), 401)
	changed := consoleJSON(t, consoleRequest(s, "PATCH", "/api/console/v1/admin/members/"+memberID, `{"display_name":"新名字"}`, admin, csrf, testConsoleOrigin, ""), 200)["user"].(map[string]any)
	if changed["disabled"] != true {
		t.Fatal("PATCH re-enabled omitted disabled field")
	}
	consoleJSON(t, consoleRequest(s, "PATCH", "/api/console/v1/admin/members/"+memberID, `{"disabled":false}`, admin, csrf, testConsoleOrigin, ""), 200)
	consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/auth/me", "", member, "", "", ""), 401)
	consoleJSON(t, consoleRequest(s, "PATCH", "/api/console/v1/admin/members/"+adminID, `{"disabled":true}`, admin, csrf, testConsoleOrigin, ""), 403)
	consoleJSON(t, consoleRequest(s, "POST", "/api/console/v1/admin/members/"+adminID+"/reset-password", "", admin, csrf, testConsoleOrigin, ""), 403)
	reset := consoleJSON(t, consoleRequest(s, "POST", "/api/console/v1/admin/members/"+memberID+"/reset-password", "", admin, csrf, testConsoleOrigin, ""), 200)
	if reset["temporary_password"] == temp {
		t.Fatal("temporary password reused")
	}
	for _, tc := range []struct{ method, path, body string }{{"POST", "/api/console/v1/admin/members", `{"username":"new","display_name":"N","role":"admin"}`}, {"PATCH", "/api/console/v1/admin/members/" + memberID, `{"username":"renamed"}`}, {"PATCH", "/api/console/v1/admin/members/" + memberID, `{}`}, {"POST", "/api/console/v1/admin/members", `null`}} {
		consoleJSON(t, consoleRequest(s, tc.method, tc.path, tc.body, admin, csrf, testConsoleOrigin, ""), 422)
	}
	consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/auth/me", "", nil, "", "", "test-key"), 401)
	consoleJSON(t, consoleRequest(s, "GET", "/internal/vault/status", "", admin, "", "", ""), 401)
	consoleJSON(t, consoleRequest(s, "POST", "/api/console/v1/auth/logout", "", admin, csrf, testConsoleOrigin, ""), 204)
	consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/auth/me", "", admin, "", "", ""), 401)
}

func TestConsoleOriginAndCSRF(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	for _, origin := range []string{"", "http://family.example:8443", "https://other.example:8443", "https://family.example", "https://family.example:8443/", "null"} {
		consoleJSON(t, consoleRequest(s, "POST", "/api/console/v1/auth/login", `{"username":"owner","password":"`+testConsolePassword+`"}`, nil, "", origin, ""), 403)
	}
	cookie, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" || cookie.Path != "/api/console/v1" || cookie.MaxAge != 604800 {
		t.Fatalf("cookie=%+v", cookie)
	}
	for _, tc := range []struct{ origin, csrf string }{{"", csrf}, {testConsoleOrigin, ""}, {testConsoleOrigin, "wrong"}, {"http://family.example:8443", csrf}} {
		consoleJSON(t, consoleRequest(s, "POST", "/api/console/v1/auth/logout", "", cookie, tc.csrf, tc.origin, ""), 403)
	}
	for _, origin := range []string{testConsoleOrigin, "http://localhost:8080"} {
		h := consoleTestServer(t, origin)
		consoleBootstrap(t, h)
		r := httptest.NewRequest("POST", "/api/console/v1/auth/login", strings.NewReader(`{"username":"owner","password":"`+testConsolePassword+`"}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		h.engine.ServeHTTP(w, r)
		consoleJSON(t, w, 200)
		if w.Result().Cookies()[0].Secure != strings.HasPrefix(origin, "https:") {
			t.Fatal("forwarded header changed cookie protection")
		}
	}
}

func TestConsoleBootstrapRequiresManagementKey(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	for _, key := range []string{"", "wrong"} {
		consoleJSON(t, consoleRequest(s, "POST", "/internal/console/bootstrap", `{"username":"owner","display_name":"Owner","password":"`+testConsolePassword+`"}`, nil, "", "", key), 401)
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- consoleRequest(s, "POST", "/internal/console/bootstrap", `{"username":"owner","display_name":"Owner","password":"`+testConsolePassword+`"}`, nil, "", "", "test-key").Code
		}()
	}
	wg.Wait()
	close(statuses)
	created := 0
	for status := range statuses {
		if status == 201 {
			created++
		} else if status != 409 {
			t.Fatalf("bootstrap=%d", status)
		}
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
	cookie, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	consoleJSON(t, consoleRequest(s, "POST", "/internal/console/reset-admin", `{"password":"replacement-password"}`, cookie, csrf, testConsoleOrigin, ""), 401)
	consoleJSON(t, consoleRequest(s, "POST", "/internal/console/reset-admin", `{"password":"replacement-password"}`, nil, "", "", "test-key"), 204)
	consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/auth/me", "", cookie, "", "", ""), 401)
	consoleLogin(t, s, "owner", "replacement-password", testConsoleOrigin)
}

func TestConsoleLoginRateLimit(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	attempt := func(name, peer, forwarded string) int {
		body, _ := json.Marshal(map[string]string{"username": name, "password": "wrong-password-123"})
		r := httptest.NewRequest("POST", "/api/console/v1/auth/login", strings.NewReader(string(body)))
		r.RemoteAddr = peer
		r.Header.Set("Origin", testConsoleOrigin)
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		s.engine.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 10; i++ {
		name := "missing"
		if i%2 == 1 {
			name = "MISSING"
		}
		if code := attempt(name, "192.0.2.20:42", fmt.Sprintf("198.51.100.%d", i)); code != 401 {
			t.Fatalf("attempt %d=%d", i, code)
		}
	}
	if code := attempt("missing", "192.0.2.20:999", "203.0.113.8"); code != 429 {
		t.Fatalf("username limit=%d", code)
	}
	for i := 0; i < 60; i++ {
		if code := attempt(fmt.Sprintf("unknown%02d", i), "192.0.2.21:1", ""); code != 401 {
			t.Fatalf("IP attempt %d=%d", i, code)
		}
	}
	if code := attempt("another", "192.0.2.21:2", ""); code != 429 {
		t.Fatalf("IP limit=%d", code)
	}
	if code := attempt("missing", "192.0.2.22:1", ""); code != 401 {
		t.Fatalf("independent peer=%d", code)
	}
}

func TestConsoleDisabledUnavailableAndBoundary(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	missing := NewServerFromApp(&core.App{Config: s.cfg, Logger: s.logger})
	for _, path := range []string{"/api/console/v1/auth/status", "/api/console/v1/auth/me"} {
		consoleJSON(t, consoleRequest(missing, "GET", path, "", nil, "", "", ""), 503)
	}
	consoleJSON(t, consoleRequest(missing, "POST", "/internal/console/bootstrap", `{}`, nil, "", "", "test-key"), 503)
	cfg := *s.cfg
	cfg.Console.Enabled = false
	disabled := NewServerFromApp(&core.App{Config: &cfg, Logger: s.logger})
	consoleJSON(t, consoleRequest(disabled, "GET", "/api/console/v1/auth/status", "", nil, "", "", ""), 503)
	consoleJSON(t, consoleRequest(s, "GET", "/api/console/v10/auth/status", "", nil, "", "", ""), 401)
	browser := consoleRequest(s, "POST", "/auth/browser", "", nil, "", "", "").Result().Cookies()[0]
	consoleJSON(t, consoleRequest(s, "GET", "/api/console/v1/auth/me", "", browser, "", "", ""), 401)
}

func TestConsoleRateLimitRollingWindowAndBoundedBuckets(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	limiter := newConsoleLoginLimiter(func() time.Time { return now })
	if !limiter.allow("192.0.2.1", "owner") {
		t.Fatal("first denied")
	}
	now = now.Add(4 * time.Minute)
	for i := 0; i < 9; i++ {
		if !limiter.allow("192.0.2.1", "owner") {
			t.Fatal("early deny")
		}
	}
	now = now.Add(time.Minute + time.Second)
	if !limiter.allow("192.0.2.1", "owner") {
		t.Fatal("expired attempt still counted")
	}
	if limiter.allow("192.0.2.1", "owner") {
		t.Fatal("accepted more than 10 attempts in the last five minutes")
	}
	bounded := newConsoleLoginLimiter(func() time.Time { return now })
	for i := 0; i < 4096; i++ {
		if !bounded.allow(fmt.Sprint(i), "user") {
			t.Fatalf("early bucket cap %d", i)
		}
	}
	if bounded.allow("overflow", "user") {
		t.Fatal("unbounded peer buckets")
	}
	now = now.Add(5 * time.Minute)
	if !bounded.allow("overflow", "user") {
		t.Fatal("expired buckets not reclaimed")
	}
}
