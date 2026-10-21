package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPreviewAssetsAreIsolatedAndMarked(t *testing.T) {
	source, target := t.TempDir(), filepath.Join(t.TempDir(), "preview", "static", "console")
	original := "<!doctype html><html><head><title>Console</title></head><body></body></html>"
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "assets", "main-12345678.js"), []byte("console.log('test')"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := preparePreviewAssets(source, target); err != nil {
		t.Fatal(err)
	}
	marked, err := os.ReadFile(filepath.Join(target, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(marked), `<meta name="console-preview" content="mock">`) != 1 {
		t.Fatalf("missing marker: %s", marked)
	}
	unchanged, err := os.ReadFile(filepath.Join(source, "index.html"))
	if err != nil || string(unchanged) != original {
		t.Fatal("source assets modified")
	}
	asset, err := os.ReadFile(filepath.Join(target, "assets", "main-12345678.js"))
	if err != nil || string(asset) != "console.log('test')" {
		t.Fatal("asset not cloned")
	}
}

// The helper runs in its own process: serving the private static copy cannot
// change the global working directory of another test.
func TestPreviewHTTPProcess(t *testing.T) {
	address := os.Getenv("PREVIEW_HTTP_TEST_ADDRESS")
	if address == "" {
		return
	}
	f, err := newFixture(address, "http://"+address)
	if err != nil {
		t.Fatal("cannot create isolated HTTP fixture")
	}
	defer f.Close()
	if os.Getenv("PREVIEW_HTTP_TEST_MARKED") == "1" {
		restore, err := f.enterPreviewDirectory()
		if err != nil {
			t.Fatal(err)
		}
		defer restore()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	if err := f.server.Run(ctx); err != nil {
		t.Fatal("isolated HTTP server failed")
	}
}

func startPreviewHTTPProcess(t *testing.T, marked bool) string {
	t.Helper()
	root := t.TempDir()
	assets := filepath.Join(root, "static", "console")
	if err := os.MkdirAll(filepath.Join(assets, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "index.html"), []byte("<!doctype html><html><head><title>Test</title></head><body></body></html>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "assets", "main-12345678.js"), []byte("/* fixture asset */"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestPreviewHTTPProcess$")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PREVIEW_HTTP_TEST_ADDRESS="+address, fmt.Sprintf("PREVIEW_HTTP_TEST_MARKED=%d", map[bool]int{false: 0, true: 1}[marked]), "CONSOLE_FIXTURE_TEMP_ROOT="+root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		stdin.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error("isolated HTTP fixture exited unsuccessfully")
			}
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("isolated HTTP fixture did not stop")
		}
		original, err := os.ReadFile(filepath.Join(assets, "index.html"))
		if err != nil || strings.Contains(string(original), "console-preview") {
			t.Error("shared build was marked")
		}
	})
	origin := "http://" + address
	for i := 0; i < 200; i++ {
		res, err := http.Get(origin + "/api/console/v1/auth/status")
		if err == nil {
			res.Body.Close()
			return origin
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("isolated HTTP fixture did not become ready")
	return ""
}

func TestPreviewHTTPDocumentAndCacheSemantics(t *testing.T) {
	origin := startPreviewHTTPProcess(t, true)
	for _, path := range []string{"/app/", "/app/automations?id=fixture_time", "/app/areas", "/app/account", "/app/admin/collection"} {
		res, err := http.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || strings.Count(string(body), previewMeta) != 1 {
			t.Fatalf("unmarked document at %s", path)
		}
		if res.Header.Get("Cache-Control") != "no-store" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "script-src 'self'") || strings.Contains(res.Header.Get("Content-Security-Policy"), "unsafe-inline") {
			t.Fatal("document safety headers changed")
		}
		head, err := http.Head(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		headBody, _ := io.ReadAll(head.Body)
		head.Body.Close()
		if head.StatusCode != 200 || len(headBody) != 0 || head.Header.Get("Content-Length") != strconv.Itoa(len(body)) || head.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("invalid HEAD document response")
		}
		req, _ := http.NewRequest(http.MethodGet, origin+path, nil)
		req.Header.Set("If-Modified-Since", res.Header.Get("Last-Modified"))
		cached, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		cachedBody, _ := io.ReadAll(cached.Body)
		cached.Body.Close()
		if cached.StatusCode != 304 || len(cachedBody) != 0 || cached.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("invalid conditional document response")
		}
	}
	asset, err := http.Get(origin + "/app/assets/main-12345678.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(asset.Body)
	asset.Body.Close()
	if string(body) != "/* fixture asset */" || !strings.Contains(asset.Header.Get("Cache-Control"), "immutable") {
		t.Fatal("asset altered")
	}
}

func TestUnmarkedHTTPBuildDoesNotClaimPreview(t *testing.T) {
	origin := startPreviewHTTPProcess(t, false)
	res, err := http.Get(origin + "/app/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || strings.Contains(string(body), "console-preview") {
		t.Fatal("unmarked server claimed preview mode")
	}
}

func TestPreviewAssetsRejectInvalidHTML(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("not a built document"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := preparePreviewAssets(source, filepath.Join(t.TempDir(), "target")); err == nil {
		t.Fatal("accepted HTML without head")
	}
}

func TestPreviewAssetsRejectLinks(t *testing.T) {
	source := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.js")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "linked.js")); err != nil {
		t.Skip("symlink creation unavailable")
	}
	if err := preparePreviewAssets(source, filepath.Join(t.TempDir(), "target")); err == nil {
		t.Fatal("accepted symlink")
	}
}
