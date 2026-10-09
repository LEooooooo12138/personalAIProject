package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRunDefaultsToOfflinePreviewAndSafeReport(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	output := filepath.Join(dir, "report.json")
	os.WriteFile(input, []byte(`{"areas":[],"devices":[{"id":"d","name":"负一楼中控"}],"entities":[],"labels":[]}`), 0600)
	var out bytes.Buffer
	code := run(context.Background(), []string{"--inventory", input, "--output", output}, func(string) string { return "fixture-secret" }, &out)
	if code != 0 {
		t.Fatal(code, out.String())
	}
	data, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	var report map[string]any
	if e = json.Unmarshal(data, &report); e != nil {
		t.Fatal(e)
	}
	if report["mode"] != "preview" || report["plan"] == nil || strings.Contains(string(data), "fixture-secret") {
		t.Fatal(string(data))
	}
	if !strings.Contains(string(data), "地下室") {
		t.Fatal("missing approved target")
	}
}
func TestRunRejectsOfflineApplyAndCredentialArguments(t *testing.T) {
	for _, args := range [][]string{{"--apply", "--inventory", "unused"}, {"--token", "fixture-secret"}, {"--endpoint", "http://localhost:8123"}} {
		var out bytes.Buffer
		if run(context.Background(), args, func(string) string { return "" }, &out) == 0 {
			t.Fatal("unsafe arguments accepted", args)
		}
		if strings.Contains(out.String(), "fixture-secret") {
			t.Fatal("argument leaked")
		}
	}
}
func TestRunRejectsMissingOrCredentialBearingEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", "http://user:password@localhost:8123", "http://localhost:8123/?token=fixture-secret"} {
		var out bytes.Buffer
		env := func(k string) string {
			if k == "HA_BASE_URL" {
				return endpoint
			}
			return "fixture-secret"
		}
		if run(context.Background(), nil, env, &out) == 0 {
			t.Fatal("invalid endpoint accepted")
		}
		if strings.Contains(out.String(), "fixture-secret") || strings.Contains(out.String(), "password") {
			t.Fatal("config leaked")
		}
	}
}

// Default live preview must perform only registry reads; explicit apply uses only
// the allowlisted registry area-field update and verifies it by rereading.
func TestRunLivePreviewAndExplicitMetadataApply(t *testing.T) {
	var mu sync.Mutex
	var area *string
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			t.Error("non-registry endpoint requested")
			http.NotFound(w, r)
			return
		}
		conn, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		conn.WriteJSON(map[string]any{"type": "auth_required"})
		var auth map[string]any
		if conn.ReadJSON(&auth) != nil {
			return
		}
		if auth["access_token"] != "fixture-secret" {
			t.Error("wrong environment credential")
		}
		conn.WriteJSON(map[string]any{"type": "auth_ok"})
		for {
			var req map[string]any
			if conn.ReadJSON(&req) != nil {
				return
			}
			mu.Lock()
			var result any
			switch req["type"] {
			case "config/area_registry/list":
				result = []any{map[string]any{"area_id": "other", "name": "其他"}}
			case "config/device_registry/list":
				result = []any{map[string]any{"id": "one", "name": "电视", "area_id": area}}
			case "config/entity_registry/list", "config/label_registry/list":
				result = []any{}
			case "config/device_registry/update":
				if len(req) != 4 || req["device_id"] != "one" || req["area_id"] != "other" {
					t.Error("unsafe metadata payload")
				}
				a := "other"
				area = &a
				writes++
				result = map[string]any{"id": "one", "area_id": "other"}
			default:
				t.Error("unexpected command", req["type"])
			}
			mu.Unlock()
			if conn.WriteJSON(map[string]any{"id": req["id"], "type": "result", "success": true, "result": result}) != nil {
				return
			}
		}
	}))
	defer server.Close()
	env := func(k string) string {
		if k == "HA_BASE_URL" {
			return server.URL
		}
		return "fixture-secret"
	}
	var out bytes.Buffer
	if run(context.Background(), nil, env, &out) != 0 {
		t.Fatal(out.String())
	}
	mu.Lock()
	count := writes
	mu.Unlock()
	if count != 0 {
		t.Fatal("preview wrote metadata")
	}
	out.Reset()
	if run(context.Background(), []string{"--apply"}, env, &out) != 0 {
		t.Fatal(out.String())
	}
	mu.Lock()
	count = writes
	mu.Unlock()
	if count != 1 {
		t.Fatal("apply did not write exactly one assignment")
	}
	var report struct {
		Mode   string
		Result struct{ Success int }
	}
	if e := json.Unmarshal(out.Bytes(), &report); e != nil || report.Mode != "apply" || report.Result.Success != 1 {
		t.Fatal(report, e)
	}
	out.Reset()
	if run(context.Background(), []string{"--apply"}, env, &out) != 0 {
		t.Fatal(out.String())
	}
	mu.Lock()
	count = writes
	mu.Unlock()
	if count != 1 {
		t.Fatal("replay wrote again")
	}
	if strings.Contains(out.String(), "fixture-secret") || strings.Contains(out.String(), server.URL) {
		t.Fatal("report leaked configuration")
	}
}

func TestRunFilteredInventoryPreservesExistingAreaIDsAndAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.json")
	os.WriteFile(path, []byte(`{"areas":[{"id":"b1","name":"地下室B1","aliases":["地下室"]}],"devices":[{"id":"stairs","name":"负一楼楼梯"},{"id":"existing","name":"感应","area_id":"b1"}],"entities":[],"labels":[]}`), 0600)
	var out bytes.Buffer
	if run(context.Background(), []string{"--inventory", path}, func(string) string { return "" }, &out) != 0 {
		t.Fatal(out.String())
	}
	var report struct {
		Plan struct {
			Items []struct {
				ID, TargetName string
				TargetAreaID   *string `json:"target_area_id"`
			}
		}
	}
	if e := json.Unmarshal(out.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	for _, item := range report.Plan.Items {
		if item.TargetAreaID == nil || *item.TargetAreaID != "b1" {
			t.Fatal("filtered area ID lost", item)
		}
	}
}
