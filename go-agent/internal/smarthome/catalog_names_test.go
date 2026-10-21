package smarthome

import (
	"context"
	"testing"
)

// Removing a registered name must produce an honest display label while IDs
// remain available for routing, lookup and technical details.
func TestCatalogUnnamedDisplayPreservesIdentifiersAndNamePriority(t *testing.T) {
	reg := RegistrySnapshot{
		Areas: []RegistryArea{{ID: "room", Name: "客厅"}},
		Devices: []RegistryDevice{
			{ID: "raw_controller_id", AreaID: cp("room")},
			{ID: "named", Name: cp("原始设备名"), NameByUser: cp("用户设备名"), AreaID: cp("room")},
		},
		Entities: []RegistryEntity{
			{EntityID: "switch.unnamed", DeviceID: cp("raw_controller_id")},
			{EntityID: "sensor.standalone", AreaID: cp("room")},
			{EntityID: "switch.named", Name: cp("用户实体名"), OriginalName: cp("原始实体名"), DeviceID: cp("named")},
			{EntityID: "switch.original", OriginalName: cp("原始通道名"), DeviceID: cp("named")},
		},
	}
	svc := NewCatalogService(context.Background(), &catalogFake{reg: reg})
	defer svc.Close()
	snapshot, err := svc.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	list, err := snapshot.ListDevices("a_cm9vbQ")
	if err != nil {
		t.Fatal(err)
	}
	entities := map[string]string{}
	for _, device := range list.Devices {
		switch device.ID {
		case "d_cmF3X2NvbnRyb2xsZXJfaWQ":
			if device.Name != "未命名设备" {
				t.Errorf("raw device ID shown as name: %q", device.Name)
			}
		case "d_bmFtZWQ":
			if device.Name != "用户设备名" {
				t.Errorf("user device name lost: %q", device.Name)
			}
		case "e_c2Vuc29yLnN0YW5kYWxvbmU":
			if device.Name != "未命名实体" {
				t.Errorf("standalone card uses raw entity ID: %q", device.Name)
			}
		}
		for _, entity := range device.Entities {
			entities[entity.EntityID] = entity.Name
		}
	}
	for id, want := range map[string]string{"switch.unnamed": "未命名实体", "sensor.standalone": "未命名实体", "switch.named": "用户实体名", "switch.original": "原始通道名"} {
		if entities[id] != want {
			t.Errorf("%s display name = %q, want %q", id, entities[id], want)
		}
	}
	search, err := snapshot.ListAreas("switch.unnamed")
	if err != nil || len(search.Areas) != 1 || len(search.Areas[0].Matches) != 1 {
		t.Fatalf("technical ID search changed: %+v %v", search, err)
	}
	found := false
	for _, target := range snapshot.QueryTargets() {
		if target.EntityID == "switch.unnamed" {
			found = true
			if target.Name != "未命名实体" {
				t.Errorf("query target name uses raw ID: %q", target.Name)
			}
		}
	}
	if !found {
		t.Fatal("unnamed entity identity lost from query targets")
	}
}
