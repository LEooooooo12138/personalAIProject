package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
)

// The real HA client/Manager talks only to this loopback fixture. Unexpected
// methods and endpoints fail the test: family reads cannot invoke actions.
func areasTestServer(t *testing.T) (*Server, *atomic.Bool, *atomic.Int32) {
	t.Helper()
	fail := &atomic.Bool{}
	reads := &atomic.Int32{}
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != "GET" {
			t.Errorf("family endpoint invoked non-read HA method %s", r.Method)
			http.Error(w, "not read", 405)
			return
		}
		switch r.URL.Path {
		case "/api/states":
			if r.Header.Get("Authorization") != "Bearer HA_TOKEN_SECRET_531" {
				t.Error("missing fixture authorization")
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"entity_id":"switch.kitchen","state":"off","attributes":{"ip":"HA_ATTR_SECRET_531","config_entry_id":"HA_CONFIG_SECRET_531"}}]`)
		case "/api/websocket":
			c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			c.WriteJSON(map[string]string{"type": "auth_required", "ha_version": "2026.7.2"})
			var auth struct{ Type, AccessToken string }
			var raw map[string]string
			if err := c.ReadJSON(&raw); err != nil {
				return
			}
			auth.Type = raw["type"]
			auth.AccessToken = raw["access_token"]
			if auth.Type != "auth" || auth.AccessToken != "HA_TOKEN_SECRET_531" {
				t.Error("unexpected fixture HA auth")
				return
			}
			c.WriteJSON(map[string]string{"type": "auth_ok", "ha_version": "2026.7.2"})
			results := map[string]string{
				"config/area_registry/list":   `[{"area_id":"A","name":"厨房"},{"area_id":"B","name":"书房"}]`,
				"config/device_registry/list": `[{"id":"dev","name":"厨房四位","area_id":"A","labels":["HA_REGISTRY_SECRET_531"]},{"id":"empty","name":"书房控制器","area_id":"B"}]`,
				"config/entity_registry/list": `[{"entity_id":"switch.kitchen","device_id":"dev","area_id":null,"name":"厨房通道1"},{"entity_id":"sensor.out","device_id":null,"area_id":null,"disabled_by":"user","entity_category":"diagnostic"}]`,
				"config/label_registry/list":  `[{"label_id":"HA_REGISTRY_SECRET_531","name":"private label"}]`,
			}
			for i := 0; i < 4; i++ {
				var command struct {
					ID   int    `json:"id"`
					Type string `json:"type"`
				}
				if err := c.ReadJSON(&command); err != nil {
					return
				}
				result, ok := results[command.Type]
				if !ok {
					t.Errorf("unexpected HA command %s", command.Type)
					return
				}
				if fail.Load() {
					c.WriteJSON(map[string]any{"id": command.ID, "type": "result", "success": false, "error": map[string]string{"message": "HA_TOKEN_SECRET_531 http://secret-host/private"}})
					return
				}
				c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"id":%d,"type":"result","success":true,"result":%s}`, command.ID, result)))
			}
		default:
			t.Errorf("unexpected HA path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ha.Close)
	manager, err := smarthome.NewManager(smarthome.HAConfig{BaseURL: ha.URL, Token: "HA_TOKEN_SECRET_531", AgentVaultPath: t.TempDir()}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	s := consoleTestServer(t, testConsoleOrigin)
	s.smartHome = manager
	consoleBootstrap(t, s)
	return s, fail, reads
}

// Missing routes/area permission must fail real authenticated HTTP requests.
func TestFamilyAreasReadForAdminAndMember(t *testing.T) {
	s, _, _ := areasTestServer(t)
	admin, _, identity := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "Member")
	for _, cookie := range []*http.Cookie{admin, member} {
		me := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/auth/me", "", cookie, "", "", ""), 200)
		caps := fmt.Sprint(me["capabilities"])
		if !strings.Contains(caps, "areas:read") {
			t.Errorf("effective family user has no areas:read capability: %v", me)
		}
		for _, path := range []string{"/areas", "/areas/a_QQ/devices", "/areas/a_QQ/devices/d_ZGV2", "/areas/u_other/devices/e_c2Vuc29yLm91dA"} {
			w := consoleRequest(s, "GET", consolePrefix+path, "", cookie, "", "", "")
			body := consoleJSON(t, w, 200)
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("private area response cached: %v", w.Header())
			}
			if strings.Contains(w.Body.String(), "HA_TOKEN_SECRET") || strings.Contains(w.Body.String(), "HA_ATTR_SECRET") || strings.Contains(w.Body.String(), "HA_CONFIG_SECRET") || strings.Contains(w.Body.String(), "HA_REGISTRY_SECRET") || strings.Contains(w.Body.String(), "attributes") {
				t.Errorf("private HA fields leaked: %s", w.Body.String())
			}
			if path == "/areas" {
				totals := body["totals"].(map[string]any)
				if totals["devices"] != float64(2) || totals["entities"] != float64(2) || len(body["areas"].([]any)) != 3 {
					t.Errorf("not the full household catalog: %v", body)
				}
			}
			if strings.HasSuffix(path, "e_c2Vuc29yLm91dA") {
				entity := body["device"].(map[string]any)["entities"].([]any)[0].(map[string]any)
				if entity["state"] != nil || entity["unit"] != nil || entity["last_changed"] != nil || entity["disabled"] != true {
					t.Errorf("nullable/disabled entity fields lost: %v", entity)
				}
			}
		}
	}
	if !strings.Contains(fmt.Sprint(identity["capabilities"]), "areas:read") {
		t.Errorf("login omits area capability: %v", identity)
	}
	filtered := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/areas?q="+url.QueryEscape("厨房"), "", member, "", "", ""), 200)
	if len(filtered["areas"].([]any)) != 1 || filtered["totals"].(map[string]any)["devices"] != float64(2) {
		t.Fatalf("filtered query changes whole-house totals: %v", filtered)
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/admin/members", "", member, "", "", ""), 403)
}

func TestFamilyAreasRejectAnonymousRestrictedDisabledAndUnknown(t *testing.T) {
	s, _, reads := areasTestServer(t)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/areas", "", nil, "", "", ""), 401)
	user, password, err := s.consoleStore.CreateMember("Restricted", "Restricted")
	if err != nil {
		t.Fatal(err)
	}
	restricted, _, _ := consoleLogin(t, s, "Restricted", password, testConsoleOrigin)
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/areas", "", restricted, "", "", ""), 403)
	if reads.Load() != 0 {
		t.Fatal("unauthorized/restricted request touched HA fixture")
	}
	if _, err := s.consoleStore.UpdateMember(user.ID, "Restricted", true); err != nil {
		t.Fatal(err)
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/areas", "", restricted, "", "", ""), 401)
	_, member := consoleMemberLogin(t, s, "Reader")
	for _, path := range []string{"/areas/a_Qg/devices/d_ZGV2", "/areas/a_QQ/devices/d_bWlzc2luZw", "/areas/a_QQ=/devices", "/areas/a_/devices", "/areas/a_QQ/devices/e__w", "/missing"} {
		consoleJSON(t, consoleRequest(s, "GET", consolePrefix+path, "", member, "", "", ""), 404)
	}
	consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/areas?q="+strings.Repeat("x", 129), "", member, "", "", ""), 422)
}

func TestFamilyAreasHAUnavailableReturnsSafe503(t *testing.T) {
	s, fail, _ := areasTestServer(t)
	fail.Store(true)
	_, cookie := consoleMemberLogin(t, s, "Reader")
	w := consoleRequest(s, "GET", consolePrefix+"/areas", "", cookie, "", "", "")
	consoleJSON(t, w, 503)
	if strings.Contains(w.Body.String(), "HA_TOKEN_SECRET") || strings.Contains(w.Body.String(), "secret-host") || strings.Contains(w.Body.String(), "websocket") {
		t.Fatalf("raw upstream failure leaked: %s", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("error response cached")
	}
}

// Test utility adapts the real CatalogService without adding test-only methods
// to either CatalogService or Manager.
type testAreaCatalog struct {
	snapshot smarthome.CatalogSnapshot
	err      error
}

func (c testAreaCatalog) Catalog(context.Context) (smarthome.CatalogSnapshot, error) {
	return c.snapshot, c.err
}

func TestFamilyAreasStaleMetadataMatchesIntegration(t *testing.T) {
	s, _, _ := areasTestServer(t)
	snapshot, err := s.smartHome.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 10, 8, 1, 2, 3, 0, time.UTC)
	attempt := observed.Add(time.Minute)
	code := "ha_unavailable"
	snapshot.Meta = smarthome.CatalogMeta{ObservedAt: &observed, LastSuccessAt: &observed, LastAttemptAt: &attempt, Freshness: "stale", Connection: "unavailable", ErrorCode: &code}
	s.areaCatalog = testAreaCatalog{snapshot: snapshot}
	s.infer = &ragHTTPInference{}
	_, cookie := consoleMemberLogin(t, s, "Reader")
	for _, path := range []string{"/areas", "/areas/a_QQ/devices", "/areas/a_QQ/devices/d_ZGV2", "/integrations/status"} {
		body := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+path, "", cookie, "", "", ""), 200)
		key := "meta"
		if path == "/integrations/status" {
			key = "ha"
		}
		meta := body[key].(map[string]any)
		if meta["freshness"] != "stale" || meta["connection"] != "unavailable" || meta["observed_at"] != "2026-10-08T01:02:03Z" || meta["last_attempt_at"] != "2026-10-08T01:03:03Z" || meta["last_success_at"] != "2026-10-08T01:02:03Z" {
			t.Errorf("%s advances stale observation or loses status: %v", path, meta)
		}
	}
}

type controlledAreaCatalog func(context.Context) (smarthome.CatalogSnapshot, error)

func (fn controlledAreaCatalog) Catalog(ctx context.Context) (smarthome.CatalogSnapshot, error) {
	return fn(ctx)
}

// Revocation during a slow upstream read must prevent final publication even
// though the middleware authenticated the cookie when the request started.
func TestFamilyReadsRecheckSessionAfterUpstreamWait(t *testing.T) {
	for _, path := range []string{"/areas", "/areas/a_QQ/devices", "/areas/a_QQ/devices/d_ZGV2", "/integrations/status"} {
		for _, change := range []string{"disable", "logout", "password"} {
			t.Run(path+"/"+change, func(t *testing.T) {
				s, _, _ := areasTestServer(t)
				snapshot, err := s.smartHome.Catalog(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				user, cookie := consoleMemberLogin(t, s, "Reader")
				s.infer = &statusProbeClient{list: func(context.Context) ([]inference.ModelInfo, error) { return []inference.ModelInfo{}, nil }}
				started, release := make(chan struct{}), make(chan struct{})
				s.areaCatalog = controlledAreaCatalog(func(ctx context.Context) (smarthome.CatalogSnapshot, error) {
					close(started)
					select {
					case <-release:
						return snapshot, nil
					case <-ctx.Done():
						return smarthome.CatalogSnapshot{}, ctx.Err()
					}
				})
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- consoleRequest(s, "GET", consolePrefix+path, "", cookie, "", "", "") }()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("read did not reach controlled upstream barrier")
				}
				switch change {
				case "disable":
					_, err = s.consoleStore.UpdateMember(user.ID, "Reader", true)
				case "logout":
					err = s.consoleStore.Logout(cookie.Value)
				case "password":
					err = s.consoleStore.ChangePassword(cookie.Value, testConsolePassword, "new-family-password-321")
				}
				if err != nil {
					close(release)
					t.Fatal(err)
				}
				close(release)
				response := <-done
				if response.Code == 200 || strings.Contains(response.Body.String(), `"totals"`) || strings.Contains(response.Body.String(), `"ollama"`) || strings.Contains(response.Body.String(), `"device"`) {
					t.Fatalf("revoked caller received business data: status=%d body=%s", response.Code, response.Body.String())
				}
				if response.Code != 401 && response.Code != 403 {
					t.Errorf("revoked caller gets wrong safe auth error: status=%d body=%s", response.Code, response.Body.String())
				}
			})
		}
	}
}
