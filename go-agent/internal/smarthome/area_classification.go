package smarthome

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type AreaPlanItem struct {
	Kind                 string  `json:"kind"`
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	OriginalAreaID       *string `json:"original_area_id"`
	TargetAreaID         *string `json:"target_area_id"`
	TargetName           string  `json:"target_name"`
	Action               string  `json:"action"`
	Reason               string  `json:"reason"`
	LoadLocationVerified bool    `json:"load_location_verified"`
}
type AreaPlan struct {
	GeneratedAt                  time.Time      `json:"generated_at"`
	Items                        []AreaPlanItem `json:"items"`
	CreateAreas                  []string       `json:"create_areas"`
	AreaTargetImpactVerification string         `json:"area_target_impact_verification"`
}
type AreaApplyItem struct {
	AreaPlanItem
	Status       string  `json:"status"`
	ResultCode   string  `json:"result_code"`
	ResultAreaID *string `json:"result_area_id"`
}
type AreaApplyReport struct {
	Items                        []AreaApplyItem `json:"items"`
	Success                      int             `json:"success"`
	Skipped                      int             `json:"skipped"`
	Conflict                     int             `json:"conflict"`
	Failed                       int             `json:"failed"`
	Unknown                      int             `json:"unknown"`
	AreaTargetImpactVerification string          `json:"area_target_impact_verification"`
}
type AreaPlanClient interface {
	GetRegistry(context.Context) (RegistrySnapshot, error)
	CreateArea(context.Context, string) (RegistryArea, error)
	SetDeviceArea(context.Context, string, *string) error
	SetEntityArea(context.Context, string, *string) error
}
type placeTerm struct{ term, target string }

var placeTerms = []placeTerm{
	{"地下室夹层", "地下室夹层"}, {"主卧电梯口", "主卧电梯口"}, {"主卧厕所", "主卧厕所"}, {"老人房厕所", "老人房"},
	{"负一楼楼梯", "地下室"}, {"负二楼楼梯", "负二楼楼梯"}, {"一楼楼梯", "一楼楼梯"}, {"二楼楼梯", "二楼楼梯"}, {"三楼后门", "三楼后门"}, {"二楼公卫", "二楼公卫"},
	{"地下室B1", "地下室"}, {"地下室", "地下室"}, {"负一楼", "地下室"}, {"老人房", "老人房"}, {"儿童房", "儿童房"}, {"衣帽间", "衣帽间"}, {"客厅", "客厅"}, {"厨房", "厨房"}, {"主卧", "主卧"}, {"书房", "书房"}, {"餐厅", "餐厅"}, {"车库", "车库"}, {"客卫", "客卫"}, {"入户", "入户"},
}

func namePlace(name string) (string, bool) {
	longest := 0
	targets := map[string]bool{}
	for _, p := range placeTerms {
		if strings.Contains(name, p.term) {
			size := utf8.RuneCountInString(p.term)
			if size > longest {
				longest = size
				targets = map[string]bool{}
			}
			if size == longest {
				targets[p.target] = true
			}
		}
	}
	if len(targets) == 1 {
		for target := range targets {
			return target, false
		}
	}
	return "", len(targets) > 1
}
func labelPlace(name string) string {
	for _, p := range placeTerms {
		if strings.TrimSpace(name) == p.term {
			return p.target
		}
	}
	return ""
}
func resolveArea(reg RegistrySnapshot, name string) (RegistryArea, bool, bool) {
	var found RegistryArea
	count := 0
	for _, a := range reg.Areas {
		matches := a.Name == name
		for _, alias := range a.Aliases {
			if alias == name {
				matches = true
			}
		}
		if matches {
			found = a
			count++
		}
	}
	return found, count == 1, count > 1
}
func BuildAreaPlan(reg RegistrySnapshot) AreaPlan {
	plan := AreaPlan{GeneratedAt: time.Now().UTC(), Items: []AreaPlanItem{}, CreateAreas: []string{}, AreaTargetImpactVerification: "unverified"}
	labels := map[string]string{}
	for _, l := range reg.Labels {
		labels[l.ID] = labelPlace(l.Name)
	}
	creates := map[string]bool{}
	deviceIDs := map[string]bool{}
	add := func(kind, id, name string, original *string, labelIDs []string) {
		item := AreaPlanItem{Kind: kind, ID: id, Name: name, OriginalAreaID: clonePtr(original), Action: "assign"}
		if original != nil && *original != "" {
			item.Action = "preserve"
			item.Reason = "existing_area"
			item.TargetAreaID = clonePtr(original)
			for _, a := range reg.Areas {
				if a.ID == *original {
					item.TargetName = a.Name
				}
			}
			plan.Items = append(plan.Items, item)
			return
		}
		target, ambiguous := namePlace(name)
		reason := "name"
		labelTargets := map[string]bool{}
		for _, id := range labelIDs {
			if labels[id] != "" {
				labelTargets[labels[id]] = true
			}
		}
		if ambiguous {
			target = "其他"
			reason = "ambiguous_name"
		} else if len(labelTargets) > 1 {
			target = "其他"
			reason = "conflicting_labels"
		} else if len(labelTargets) == 1 {
			for labeled := range labelTargets {
				if target != "" && target != labeled {
					target = "其他"
					reason = "name_label_conflict"
				} else {
					target = labeled
					reason = "label"
				}
			}
		} else if target == "" {
			target = "其他"
			reason = "no_unique_location"
		}
		a, found, duplicate := resolveArea(reg, target)
		item.TargetName = target
		item.Reason = reason
		if found {
			item.TargetName = a.Name
			item.TargetAreaID = clonePtr(&a.ID)
		} else if duplicate {
			item.Action = "conflict"
			item.Reason = "ambiguous_area"
		} else {
			creates[target] = true
		}
		plan.Items = append(plan.Items, item)
	}
	for _, d := range reg.Devices {
		deviceIDs[d.ID] = true
		add("device", d.ID, deviceName(d), d.AreaID, d.Labels)
	}
	for _, e := range reg.Entities {
		if e.DeviceID != nil && deviceIDs[*e.DeviceID] {
			continue
		}
		add("entity", e.EntityID, entityName(e), e.AreaID, e.Labels)
	}
	for name := range creates {
		plan.CreateAreas = append(plan.CreateAreas, name)
	}
	sort.Strings(plan.CreateAreas)
	sort.Slice(plan.Items, func(i, j int) bool {
		if plan.Items[i].Kind != plan.Items[j].Kind {
			return plan.Items[i].Kind < plan.Items[j].Kind
		}
		return plan.Items[i].ID < plan.Items[j].ID
	})
	return plan
}
func sameArea(a, b *string) bool {
	if a == nil {
		return b == nil || *b == ""
	}
	if b == nil {
		return *a == ""
	}
	return *a == *b
}
func currentArea(reg RegistrySnapshot, item AreaPlanItem) (*string, bool) {
	if item.Kind == "device" {
		for _, d := range reg.Devices {
			if d.ID == item.ID {
				return d.AreaID, true
			}
		}
	}
	if item.Kind == "entity" {
		for _, e := range reg.Entities {
			if e.EntityID == item.ID {
				return e.AreaID, true
			}
		}
	}
	return nil, false
}

// ApplyAreaPlan only sends area metadata. Failed writes are read back once, never replayed blindly.
func ApplyAreaPlan(ctx context.Context, client AreaPlanClient, plan AreaPlan) (AreaApplyReport, error) {
	report := AreaApplyReport{Items: []AreaApplyItem{}, AreaTargetImpactVerification: "unverified"}
	record := func(item AreaPlanItem, status, code string, result *string) {
		report.Items = append(report.Items, AreaApplyItem{AreaPlanItem: item, Status: status, ResultCode: code, ResultAreaID: clonePtr(result)})
		switch status {
		case "success":
			report.Success++
		case "skipped":
			report.Skipped++
		case "conflict":
			report.Conflict++
		case "failed":
			report.Failed++
		default:
			report.Unknown++
		}
	}
	for _, item := range plan.Items {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if item.Action == "preserve" {
			record(item, "skipped", "existing_area", item.OriginalAreaID)
			continue
		}
		if item.Action == "conflict" {
			record(item, "conflict", item.Reason, nil)
			continue
		}
		if (item.Kind != "device" && item.Kind != "entity") || item.ID == "" || strings.TrimSpace(item.TargetName) == "" {
			record(item, "failed", "invalid_plan", nil)
			continue
		}
		reg, err := client.GetRegistry(ctx)
		if err != nil {
			record(item, "failed", "registry_unavailable", nil)
			continue
		}
		current, exists := currentArea(reg, item)
		if !exists {
			record(item, "conflict", "item_missing", nil)
			continue
		}
		target, found, duplicate := resolveArea(reg, item.TargetName)
		if duplicate {
			record(item, "conflict", "ambiguous_area", current)
			continue
		}
		if found && sameArea(current, &target.ID) {
			record(item, "skipped", "already_assigned", current)
			continue
		}
		if !sameArea(current, item.OriginalAreaID) {
			record(item, "conflict", "area_changed", current)
			continue
		}
		if item.TargetAreaID != nil && (!found || target.ID != *item.TargetAreaID) {
			record(item, "conflict", "target_changed", current)
			continue
		}
		if !found {
			_, createErr := client.CreateArea(ctx, item.TargetName)
			// Even a lost response might have created the area. Verify before any retry.
			latest, readErr := client.GetRegistry(ctx)
			if readErr != nil {
				record(item, "unknown", "create_readback_unavailable", current)
				continue
			}
			target, found, duplicate = resolveArea(latest, item.TargetName)
			if duplicate {
				record(item, "conflict", "ambiguous_area", current)
				continue
			}
			if !found {
				code := "create_not_verified"
				if createErr != nil {
					code = "create_failed"
				}
				record(item, "failed", code, current)
				continue
			}
			// Recheck the original value after creating an area.
			current, exists = currentArea(latest, item)
			if !exists || !sameArea(current, item.OriginalAreaID) {
				record(item, "conflict", "area_changed", current)
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		var writeErr error
		if item.Kind == "device" {
			writeErr = client.SetDeviceArea(ctx, item.ID, &target.ID)
		} else {
			writeErr = client.SetEntityArea(ctx, item.ID, &target.ID)
		}
		latest, readErr := client.GetRegistry(ctx)
		if readErr != nil {
			record(item, "unknown", "write_readback_unavailable", nil)
			continue
		}
		result, exists := currentArea(latest, item)
		if exists && sameArea(result, &target.ID) {
			record(item, "success", "verified", result)
		} else if !exists || !sameArea(result, item.OriginalAreaID) {
			record(item, "conflict", "readback_changed", result)
		} else {
			code := "write_not_verified"
			if writeErr != nil {
				code = "write_failed"
			}
			record(item, "failed", code, result)
		}
	}
	return report, ctx.Err()
}
