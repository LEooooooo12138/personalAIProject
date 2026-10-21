package gateway

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

type namedSuggestionCatalogSource struct{}

func (namedSuggestionCatalogSource) GetRegistry(context.Context) (smarthome.RegistrySnapshot, error) {
	p := func(value string) *string { return &value }
	return smarthome.RegistrySnapshot{
		Areas:   []smarthome.RegistryArea{{ID: "living", Name: "客厅"}, {ID: "kitchen", Name: "厨房"}},
		Devices: []smarthome.RegistryDevice{{ID: "controller", Name: p("客厅三路开关"), AreaID: p("living")}, {ID: "UNNAMED_RAW_DEVICE_ID", AreaID: p("kitchen")}},
		Entities: []smarthome.RegistryEntity{
			{EntityID: "light.room", Name: p("开关 1"), DeviceID: p("controller"), AreaID: p("kitchen")},
			{EntityID: "binary_sensor.house", Name: p("全家占用")},
			{EntityID: "switch.source", Name: p("触发开关")},
			{EntityID: "switch.unnamed", DeviceID: p("UNNAMED_RAW_DEVICE_ID")},
			{EntityID: "sensor.private", Name: p("UNRELATED_DIRECTORY_SECRET")},
		},
	}, nil
}
func (namedSuggestionCatalogSource) GetStates(context.Context) ([]smarthome.EntityState, error) {
	return []smarthome.EntityState{{EntityID: "binary_sensor.house", State: "on", Attributes: map[string]any{"device_class": "occupancy", "secret": "RAW_ATTRIBUTE_SECRET"}}}, nil
}
func namedSuggestionSnapshot(t *testing.T) smarthome.CatalogSnapshot {
	t.Helper()
	catalog := smarthome.NewCatalogService(context.Background(), namedSuggestionCatalogSource{})
	t.Cleanup(catalog.Close)
	snapshot, err := catalog.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// A regression that projects IDs instead of catalog names must fail every real
// HTTP publication path, including the response of an explicit mutation.
func TestConsoleSuggestionPresentationUsesCatalogAcrossResponses(t *testing.T) {
	s, writes := suggestionsFixture(t)
	s.areaCatalog = testAreaCatalog{snapshot: namedSuggestionSnapshot(t)}
	unbound := persistedTimeRule("unbound", "pending")
	unbound.PresenceBinding = nil
	ignored := persistedTimeRule("ignored", "pending")
	seedSuggestions(t, s, []smarthome.RuleSuggestion{unbound, ignored})
	admin, csrf, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
	check := func(view map[string]any, count int) {
		t.Helper()
		title, _ := view["title"].(string)
		if !strings.Contains(title, "厨房") || !strings.Contains(title, "客厅三路开关") || !strings.Contains(title, "开关 1") || strings.Contains(title, "light.room") {
			t.Errorf("title lacks verified entity context: %q", title)
		}
		entities, ok := view["entities"].([]any)
		if !ok || len(entities) != count {
			t.Errorf("rule metadata missing or includes unrelated entities: %v", view["entities"])
			return
		}
		for _, raw := range entities {
			e := raw.(map[string]any)
			if e["entity_id"] == "light.room" && (e["name"] != "开关 1" || e["area_name"] != "厨房" || e["device_name"] != "客厅三路开关" || e["domain"] != "light" || e["device_class"] != nil) {
				t.Errorf("wrong action metadata: %v", e)
			}
			if e["entity_id"] == "binary_sensor.house" && (e["name"] != "全家占用" || e["device_class"] != "occupancy" || e["device_name"] != "") {
				t.Errorf("wrong presence metadata: %v", e)
			}
		}
	}
	list := consoleJSON(t, consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", ""), 200)
	for _, raw := range list["suggestions"].([]any) {
		view := raw.(map[string]any)
		count := 2
		if view["id"] == "unbound" {
			count = 1
		}
		check(view, count)
	}
	bound := consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/unbound/bindings", `{"presence":{"entity_id":"binary_sensor.house","state":"on"}}`, admin, csrf, testConsoleOrigin, ""), 200)["suggestion"].(map[string]any)
	check(bound, 2)
	if writes.Load() != 0 {
		t.Fatal("binding installed automation")
	}
	id := bound["id"].(string)
	check(consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/"+id+"/confirm", `{}`, admin, csrf, testConsoleOrigin, ""), 200)["suggestion"].(map[string]any), 2)
	check(consoleJSON(t, consoleRequest(s, "POST", consolePrefix+"/admin/suggestions/ignored/ignore", `{}`, admin, csrf, testConsoleOrigin, ""), 200)["suggestion"].(map[string]any), 2)
}

func TestConsoleSuggestionMetadataOnlyContainsSharedRuleEntities(t *testing.T) {
	s, _ := suggestionsFixture(t)
	s.areaCatalog = testAreaCatalog{snapshot: namedSuggestionSnapshot(t)}
	seedSuggestions(t, s, []smarthome.RuleSuggestion{persistedTimeRule("shared", "confirmed"), persistedTimeRule("private", "pending")})
	sharing, _ := s.consoleStore.GetSharing()
	if _, err := s.consoleStore.PutSharing(sharing.Revision, []string{"light.room", "binary_sensor.house"}, []string{"shared"}); err != nil {
		t.Fatal(err)
	}
	_, member := consoleMemberLogin(t, s, "Member")
	w := consoleRequest(s, "GET", consolePrefix+"/suggestions", "", member, "", "", "")
	body := consoleJSON(t, w, 200)
	views := body["suggestions"].([]any)
	if len(views) != 1 {
		t.Fatalf("wrong visible suggestions: %v", views)
	}
	entities, ok := views[0].(map[string]any)["entities"].([]any)
	if !ok || len(entities) != 2 {
		t.Fatalf("shared rule names unavailable: %v", views)
	}
	if strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "switch.source") || strings.Contains(w.Body.String(), `"private"`) {
		t.Fatalf("unrelated or raw data leaked: %s", w.Body.String())
	}
}

func TestConsoleSuggestionPresentationFallbackAndLegacyTarget(t *testing.T) {
	for _, mode := range []string{"unnamed", "missing", "stale", "error", "legacy", "correlation"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := suggestionsFixture(t)
			snapshot := namedSuggestionSnapshot(t)
			item := persistedTimeRule("rule", "pending")
			item.PresenceBinding = nil
			want := "开关 1"
			switch mode {
			case "unnamed":
				item.Intent.EntityID = "switch.unnamed"
				want = "未命名实体"
			case "missing":
				item.Intent.EntityID = "switch.absent"
				want = "名称待核对"
			case "stale":
				snapshot.Meta.Freshness = "stale"
				want = "名称待核对"
			case "error":
				want = "名称待核对"
			case "legacy":
				item.Intent = nil
				item.Automation = &smarthome.AutomationConfig{Trigger: []smarthome.AutomationTrigger{{Platform: "state", EntityID: "switch.source", To: "on"}}, Action: []smarthome.AutomationAction{{Service: "light.turn_on", Target: smarthome.AutomationTarget{EntityID: "light.room"}}}}
			case "correlation":
				item.Intent = &smarthome.SuggestionIntent{SchemaVersion: 1, Kind: "correlation", EntityID: "switch.source", RelatedID: "light.room", TimeZone: "UTC", RequiredConditions: []string{"sunset_to_midnight"}}
			}
			var calls atomic.Int32
			s.areaCatalog = controlledAreaCatalog(func(context.Context) (smarthome.CatalogSnapshot, error) {
				calls.Add(1)
				if mode == "error" {
					return smarthome.CatalogSnapshot{}, errors.New("RAW_CATALOG_SECRET")
				}
				return snapshot, nil
			})
			second := item
			second.ID = "second"
			seedSuggestions(t, s, []smarthome.RuleSuggestion{item, second})
			admin, _, _ := consoleLogin(t, s, "Owner", testConsolePassword, testConsoleOrigin)
			w := consoleRequest(s, "GET", consolePrefix+"/suggestions", "", admin, "", "", "")
			body := consoleJSON(t, w, 200)
			for _, raw := range body["suggestions"].([]any) {
				view := raw.(map[string]any)
				title := view["title"].(string)
				if !strings.Contains(title, want) || strings.Contains(title, ".room") || strings.Contains(title, ".unnamed") || strings.Contains(title, ".absent") || strings.Contains(title, "触发开关") {
					t.Errorf("wrong target/fallback title: %q want %q", title, want)
				}
				if mode == "unnamed" && (!strings.Contains(title, "未命名设备") || strings.Contains(title, "UNNAMED_RAW_DEVICE_ID")) {
					t.Errorf("unnamed device leaked its registry ID in title: %q", title)
				}
			}
			if calls.Load() != 1 {
				t.Errorf("directory must be read once per request, got %d", calls.Load())
			}
			if strings.Contains(w.Body.String(), "SECRET") {
				t.Fatal("raw strings leaked")
			}
		})
	}
}
