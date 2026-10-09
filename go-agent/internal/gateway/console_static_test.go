package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func staticTestRouter(t *testing.T, built bool) (*gin.Engine, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "console")
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if built {
		for name, content := range map[string]string{
			"index.html":              "<!doctype html><title>fixture console</title>",
			"assets/app-a1b2c3d4.js":  "const fixture = true;",
			"assets/app-a1b2c3d4.css": "body { color: teal; }",
			"favicon.svg":             `<svg xmlns="http://www.w3.org/2000/svg"/>`,
			"assets/private.ts":       "source must not be served",
			"assets/private.js":       "unhashed source must not be served",
		} {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	r := gin.New()
	r.Use(newAccessControl("test-key").middleware())
	registerConsoleStaticRoutes(r, root, testConsoleOrigin)
	return r, root
}

func staticRequest(r http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestConsoleStaticKnownPagesAndAssets(t *testing.T) {
	r, _ := staticTestRouter(t, true)
	redirect := staticRequest(r, "GET", "/app")
	if redirect.Code != 308 || redirect.Header().Get("Location") != "/app/" {
		t.Fatalf("/app redirect: %d %q", redirect.Code, redirect.Header().Get("Location"))
	}
	for _, path := range []string{"/app/", "/app/chat", "/app/chat?sid=abc", "/app/account", "/app/members"} {
		w := staticRequest(r, "GET", path)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture console") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: status=%d headers=%v body=%q", path, w.Code, w.Header(), w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self' wss://family.example:8443") || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s missing security headers: %v", path, w.Header())
		}
	}
	for _, tc := range []struct{ path, mime, content string }{
		{"/app/assets/app-a1b2c3d4.js", "text/javascript", "const fixture"},
		{"/app/assets/app-a1b2c3d4.css", "text/css", "color: teal"},
		{"/app/favicon.svg", "image/svg+xml", "<svg"},
	} {
		w := staticRequest(r, "GET", tc.path)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.mime) || !strings.Contains(w.Body.String(), tc.content) {
			t.Fatalf("%s: status=%d mime=%q body=%q", tc.path, w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
	}
	if got := staticRequest(r, "GET", "/app/assets/app-a1b2c3d4.js").Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("hashed asset cache=%q", got)
	}
	if w := staticRequest(r, "HEAD", "/app/chat"); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD page: %d %q", w.Code, w.Body.String())
	}
}

func TestConsoleStaticRejectsMissingAndUnsafePaths(t *testing.T) {
	r, root := staticTestRouter(t, true)
	outside := filepath.Join(t.TempDir(), "secret.js")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "assets", "leak-a1b2c3d4.js")); err != nil {
		t.Logf("symlink unavailable: %v", err)
	} else if w := staticRequest(r, "GET", "/app/assets/leak-a1b2c3d4.js"); w.Code == 200 || strings.Contains(w.Body.String(), "outside-secret") {
		t.Fatalf("symlink escaped: %d %q", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/app/not-a-page", 404},
		{"GET", "/app/assets/missing.js", 404},
		{"GET", "/app/assets/", 404},
		{"GET", "/app/assets/private.ts", 404},
		{"GET", "/app/assets/private.js", 404},
		{"GET", "/app/index.html", 404},
		{"POST", "/app/chat", 405},
		{"GET", "/app/%2e%2e/assets/app-a1b2c3d4.js", 404},
	} {
		w := staticRequest(r, tc.method, tc.path)
		if w.Code != tc.want || strings.Contains(w.Body.String(), "fixture console") {
			t.Fatalf("%s %s: status=%d body=%q", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestConsoleStaticSymlinkStaysWithinRoot(t *testing.T) {
	r, root := staticTestRouter(t, true)
	if err := os.Symlink("app-a1b2c3d4.js", filepath.Join(root, "assets", "alias-a1b2c3d4.js")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	w := staticRequest(r, "GET", "/app/assets/alias-a1b2c3d4.js")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "const fixture") {
		t.Fatalf("contained symlink: %d %q", w.Code, w.Body.String())
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "outside-a1b2c3d4.js"), []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(root, "assets", "escape")); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	w = staticRequest(r, "GET", "/app/assets/escape/outside-a1b2c3d4.js")
	if w.Code == 200 || strings.Contains(w.Body.String(), "outside-secret") {
		t.Fatalf("directory symlink escaped: %d %q", w.Code, w.Body.String())
	}
}

func TestConsoleStaticMissingBuildAndAPIIsolation(t *testing.T) {
	r, _ := staticTestRouter(t, false)
	for _, path := range []string{"/app/", "/app/chat"} {
		w := staticRequest(r, "GET", path)
		if w.Code != 503 || strings.Contains(w.Body.String(), "console\\") || strings.Contains(w.Body.String(), "console/") {
			t.Fatalf("%s: status=%d body=%q", path, w.Code, w.Body.String())
		}
	}
	s := consoleTestServer(t, testConsoleOrigin)
	for _, path := range []string{"/api/console/v1/auth/me", "/internal/vault/status"} {
		w := consoleRequest(s, "GET", path, "", nil, "", "", "")
		if w.Code != 401 {
			t.Fatalf("%s unauthenticated status=%d body=%q", path, w.Code, w.Body.String())
		}
	}
	s.cfg.Console.Enabled = false
	w := consoleRequest(s, "GET", "/api/console/v1/auth/status", "", nil, "", "", "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "not_enabled") {
		t.Fatalf("disabled API status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestConsoleStaticAuthCoordinator(t *testing.T) {
	r, root := staticTestRouter(t, true)
	const workerPath = "assets/console-auth-worker.js"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(workerPath)), []byte("onconnect = () => {};"), 0600); err != nil {
		t.Fatal(err)
	}
	w := staticRequest(r, "GET", "/app/"+workerPath)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("coordinator: status=%d headers=%v", w.Code, w.Header())
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "worker-src 'self'") {
		t.Fatalf("coordinator is blocked by CSP: %q", w.Header().Get("Content-Security-Policy"))
	}
	for _, path := range []string{"/app/console-auth-worker.js", "/app/assets/other-worker.js"} {
		if got := staticRequest(r, "GET", path); got.Code != http.StatusNotFound {
			t.Fatalf("unexpected unhashed script exposed: %s = %d", path, got.Code)
		}
	}
}

func TestConsoleStaticFamilyNestedPagesAndInvalidIDs(t *testing.T) {
	r, _ := staticTestRouter(t, true)
	for _, path := range []string{"/app/areas", "/app/areas/a_QQ", "/app/areas/u_other", "/app/areas/a_QQ/devices/d_ZGV2", "/app/areas/u_other/devices/e_c2Vuc29yLm91dA"} {
		w := staticRequest(r, "GET", path)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "fixture console") || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("legal family navigation %s rejected: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/app/areas/unknown", "/app/areas/a_", "/app/areas/a_QQ=", "/app/areas/e_ZGV2", "/app/areas/a__w", "/app/areas/a_QQ/devices/unknown", "/app/areas/a_QQ/devices/d_", "/app/areas/a_QQ/devices/d_ZGV2/more", "/app/areas/a_QQ/devices", "/app/areas/%2e%2e", "/app/areas/a_QQ%2fdevices%2fd_ZGV2", "/app/areas/%252e%252e", "/app/areas/a_QQ/", "/app/areas/a_QQ/missing.js"} {
		w := staticRequest(r, "GET", path)
		if w.Code != 404 || strings.Contains(w.Body.String(), "fixture console") {
			t.Errorf("invalid nested family path %s=%d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestConsoleStaticCompletionDeepLinks(t *testing.T) {
	r, _ := staticTestRouter(t, true)
	for _, path := range []string{"/app/knowledge", "/app/knowledge?path=concepts%2Fguide.md", "/app/admin/knowledge", "/app/admin/knowledge?path=concepts%2Fprivate.md", "/app/admin/collection", "/app/automations?id=fixture_time"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := staticRequest(r, method, path)
			if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("deep link %s %s: %d", method, path, w.Code)
			}
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Errorf("HEAD emitted body for %s", path)
			}
		}
	}
	for _, path := range []string{"/app/admin", "/app/admin/secrets", "/app/knowledge/unknown", "/app/automations/unknown", "/app/admin/knowledge/more"} {
		if w := staticRequest(r, "GET", path); w.Code != http.StatusNotFound {
			t.Errorf("unexpected fallback %s: %d", path, w.Code)
		}
	}
}
