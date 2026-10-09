package smarthome

import (
	"context"
	"encoding/json"
	"errors"
	"go.uber.org/zap"
	"strings"
	"sync"
	"testing"
	"time"
)

func cp(s string) *string { return &s }
func catalogFixture() RegistrySnapshot {
	return RegistrySnapshot{
		Areas:   []RegistryArea{{ID: "a", Name: "客厅"}, {ID: "b", Name: "书房"}, {ID: "empty", Name: "空房"}},
		Devices: []RegistryDevice{{ID: "controller", Name: cp("控制器"), AreaID: cp("a")}, {ID: "unassigned", Name: cp("空设备")}, {ID: "orphan", Name: cp("异常"), AreaID: cp("missing")}},
		Entities: []RegistryEntity{
			{EntityID: "switch.one", DeviceID: cp("controller"), AreaID: cp("b"), OriginalName: cp("开关 1")},
			{EntityID: "switch.two", DeviceID: cp("controller"), AreaID: cp("b"), HiddenBy: cp("user"), Category: cp("config")},
			{EntityID: "sensor.solo", OriginalName: cp("独立"), DisabledBy: cp("integration")},
			{EntityID: "sensor.invalid", DeviceID: cp("orphan"), AreaID: cp("missing")},
		}, Labels: []RegistryLabel{},
	}
}

type catalogFake struct {
	mu          sync.Mutex
	reg         RegistrySnapshot
	states      []EntityState
	fail        bool
	stateFail   bool
	calls       int
	gate        chan struct{}
	entered     chan struct{}
	sawDeadline time.Duration
}

func (f *catalogFake) GetRegistry(ctx context.Context) (RegistrySnapshot, error) {
	f.mu.Lock()
	f.calls++
	g, e := f.gate, f.entered
	fail := f.fail
	reg := f.reg
	if d, ok := ctx.Deadline(); ok {
		f.sawDeadline = time.Until(d)
	}
	f.mu.Unlock()
	if e != nil {
		select {
		case e <- struct{}{}:
		default:
		}
	}
	if g != nil {
		select {
		case <-ctx.Done():
			return RegistrySnapshot{}, ctx.Err()
		case <-g:
		}
	}
	if fail {
		return RegistrySnapshot{}, errors.New("fixture-secret")
	}
	return reg, nil
}
func (f *catalogFake) GetStates(context.Context) ([]EntityState, error) {
	if f.stateFail {
		return nil, errors.New("fixture-secret")
	}
	return f.states, nil
}
func TestCatalogAreaUnionTotalsAndSafeNullableProjection(t *testing.T) {
	f := &catalogFake{reg: catalogFixture(), states: []EntityState{{EntityID: "switch.one", State: "on", Attributes: map[string]any{"unit_of_measurement": "W", "secret": "fixture-secret"}}}}
	svc := NewCatalogService(context.Background(), f)
	defer svc.Close()
	s, e := svc.Get(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	list, e := s.ListAreas("")
	if e != nil {
		t.Fatal(e)
	}
	if list.Totals.Devices != 3 || list.Totals.Entities != 4 || list.Totals.StandaloneEntities != 1 || len(list.Areas) != 4 {
		t.Fatalf("%+v", list)
	}
	a, e := s.ListDevices("a_YQ")
	if e != nil {
		t.Fatal(e)
	}
	if len(a.Devices) != 1 || a.Devices[0].Membership != "direct" || len(a.Devices[0].Entities) != 0 {
		t.Fatalf("%+v", a)
	}
	b, e := s.Device("a_Yg", "d_Y29udHJvbGxlcg")
	if e != nil {
		t.Fatal(e)
	}
	if b.Device.Membership != "entity" || len(b.Device.Entities) != 2 || b.Device.Entities[0].State == nil || b.Device.Entities[1].State != nil || b.Device.LoadLocationVerified {
		t.Fatalf("%+v", b)
	}
	if *b.Device.Entities[0].Unit != "W" || b.Device.Entities[0].LastChanged != nil {
		t.Fatal("bad nullable state")
	}
	other, e := s.ListDevices("u_other")
	if e != nil || len(other.Devices) != 3 {
		t.Fatalf("%+v %v", other, e)
	}
	empty, e := s.ListDevices("a_ZW1wdHk")
	if e != nil || len(empty.Devices) != 0 {
		t.Fatalf("%+v %v", empty, e)
	}
}
func TestCatalogUsesRealOtherAndRejectsWrongCombinations(t *testing.T) {
	r := catalogFixture()
	r.Areas = append(r.Areas, RegistryArea{ID: "other", Name: "其他"})
	svc := NewCatalogService(context.Background(), &catalogFake{reg: r})
	defer svc.Close()
	s, _ := svc.Get(context.Background())
	other, e := s.ListDevices("a_b3RoZXI")
	if e != nil || len(other.Devices) != 3 {
		t.Fatal(other, e)
	}
	for _, id := range []string{"u_other", "a_YQ==", "a__w", "a_", "a_bWlzc2luZw"} {
		if _, e = s.ListDevices(id); !errors.Is(e, ErrCatalogNotFound) {
			t.Fatalf("%q %v", id, e)
		}
	}
	if _, e = s.Device("a_Yg", "d_dW5hc3NpZ25lZA"); !errors.Is(e, ErrCatalogNotFound) {
		t.Fatal(e)
	}
}
func TestCatalogSearchKeepsWholeHouseTotals(t *testing.T) {
	svc := NewCatalogService(context.Background(), &catalogFake{reg: catalogFixture()})
	defer svc.Close()
	s, _ := svc.Get(context.Background())
	list, e := s.ListAreas("switch.one")
	if e != nil || list.Totals.Devices != 3 || len(list.Areas) != 1 || len(list.Areas[0].Matches) != 1 {
		t.Fatal(list, e)
	}
	if _, e = s.ListAreas(strings.Repeat("中", 129)); !errors.Is(e, ErrCatalogInvalid) {
		t.Fatal(e)
	}
}
func TestCatalogSharesRefreshWithoutCallerCancellation(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 2)
	f := &catalogFake{reg: catalogFixture(), gate: gate, entered: entered}
	svc := NewCatalogService(context.Background(), f)
	defer svc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, e := svc.Get(ctx); first <- e }()
	<-entered
	go func() { _, e := svc.Get(context.Background()); second <- e }()
	cancel()
	if e := <-first; e != context.Canceled {
		t.Fatal(e)
	}
	close(gate)
	if e := <-second; e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls != 1 || f.sawDeadline > 10*time.Second || f.sawDeadline < 9*time.Second {
		t.Fatalf("calls=%d deadline=%s", f.calls, f.sawDeadline)
	}
}
func TestCatalogCacheFailurePreservesObservedTime(t *testing.T) {
	f := &catalogFake{reg: catalogFixture()}
	svc := NewCatalogService(context.Background(), f)
	defer svc.Close()
	_, _ = svc.Get(context.Background())
	svc.Get(context.Background())
	if f.calls != 1 {
		t.Fatal("not cached")
	}
	// Expire the observation without waiting thirty seconds; exercises real refresh logic.
	svc.mu.Lock()
	old := time.Now().Add(-31 * time.Second)
	svc.snapshot.Meta.ObservedAt = &old
	svc.snapshot.Meta.LastSuccessAt = &old
	svc.mu.Unlock()
	f.fail = true
	stale, e := svc.Get(context.Background())
	if e != nil || stale.Meta.Freshness != "stale" || stale.Meta.Connection != "unavailable" || !stale.Meta.ObservedAt.Equal(old) || !stale.Meta.LastSuccessAt.Equal(old) {
		t.Fatal(stale.Meta, e)
	}
	if stale.Meta.LastAttemptAt == nil || !stale.Meta.LastAttemptAt.After(old) {
		t.Fatal("lost attempt")
	}
	list, e := stale.ListAreas("")
	if e != nil || list.Totals.Entities != 4 {
		t.Fatal(list, e)
	}
	f.fail = false
	fresh, e := svc.Get(context.Background())
	if e != nil || fresh.Meta.Freshness != "fresh" {
		t.Fatal(fresh.Meta, e)
	}
}
func TestCatalogCloseCancelsSharedRefreshAndFirstFailureIsSafe(t *testing.T) {
	f := &catalogFake{reg: catalogFixture(), gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	svc := NewCatalogService(context.Background(), f)
	done := make(chan error, 1)
	go func() { _, e := svc.Get(context.Background()); done <- e }()
	<-f.entered
	svc.Close()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("close succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close blocked")
	}
	failed := NewCatalogService(context.Background(), &catalogFake{fail: true})
	defer failed.Close()
	s, e := failed.Get(context.Background())
	if !errors.Is(e, ErrCatalogUnavailable) || strings.Contains(e.Error(), "fixture-secret") || s.Meta.Freshness != "unknown" || s.Meta.Connection != "unavailable" || s.Meta.ObservedAt != nil {
		t.Fatal(s.Meta, e)
	}
}

func TestManagerCatalogStopBeforeStartCancelsRequests(t *testing.T) {
	f := &catalogFake{reg: catalogFixture(), gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	m, e := NewManager(HAConfig{AgentVaultPath: t.TempDir()}, zap.NewNop())
	if e != nil {
		t.Fatal(e)
	}
	m.catalog = NewCatalogService(context.Background(), f)
	done := make(chan error, 1)
	go func() { _, e := m.Catalog(context.Background()); done <- e }()
	<-f.entered
	m.Stop()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("Stop left catalog alive")
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked")
	}
	if _, e = m.Catalog(context.Background()); e == nil {
		t.Fatal("catalog usable after Stop")
	}
}

func TestCatalogPartialStateFailureAndDTOCopies(t *testing.T) {
	f := &catalogFake{reg: catalogFixture()}
	svc := NewCatalogService(context.Background(), f)
	defer svc.Close()
	s, e := svc.Get(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	details, _ := s.Device("a_Yg", "d_Y29udHJvbGxlcg")
	details.Device.Entities[1].Category = cp("changed")
	*details.Device.DirectAreaID = "changed"
	*s.Meta.ObservedAt = time.Time{}
	again, _ := svc.Get(context.Background())
	detail, _ := again.Device("a_Yg", "d_Y29udHJvbGxlcg")
	if *detail.Device.DirectAreaID != "a_YQ" || *detail.Device.Entities[1].Category != "config" || again.Meta.ObservedAt.IsZero() {
		t.Fatal("caller altered cached snapshot")
	}
	data, e := json.Marshal(detail)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), "attributes") || strings.Contains(string(data), "fixture-secret") {
		t.Fatal("unsafe projection")
	}
}
func TestCatalogTotalDeadlineStopsFirstRead(t *testing.T) {
	f := &catalogFake{gate: make(chan struct{})}
	svc := NewCatalogService(context.Background(), f)
	defer svc.Close()
	start := time.Now()
	s, e := svc.Get(context.Background())
	if !errors.Is(e, ErrCatalogUnavailable) || time.Since(start) < 9*time.Second || time.Since(start) > 12*time.Second || s.Meta.Freshness != "unknown" {
		t.Fatal(s.Meta, e, time.Since(start))
	}
}

func TestCatalogUnassignedControllerKeepsOtherCardWhenEveryEntityOverrides(t *testing.T) {
	reg := RegistrySnapshot{Areas: []RegistryArea{{ID: "a", Name: "客厅"}}, Devices: []RegistryDevice{{ID: "d", Name: cp("控制器")}}, Entities: []RegistryEntity{{EntityID: "switch.one", DeviceID: cp("d"), AreaID: cp("a")}}}
	svc := NewCatalogService(context.Background(), &catalogFake{reg: reg})
	defer svc.Close()
	s, _ := svc.Get(context.Background())
	other, e := s.ListDevices("u_other")
	if e != nil || len(other.Devices) != 1 || len(other.Devices[0].Entities) != 0 || other.Devices[0].Membership != "direct" {
		t.Fatal(other, e)
	}
	assigned, e := s.Device("a_YQ", "d_ZA")
	if e != nil || assigned.Device.EntityCount != 1 || assigned.Device.DirectAreaID != nil || assigned.Device.Membership != "entity" {
		t.Fatal(assigned, e)
	}
}
func TestCatalogStateReadFailureDoesNotPublishPartialRegistry(t *testing.T) {
	f := &catalogFake{reg: catalogFixture()}
	svc := NewCatalogService(context.Background(), f)
	defer svc.Close()
	s, _ := svc.Get(context.Background())
	svc.mu.Lock()
	old := time.Now().Add(-31 * time.Second)
	svc.snapshot.Meta.ObservedAt = &old
	svc.mu.Unlock()
	f.stateFail = true
	f.reg.Devices = append(f.reg.Devices, RegistryDevice{ID: "new-device"})
	stale, e := svc.Get(context.Background())
	before, _ := s.ListAreas("")
	after, _ := stale.ListAreas("")
	if e != nil || after.Totals != before.Totals || stale.Meta.Freshness != "stale" {
		t.Fatal(after, e)
	}
}
