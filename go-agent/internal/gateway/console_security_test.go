package gateway

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/console"
)

func TestConsoleStrictJSONAndSafeErrors(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	for _, body := range []string{`{"username":"new","display_name":"N","password":"unexpected"}`, `[]`, `null`, `{"username":"new","display_name":"` + strings.Repeat("A", 4200) + `"}`, "{\"username\":\"new\",\"display_name\":\"\xff\"}"} {
		w := consoleRequest(s, "POST", consolePrefix+"/admin/members", body, cookie, csrf, testConsoleOrigin, "")
		consoleJSON(t, w, 422)
	}
	r := httptest.NewRequest("GET", consolePrefix+"/auth/me", nil)
	r.Header.Set("X-Request-ID", strings.Repeat("secret-untrusted-", 500))
	w := httptest.NewRecorder()
	s.engine.ServeHTTP(w, r)
	v := consoleJSON(t, w, 401)
	id := v["error"].(map[string]any)["request_id"].(string)
	if len(id) != 32 || id != w.Header().Get("X-Request-ID") || strings.Contains(w.Body.String(), "secret-untrusted") {
		t.Fatal("untrusted request ID reflected")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("identity error is cacheable")
	}
	for _, path := range []string{consolePrefix + "/admin/members/missing", consolePrefix + "/admin/members/missing/reset-password"} {
		method := "PATCH"
		body := `{"disabled":true}`
		if strings.HasSuffix(path, "reset-password") {
			method = "POST"
			body = ""
		}
		consoleJSON(t, consoleRequest(s, method, path, body, cookie, csrf, testConsoleOrigin, ""), 404)
	}
}

func TestConsoleFailedWriteDoesNotReturnCredentialOrSuccess(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	// A real storage failure: the Store directory is replaced with an ordinary
	// file. No fixture mock grants permissions or manufactures auth responses.
	dir := s.cfg.Console.DataDir
	moved := dir + "-saved"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(dir); _ = os.Rename(moved, dir) })
	for _, tc := range []struct{ path, body string }{{consolePrefix + "/admin/members", `{"username":"new","display_name":"N"}`}, {consolePrefix + "/auth/password", `{"old_password":"` + testConsolePassword + `","new_password":"replacement-password"}`}, {consolePrefix + "/auth/logout", ""}} {
		w := consoleRequest(s, "POST", tc.path, tc.body, cookie, csrf, testConsoleOrigin, "")
		consoleJSON(t, w, 503)
		if strings.Contains(w.Body.String(), "temporary_password") || strings.Contains(w.Body.String(), dir) || len(w.Result().Cookies()) != 0 {
			t.Fatal("failed write disclosed a credential, path, or cleared cookie")
		}
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", cookie, "", "", ""), 200)
}

func TestConsoleLoginExpiryAndAccountValidation(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	store, err := console.OpenStore(t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	s.consoleStore = store
	consoleBootstrap(t, s)
	cookie, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	for _, tc := range []struct{ name, display string }{{"ab", "N"}, {strings.Repeat("a", 33), "N"}, {"new.name", "N"}, {"Kid", "N"}, {"valid", ""}, {"valid", strings.Repeat("家", 41)}} {
		body, _ := json.Marshal(map[string]string{"username": tc.name, "display_name": tc.display})
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/members", string(body), cookie, csrf, testConsoleOrigin, ""), 422)
	}
	for _, password := range []string{"short", strings.Repeat("a", 129)} {
		body, _ := json.Marshal(map[string]string{"old_password": testConsolePassword, "new_password": password})
		consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/auth/password", string(body), cookie, csrf, testConsoleOrigin, ""), 422)
	}
	now = now.Add(7 * 24 * time.Hour)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", cookie, "", "", ""), 401)
}

func TestConsoleRateLimitUserBucketsBounded(t *testing.T) {
	now := time.Now()
	l := newConsoleLoginLimiter(func() time.Time { return now })
	// Preload active accepted attempts to exercise saturation without 16k
	// expensive HTTP authentications; admission/expiry is the production limiter.
	for i := 0; i < 16384; i++ {
		l.users[fmt.Sprint(i)] = consoleAttemptBucket{now}
	}
	if l.allow("192.0.2.1", "new") {
		t.Fatal("unbounded username buckets")
	}
	now = now.Add(5 * time.Minute)
	if !l.allow("192.0.2.1", "new") {
		t.Fatal("user buckets not reclaimed")
	}
}

func TestConsoleTemporaryPasswordLogoutAndResetRevocation(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	v := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/members", `{"username":"kid","display_name":"孩子"}`, admin, csrf, testConsoleOrigin, ""), 201)
	id := v["user"].(map[string]any)["id"].(string)
	temp := v["temporary_password"].(string)
	first, firstCSRF, _ := consoleLogin(t, s, "kid", temp, testConsoleOrigin)
	second, _, _ := consoleLogin(t, s, "kid", temp, testConsoleOrigin)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/auth/logout", "", first, firstCSRF, testConsoleOrigin, ""), 204)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", first, "", "", ""), 401)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", second, "", "", ""), 200)
	reset := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/members/"+id+"/reset-password", "", admin, csrf, testConsoleOrigin, ""), 200)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", second, "", "", ""), 401)
	consoleLogin(t, s, "kid", reset["temporary_password"].(string), testConsoleOrigin)
	// Restart opens the actual persisted state, including token revocations.
	reopened, err := console.OpenStore(filepath.Clean(s.cfg.Console.DataDir), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	s.consoleStore = reopened
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", second, "", "", ""), 401)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", admin, "", "", ""), 200)
}
