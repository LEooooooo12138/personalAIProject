package smarthome

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func classificationFixture() RegistrySnapshot {
	return RegistrySnapshot{Areas: []RegistryArea{{ID: "b1", Name: "地下室B1", Aliases: []string{"地下室"}}, {ID: "living", Name: "客厅"}},
		Devices: []RegistryDevice{
			{ID: "stairs", Name: cp("负一楼楼梯")}, {ID: "control", Name: cp("负一楼中控")}, {ID: "scene", Name: cp("回家模式负一楼")},
			{ID: "elder", Name: cp("老人房厕所")}, {ID: "loft", Name: cp("地下室夹层灯")},
			{ID: "ambiguous", Name: cp("阳台灯")}, {ID: "negative2", Name: cp("负二楼楼梯")},
			{ID: "preserve", Name: cp("客厅开关"), AreaID: cp("b1")}, {ID: "conflict", Name: cp("客厅开关"), Labels: []string{"study"}},
			{ID: "channel", Name: cp("客厅开关")},
		}, Entities: []RegistryEntity{{EntityID: "switch.channel1", DeviceID: cp("channel"), OriginalName: cp("开关 1")}, {EntityID: "sensor.solo", OriginalName: cp("电视")}},
		Labels: []RegistryLabel{{ID: "study", Name: "书房"}},
	}
}
func TestAreaPlanApprovedRulesAndPreservesInheritance(t *testing.T) {
	plan := BuildAreaPlan(classificationFixture())
	items := map[string]AreaPlanItem{}
	for _, item := range plan.Items {
		items[item.ID] = item
	}
	for id, want := range map[string]string{"stairs": "地下室B1", "control": "地下室B1", "scene": "地下室B1", "elder": "老人房", "loft": "地下室夹层", "ambiguous": "其他", "negative2": "负二楼楼梯", "conflict": "其他", "channel": "客厅", "sensor.solo": "其他"} {
		if items[id].TargetName != want {
			t.Errorf("%s target=%s want=%s", id, items[id].TargetName, want)
		}
	}
	if items["preserve"].Action != "preserve" || items["conflict"].Reason != "name_label_conflict" {
		t.Fatal(items)
	}
	if _, ok := items["switch.channel1"]; ok {
		t.Fatal("unknown switch channel creates entity override")
	}
	if items["stairs"].TargetAreaID == nil || *items["stairs"].TargetAreaID != "b1" {
		t.Fatal("basement alias not reused")
	}
}

type areaFake struct {
	reads, failReadAt                  int
	noUpdate, noCreate, conflictUpdate bool
	afterUpdate                        func()
	reg                                RegistrySnapshot
	writes, creates                    int
	loseCreate, loseUpdate             bool
	beforeWrite                        func(*RegistrySnapshot)
}

func (f *areaFake) GetRegistry(ctx context.Context) (RegistrySnapshot, error) {
	if e := ctx.Err(); e != nil {
		return RegistrySnapshot{}, e
	}
	f.reads++
	if f.reads == f.failReadAt {
		return RegistrySnapshot{}, errors.New("fixture-secret")
	}
	if f.beforeWrite != nil {
		fn := f.beforeWrite
		f.beforeWrite = nil
		fn(&f.reg)
	}
	return f.reg, nil
}
func (f *areaFake) CreateArea(_ context.Context, name string) (RegistryArea, error) {
	f.creates++
	if f.noCreate {
		return RegistryArea{}, errors.New("fixture-secret")
	}
	a := RegistryArea{ID: "new-" + name, Name: name}
	f.reg.Areas = append(f.reg.Areas, a)
	if f.loseCreate {
		return RegistryArea{}, errors.New("fixture-secret")
	}
	return a, nil
}
func (f *areaFake) SetDeviceArea(_ context.Context, id string, a *string) error {
	f.writes++
	if f.noUpdate {
		return errors.New("fixture-secret")
	}
	if f.afterUpdate != nil {
		f.afterUpdate()
	}
	if f.conflictUpdate {
		a = cp("manual")
	}
	for i := range f.reg.Devices {
		if f.reg.Devices[i].ID == id {
			f.reg.Devices[i].AreaID = clonePtr(a)
		}
	}
	if f.loseUpdate {
		return errors.New("fixture-secret")
	}
	return nil
}
func (f *areaFake) SetEntityArea(_ context.Context, id string, a *string) error {
	f.writes++
	if f.noUpdate {
		return errors.New("fixture-secret")
	}
	if f.afterUpdate != nil {
		f.afterUpdate()
	}
	if f.conflictUpdate {
		a = cp("manual")
	}
	for i := range f.reg.Entities {
		if f.reg.Entities[i].EntityID == id {
			f.reg.Entities[i].AreaID = clonePtr(a)
		}
	}
	return nil
}
func TestAreaApplyReadbackRecoversLostCreateAndWriteAndReplayIsNoop(t *testing.T) {
	f := &areaFake{reg: RegistrySnapshot{Areas: []RegistryArea{}, Devices: []RegistryDevice{{ID: "one", Name: cp("未知")}}, Entities: []RegistryEntity{{EntityID: "sensor.solo"}}}, loseCreate: true, loseUpdate: true}
	plan := BuildAreaPlan(f.reg)
	r, e := ApplyAreaPlan(context.Background(), f, plan)
	if e != nil || r.Success != 2 || r.Failed != 0 || f.creates != 1 || f.writes != 2 {
		t.Fatal(r, e, f.creates, f.writes)
	}
	r, e = ApplyAreaPlan(context.Background(), f, plan)
	if e != nil || r.Skipped != 2 || f.creates != 1 || f.writes != 2 {
		t.Fatal(r, e, f.creates, f.writes)
	}
}
func TestAreaApplySkipsManualChangeAndDuplicateAreaAmbiguity(t *testing.T) {
	f := &areaFake{reg: RegistrySnapshot{Areas: []RegistryArea{{ID: "other", Name: "其他"}, {ID: "manual", Name: "手动"}}, Devices: []RegistryDevice{{ID: "one", Name: cp("未知")}}}}
	plan := BuildAreaPlan(f.reg)
	f.beforeWrite = func(r *RegistrySnapshot) { r.Devices[0].AreaID = cp("manual") }
	r, e := ApplyAreaPlan(context.Background(), f, plan)
	if e != nil || r.Conflict != 1 || f.writes != 0 {
		t.Fatal(r, e)
	}
	f = &areaFake{reg: RegistrySnapshot{Areas: []RegistryArea{{ID: "other1", Name: "其他"}, {ID: "other2", Name: "其他"}}, Devices: []RegistryDevice{{ID: "one"}}}}
	r, e = ApplyAreaPlan(context.Background(), f, BuildAreaPlan(f.reg))
	if e != nil || r.Conflict != 1 || f.writes != 0 || f.creates != 0 {
		t.Fatal(r, e)
	}
}
func TestAreaApplyCancelledDoesNotWrite(t *testing.T) {
	f := &areaFake{reg: classificationFixture()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := ApplyAreaPlan(ctx, f, BuildAreaPlan(f.reg))
	if !errors.Is(e, context.Canceled) || f.writes != 0 || f.creates != 0 {
		t.Fatal(e)
	}
}

func TestAreaApplyReportsFailedUnknownAndConcurrentWriteWithoutRawErrors(t *testing.T) {
	for _, mode := range []string{"failed", "unknown", "changed", "create_failed"} {
		t.Run(mode, func(t *testing.T) {
			f := &areaFake{reg: RegistrySnapshot{Areas: []RegistryArea{{ID: "other", Name: "其他"}}, Devices: []RegistryDevice{{ID: "one", Name: cp("无地点")}}}}
			switch mode {
			case "failed":
				f.noUpdate = true
			case "unknown":
				f.failReadAt = 2
			case "changed":
				f.conflictUpdate = true
			case "create_failed":
				f.reg.Areas = nil
				f.noCreate = true
			}
			r, e := ApplyAreaPlan(context.Background(), f, BuildAreaPlan(f.reg))
			if e != nil {
				t.Fatal(e)
			}
			if len(r.Items) != 1 || strings.Contains(r.Items[0].ResultCode, "fixture-secret") {
				t.Fatal(r)
			}
			switch mode {
			case "failed", "create_failed":
				if r.Failed != 1 {
					t.Fatal(r)
				}
			case "unknown":
				if r.Unknown != 1 {
					t.Fatal(r)
				}
			case "changed":
				if r.Conflict != 1 {
					t.Fatal(r)
				}
			}
		})
	}
}
func TestAreaApplyReturnsCancellationAfterLastWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &areaFake{reg: RegistrySnapshot{Areas: []RegistryArea{{ID: "other", Name: "其他"}}, Devices: []RegistryDevice{{ID: "one"}}}, afterUpdate: cancel}
	r, e := ApplyAreaPlan(ctx, f, BuildAreaPlan(f.reg))
	if !errors.Is(e, context.Canceled) || r.Unknown != 1 {
		t.Fatal(r, e)
	}
}
func TestAreaPlanEqualNameAndMultipleLabelConflictsDoNotGuess(t *testing.T) {
	r := RegistrySnapshot{Devices: []RegistryDevice{{ID: "names", Name: cp("客厅书房")}, {ID: "labels", Name: cp("电视"), Labels: []string{"l1", "l2"}}}, Labels: []RegistryLabel{{ID: "l1", Name: "客厅"}, {ID: "l2", Name: "厨房"}}}
	p := BuildAreaPlan(r)
	for _, item := range p.Items {
		if item.TargetName != "其他" {
			t.Fatal(p)
		}
	}
}
