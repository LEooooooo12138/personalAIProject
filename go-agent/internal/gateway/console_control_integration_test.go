package gateway

import (
	"context"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"testing"
)

type controlGatewayHA struct {
	writes int
	state  string
}

func (h *controlGatewayHA) GetRegistry(context.Context) (smarthome.RegistrySnapshot, error) {
	n := "台灯"
	a := "living"
	return smarthome.RegistrySnapshot{Areas: []smarthome.RegistryArea{{ID: a, Name: "客厅"}}, Entities: []smarthome.RegistryEntity{{EntityID: "light.test", Name: &n, AreaID: &a}}}, nil
}
func (h *controlGatewayHA) GetStates(context.Context) ([]smarthome.EntityState, error) {
	return []smarthome.EntityState{{EntityID: "light.test", State: h.state}}, nil
}
func (h *controlGatewayHA) GetState(context.Context, string) (*smarthome.EntityState, error) {
	return &smarthome.EntityState{EntityID: "light.test", State: h.state}, nil
}
func (h *controlGatewayHA) CallService(_ context.Context, domain, action string, data map[string]interface{}) error {
	if domain != "light" || action != "turn_on" || data["entity_id"] != "light.test" {
		panic("invalid write")
	}
	h.writes++
	h.state = "on"
	return nil
}
func TestControlHTTPBoundaries(t *testing.T) {
	s := consoleTestServer(t, testConsoleOrigin)
	consoleBootstrap(t, s)
	admin, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
	_, member := consoleMemberLogin(t, s, "alice")
	p, err := s.consoleStore.Resolve(admin.Value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.sessionStore.BrowserSession("console", "control-session", "console:"+p.UserID, true); err != nil {
		t.Fatal(err)
	}
	ha := &controlGatewayHA{state: "off"}
	catalog := smarthome.NewCatalogService(context.Background(), ha)
	defer catalog.Close()
	s.control, err = smarthome.NewControlService(smarthome.ControlConfig{Targets: []smarthome.ControlTarget{{EntityID: "light.test", Name: "台灯", AreaName: "客厅", LoadLocationVerified: true, AllowedActions: []string{"turn_on"}}}}, catalog, ha, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"session_id":"control-session","request_id":"request-1","entity_id":"light.test","action":"turn_on"}`
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/control/proposals", body, admin, "", testConsoleOrigin, ""), 403)
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/control/proposals", body, admin, csrf, "https://evil.example", ""), 403)
	outcome := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/control/proposals", body, admin, csrf, testConsoleOrigin, ""), 200)
	id := outcome["proposal"].(map[string]any)["id"].(string)
	route := consolePrefix + "/control/proposals/" + id
	if ha.writes != 0 {
		t.Fatal("proposal wrote to HA")
	}
	consoleJSON(t, consoleRequest(s, "GET", route, "", member, "", "", ""), 404)
	consoleJSON(t, consoleRequest(s, "GET", route, "", admin, "", "", ""), 200)
	for _, invalid := range []string{`{"action":"turn_off"}`, `{"entity_id":"light.other"}`, `{"x":1,"x":2}`, `{} {}`, `null`} {
		consoleJSON(t, consoleRequest(s, "POST", route+"/confirm", invalid, admin, csrf, testConsoleOrigin, ""), 400)
	}
	if ha.writes != 0 {
		t.Fatal("tampered request wrote to HA")
	}
	result := consoleJSON(t, consoleRequest(s, "POST", route+"/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 200)
	if result["status"] != "succeeded" || ha.writes != 1 {
		t.Fatalf("confirm result %v writes %d", result, ha.writes)
	}
	consoleJSON(t, consoleRequest(s, "POST", route+"/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 409)
	if ha.writes != 1 {
		t.Fatal("repeat wrote again")
	}
	member, memberCSRF, _ := consoleLogin(t, s, "alice", testConsolePassword, testConsoleOrigin)
	consoleJSON(t, consoleRequest(s, "POST", route+"/confirm", `{}`, member, memberCSRF, testConsoleOrigin, ""), 403)

	// Session ownership is checked independently of knowing an entity and request ID.
	bad := `{"session_id":"other","request_id":"r","entity_id":"light.test","action":"turn_on"}`
	consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/control/proposals", bad, admin, csrf, testConsoleOrigin, ""), 404)
}
