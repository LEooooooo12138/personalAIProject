package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/console"
	"github.com/yuanleyao/ai-agent/internal/core"
)

func TestFixtureAddressRejectsNonLoopback(t *testing.T) {
	for _, tc := range []struct {
		host string
		port int
	}{
		{"0.0.0.0", 18081}, {"192.0.2.1", 18081}, {"localhost", 18081},
		{"127.0.0.1", 0}, {"127.0.0.1", 65536},
	} {
		if _, _, err := fixtureAddress(tc.host, tc.port); err == nil {
			t.Fatalf("accepted %q:%d", tc.host, tc.port)
		}
	}
	listen, origin, err := fixtureAddress("127.0.0.1", 18081)
	if err != nil || listen != "127.0.0.1:18081" || origin != "http://127.0.0.1:18081" {
		t.Fatalf("IPv4 listen=%q origin=%q err=%v", listen, origin, err)
	}
	listen, origin, err = fixtureAddress("::1", 18081)
	if err != nil || listen != "[::1]:18081" || origin != "http://[::1]:18081" {
		t.Fatalf("IPv6 listen=%q origin=%q err=%v", listen, origin, err)
	}
}

func TestFixtureUsesTemporaryStoresAndRealAccounts(t *testing.T) {
	listen, origin, err := fixtureAddress("127.0.0.1", 18081)
	if err != nil {
		t.Fatal(err)
	}
	f, err := newFixture(listen, origin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.app.Config.Server.ListenAddress != listen || f.app.Config.Console.PublicOrigin != origin || !f.app.Config.SmartHome.Enabled || f.app.SmartHome == nil || f.app.Config.SmartHome.BaseURL != f.dependencies.ha.URL {
		t.Fatalf("unsafe config: %+v", f.app.Config)
	}
	if f.app.Config.Console.DataDir != filepath.Join(f.dir, "accounts") || f.app.Config.Vaults.Agent != filepath.Join(f.dir, "agent") || f.app.Config.Vaults.Personal != filepath.Join(f.dir, "personal") {
		t.Fatalf("non-temporary paths: %+v", f.app.Config)
	}
	for _, name := range []string{"owner", "alice", "bobby"} {
		login, err := f.app.ConsoleStore.Authenticate(name, fixturePassword)
		if err != nil || login.Principal.MustChangePassword {
			t.Fatalf("%s not ready: %+v %v", name, login.Principal, err)
		}
	}
	login, err := f.app.ConsoleStore.Authenticate("tempuser", f.tempPassword)
	if err != nil || !login.Principal.MustChangePassword {
		t.Fatalf("temporary account not restricted: %+v %v", login.Principal, err)
	}
	if _, err := f.app.ConsoleStore.Authenticate("tempuser", fixturePassword); !errors.Is(err, console.ErrUnauthenticated) {
		t.Fatalf("temporary account accepted shared password: %v", err)
	}
	if f.app.Config.Inference.Endpoint != f.dependencies.tags.URL {
		t.Fatalf("inference endpoint=%q", f.app.Config.Inference.Endpoint)
	}
	// The legacy browser WebSocket indexes user messages. Its store must use
	// the fixture embedder even though core.Bootstrap initially creates Ollama.
	session, err := f.app.SessionStore.BrowserSession("webchat", "legacy-check", "browser-check", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.app.SessionStore.IndexMessage(context.Background(), session, 0, core.Message{Role: "user", Content: "legacy message", Timestamp: time.Now()}); err != nil {
		t.Fatalf("legacy indexing reached a model: %v", err)
	}
	if _, err := f.app.Infer.Embed(context.Background(), json.RawMessage(`{"input":"probe"}`)); err == nil || err.Error() != "offline fixture has no model" {
		t.Fatalf("fixture inference client reached a model: %v", err)
	}
}

func TestFixtureMockHAReadOnlyAndOfflineModelTags(t *testing.T) {
	listen, origin, err := fixtureAddress("127.0.0.1", 18081)
	if err != nil {
		t.Fatal(err)
	}
	f, err := newFixture(listen, origin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.app.SmartHome == nil {
		t.Fatal("fixture does not provide the real HA catalog manager")
	}
	snapshot, err := f.app.SmartHome.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	areas, err := snapshot.ListAreas("")
	if err != nil {
		t.Fatal(err)
	}
	if areas.Totals.Devices != 3 || areas.Totals.Entities != 6 || areas.Totals.StandaloneEntities != 2 || len(areas.Areas) != 5 {
		t.Fatalf("wrong simulated HA directory: %+v", areas)
	}
	living, err := snapshot.ListDevices("a_bGl2aW5n")
	if err != nil || len(living.Devices) != 2 {
		t.Fatalf("living directory: %+v %v", living, err)
	}
	kitchen, err := snapshot.ListDevices("a_a2l0Y2hlbg")
	if err != nil || len(kitchen.Devices) != 2 {
		t.Fatalf("override directory: %+v %v", kitchen, err)
	}
	data, err := json.Marshal(areas)
	if err != nil || strings.Contains(string(data), "fixture-ha-secret") || strings.Contains(string(data), "attributes") {
		t.Fatal("unsafe mock HA projection", err)
	}
	models, err := f.app.Infer.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "gemma4:12b" {
		t.Fatalf("fixture tags %+v %v", models, err)
	}
	if _, err := f.app.Infer.Chat(context.Background(), json.RawMessage(`{"messages":[]}`)); !errors.Is(err, errFixtureOffline) {
		t.Fatalf("generation must stay canned/offline: %v", err)
	}
	if err := f.app.SmartHome.GetClient().CallService(context.Background(), "light", "turn_on", nil); err == nil {
		t.Fatal("the mock HA server accepted a device action")
	}
}
