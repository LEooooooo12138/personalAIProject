package smarthome

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type controlTestCatalog struct{ snap CatalogSnapshot }

func (c *controlTestCatalog) Get(context.Context) (CatalogSnapshot, error) { return c.snap, nil }

type controlTestHA struct {
	mu        sync.Mutex
	state     EntityState
	writes    int
	postErr   error
	unchanged bool
	afterPost func()
}

func (h *controlTestHA) GetState(_ context.Context, id string) (*EntityState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v := h.state
	v.EntityID = id
	return &v, nil
}
func (h *controlTestHA) CallService(_ context.Context, domain, action string, data map[string]interface{}) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.writes++
	if data["entity_id"] != h.state.EntityID || domain != "light" {
		return errors.New("wrong payload")
	}
	if !h.unchanged {
		if action == "turn_on" {
			h.state.State = "on"
		} else {
			h.state.State = "off"
		}
	}
	if h.afterPost != nil {
		h.afterPost()
	}
	return h.postErr
}
func controlFixture(t *testing.T) (*ControlService, *controlTestHA, *controlTestCatalog, *time.Time, string) {
	t.Helper()
	now := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	cat := &controlTestCatalog{CatalogSnapshot{Meta: CatalogMeta{Freshness: "fresh", Connection: "connected", ObservedAt: &now}, areas: map[string]AreaRef{"a": {ID: "a", Name: "room"}}, devices: map[string]map[string]DeviceView{"a": {"d": {Entities: []EntityView{{EntityID: "light.test", Name: "lamp", Domain: "light"}, {EntityID: "sensor.temp", Name: "temperature", Domain: "sensor"}}}}}}}
	h := &controlTestHA{state: EntityState{EntityID: "light.test", State: "off", LastChanged: now.Add(-time.Minute)}}
	dir := t.TempDir()
	cfg := ControlConfig{Targets: []ControlTarget{{EntityID: "light.test", Name: "lamp", AreaName: "room", AllowedActions: []string{"turn_on", "turn_off"}, LoadLocationVerified: true}}}
	s, e := NewControlService(cfg, cat, h, dir, WithControlClock(func() time.Time { return now }, func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }))
	if e != nil {
		t.Fatal(e)
	}
	return s, h, cat, &now, dir
}
func controlAuth() context.Context {
	return WithControlAuthorization(context.Background(), func(context.Context) error { return nil })
}

var controlOwner = ControlActor{UserID: "alice", SessionID: "chat1"}

func proposeControl(t *testing.T, s *ControlService, key string) *ControlProposal {
	t.Helper()
	o, e := s.Propose(controlAuth(), controlOwner, key, ControlIntent{Kind: "on_off", EntityID: "light.test", Action: "turn_on"})
	if e != nil {
		t.Fatal(e)
	}
	return o.Proposal
}
func TestControlPolicyAndEmptyQuery(t *testing.T) {
	s, h, cat, _, _ := controlFixture(t)
	if _, e := s.Propose(context.Background(), controlOwner, "noauth", ControlIntent{Kind: "on_off", EntityID: "light.test", Action: "turn_on"}); !errors.Is(e, ErrControlForbidden) {
		t.Fatal(e)
	}
	for _, v := range []ControlIntent{{Kind: "on_off", EntityID: "light.fake", Action: "turn_on"}, {Kind: "on_off", EntityID: "sensor.temp", Action: "turn_on"}, {Kind: "on_off", EntityID: "light.test", Action: "toggle"}} {
		if _, e := s.Propose(controlAuth(), controlOwner, "invalid", v); e == nil {
			t.Fatal("unsafe intent")
		}
	}
	empty, e := NewControlService(ControlConfig{}, cat, h, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	q, c, e := empty.Candidates(context.Background(), "")
	if e != nil || len(q) != 2 || len(c) != 0 {
		t.Fatalf("%v %v %v", q, c, e)
	}
	v, e := empty.Query(context.Background(), "sensor.temp")
	if e != nil || v.ObservedAt.IsZero() {
		t.Fatalf("%v %v", v, e)
	}
	cat.snap.Meta.Freshness = "stale"
	if _, e = s.Propose(controlAuth(), controlOwner, "stale", ControlIntent{Kind: "on_off", EntityID: "light.test", Action: "turn_on"}); !errors.Is(e, ErrControlUnavailable) {
		t.Fatal(e)
	}
	if h.writes != 0 {
		t.Fatal("read wrote")
	}
	for _, target := range []ControlTarget{{EntityID: "light.test", Name: "lamp", AllowedActions: []string{"turn_on"}}, {EntityID: "lock.test", Name: "lock", AllowedActions: []string{"turn_on"}, LoadLocationVerified: true}, {EntityID: "light.test", Name: "lamp", AllowedActions: []string{"toggle"}, LoadLocationVerified: true}} {
		if _, e = NewControlService(ControlConfig{Targets: []ControlTarget{target}}, cat, h, t.TempDir()); e == nil {
			t.Fatal("invalid policy")
		}
	}
}
func TestProposalIsImmutableOwnedAndExpiring(t *testing.T) {
	s, h, _, now, _ := controlFixture(t)
	p := proposeControl(t, s, "req")
	if p.ID == "" || p.Status != "pending" || p.ExpiresAt.Sub(p.CreatedAt) != 120*time.Second {
		t.Fatalf("%+v", p)
	}
	p.Action = "turn_off"
	p2 := proposeControl(t, s, "req")
	if p2.ID != p.ID || p2.Action != "turn_on" {
		t.Fatal("mutable/replay")
	}
	for _, actor := range []ControlActor{{UserID: "bob", SessionID: "chat1"}, {UserID: "alice", SessionID: "chat2"}} {
		if _, e := s.Get(context.Background(), actor, p.ID); !errors.Is(e, ErrControlNotFound) {
			t.Fatal(e)
		}
	}
	if _, e := s.Propose(controlAuth(), controlOwner, "req", ControlIntent{Kind: "on_off", EntityID: "light.test", Action: "turn_off"}); !errors.Is(e, ErrControlConflict) {
		t.Fatal(e)
	}
	*now = now.Add(120 * time.Second)
	got, e := s.Get(context.Background(), controlOwner, p.ID)
	if e != nil || got.Status != "expired" {
		t.Fatalf("%+v %v", got, e)
	}
	if h.writes != 0 {
		t.Fatal("write")
	}
}
func TestProposalAlreadySatisfiedHasNoProposal(t *testing.T) {
	s, h, _, _, _ := controlFixture(t)
	h.state.State = "on"
	o, e := s.Propose(controlAuth(), controlOwner, "already", ControlIntent{Kind: "on_off", EntityID: "light.test", Action: "turn_on"})
	if e != nil || o.Kind != "already_satisfied" || o.Proposal != nil || o.DeviceResult.State != "on" {
		t.Fatalf("%+v %v", o, e)
	}
	h.state.State = "off"
	again, e := s.Propose(controlAuth(), controlOwner, "already", ControlIntent{Kind: "on_off", EntityID: "light.test", Action: "turn_on"})
	if e != nil || again.Kind != "already_satisfied" {
		t.Fatalf("%v %v", again, e)
	}
}
func TestConfirmPersistsBeforeWriteAndSendsOnce(t *testing.T) {
	s, h, _, _, _ := controlFixture(t)
	p := proposeControl(t, s, "one")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.Confirm(controlAuth(), controlOwner, p.ID) }()
	}
	wg.Wait()
	got, e := s.Get(context.Background(), controlOwner, p.ID)
	if e != nil || got.Status != "succeeded" || got.After.State != "on" || h.writes != 1 {
		t.Fatalf("%+v %v writes %d", got, e, h.writes)
	}
}
func TestUnknownWriteNeverReplays(t *testing.T) {
	s, h, cat, now, dir := controlFixture(t)
	h.postErr = context.DeadlineExceeded
	p := proposeControl(t, s, "timeout")
	got, e := s.Confirm(controlAuth(), controlOwner, p.ID)
	if e != nil || got.Status != "unknown" {
		t.Fatalf("%+v %v", got, e)
	}
	if _, e = s.Confirm(controlAuth(), controlOwner, p.ID); !errors.Is(e, ErrControlConflict) {
		t.Fatal(e)
	}
	got, e = s.Reconcile(context.Background(), controlOwner, p.ID)
	if e != nil || got.Status != "unknown" || got.After.State != "on" {
		t.Fatalf("%+v %v", got, e)
	}
	restored, e := NewControlService(s.config, cat, h, dir, WithControlClock(func() time.Time { return *now }, nil))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = restored.Confirm(controlAuth(), controlOwner, p.ID); !errors.Is(e, ErrControlConflict) || h.writes != 1 {
		t.Fatalf("replay %v %d", e, h.writes)
	}
}
func TestControlReadbackSeparatesAcceptedFromSucceeded(t *testing.T) {
	s, h, _, now, _ := controlFixture(t)
	h.unchanged = true
	p := proposeControl(t, s, "unchanged")
	got, e := s.Confirm(controlAuth(), controlOwner, p.ID)
	if e != nil || got.Status != "unknown" || now.Sub(p.CreatedAt) != 10*time.Second || h.writes != 1 {
		t.Fatalf("%+v %v %v", got, e, now.Sub(p.CreatedAt))
	}
}
func TestRevokedOrChangedProposalNeverWrites(t *testing.T) {
	for _, kind := range []string{"revoked", "changed", "timestamp", "expired", "removed", "cancelled", "late_revoked"} {
		t.Run(kind, func(t *testing.T) {
			s, h, cat, now, _ := controlFixture(t)
			p := proposeControl(t, s, kind)
			ctx := controlAuth()
			switch kind {
			case "revoked":
				ctx = WithControlAuthorization(ctx, func(context.Context) error { return errors.New("revoked") })
			case "changed":
				h.state.State = "on"
			case "timestamp":
				h.state.LastChanged = h.state.LastChanged.Add(time.Second)
			case "expired":
				*now = now.Add(121 * time.Second)
			case "removed":
				cat.snap.devices["a"]["d"] = DeviceView{}
			case "cancelled":
				s.Cancel(ctx, controlOwner, p.ID)
			case "late_revoked":
				checks := 0
				ctx = WithControlAuthorization(ctx, func(context.Context) error {
					checks++
					if checks > 1 {
						return errors.New("revoked")
					}
					return nil
				})
			}
			if _, e := s.Confirm(ctx, controlOwner, p.ID); e == nil {
				t.Fatal("unsafe confirmation")
			}
			if h.writes != 0 {
				t.Fatal("unsafe write")
			}
		})
	}
}
func TestControlStoreCrashRecoveryAndBounds(t *testing.T) {
	s, h, cat, now, dir := controlFixture(t)
	p := proposeControl(t, s, "crash")
	p.Status = "executing"
	if e := s.store.Update(context.Background(), p.ID, "pending", *p); e != nil {
		t.Fatal(e)
	}
	restored, e := NewControlService(s.config, cat, h, dir, WithControlClock(func() time.Time { return *now }, nil))
	if e != nil {
		t.Fatal(e)
	}
	got, e := restored.Get(context.Background(), controlOwner, p.ID)
	if e != nil || got.Status != "unknown" {
		t.Fatalf("%+v %v", got, e)
	}
	os.WriteFile(filepath.Join(dir, "control", "proposals.json"), []byte("broken"), 0600)
	if _, e = NewControlService(s.config, cat, h, dir); e == nil {
		t.Fatal("corruption")
	}
	cfg := s.config
	cfg.MaxRecords = 1
	small, e := NewControlService(cfg, cat, h, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	proposeControl(t, small, "first")
	if _, e = small.Propose(controlAuth(), controlOwner, "second", ControlIntent{Kind: "on_off", EntityID: "light.test", Action: "turn_on"}); !errors.Is(e, ErrControlCapacity) {
		t.Fatal(e)
	}
}
func TestControlWritePersistenceFailuresNeverReplay(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			s, h, cat, _, dir := controlFixture(t)
			p := proposeControl(t, s, "disk")
			path := filepath.Join(dir, "control", "proposals.json")
			breakDisk := func() { os.Rename(path, path+".backup"); os.Mkdir(path, 0700) }
			if after {
				h.afterPost = breakDisk
			} else {
				breakDisk()
			}
			got, e := s.Confirm(controlAuth(), controlOwner, p.ID)
			if after {
				if e != nil || got.Status != "unknown" || h.writes != 1 {
					t.Fatalf("%+v %v %d", got, e, h.writes)
				}
				if _, e = s.Confirm(controlAuth(), controlOwner, p.ID); !errors.Is(e, ErrControlConflict) {
					t.Fatal(e)
				}
				os.Remove(path)
				os.Rename(path+".backup", path)
				restored, e := NewControlService(s.config, cat, h, dir)
				if e != nil {
					t.Fatal(e)
				}
				got, _ = restored.Get(context.Background(), controlOwner, p.ID)
				if got.Status != "unknown" {
					t.Fatal("executable")
				}
			} else if e == nil || h.writes != 0 {
				t.Fatalf("%v %d", e, h.writes)
			}
		})
	}
}

func TestControlStoreRejectsMutationAndInvalidTransition(t *testing.T) {
	s, _, _, _, _ := controlFixture(t)
	p := proposeControl(t, s, "immutable")
	p.Before.State = "on"
	if e := s.store.Update(context.Background(), p.ID, "pending", *p); !errors.Is(e, ErrControlConflict) {
		t.Fatalf("mutable before accepted: %v", e)
	}
	p = proposeControl(t, s, "immutable")
	p.Status = "succeeded"
	if e := s.store.Update(context.Background(), p.ID, "pending", *p); !errors.Is(e, ErrControlConflict) {
		t.Fatalf("pending skipped execution: %v", e)
	}
}
func TestControlCandidateBoundsAndDuplicateNames(t *testing.T) {
	s, h, cat, _, _ := controlFixture(t)
	for i := 0; i < 21; i++ {
		id := fmt.Sprintf("sensor.room_%d", i)
		d := cat.snap.devices["a"]["d"]
		d.Entities = append(d.Entities, EntityView{EntityID: id, Name: "duplicate", Domain: "sensor"})
		cat.snap.devices["a"]["d"] = d
	}
	if _, _, e := s.Candidates(context.Background(), ""); !errors.Is(e, ErrControlUnsupported) {
		t.Fatalf("silently truncated: %v", e)
	}
	q, c, e := s.Candidates(context.Background(), "打开 lamp")
	if e != nil || len(q) != 1 || len(c) != 1 {
		t.Fatalf("natural content %v %v %v", q, c, e)
	}
	if _, _, e = s.Candidates(context.Background(), "duplicate"); !errors.Is(e, ErrControlUnsupported) {
		t.Fatal(e)
	}
	if h.writes != 0 {
		t.Fatal("candidate writes")
	}
}
func TestControlConcurrentEntityRejectsSecondOldSnapshot(t *testing.T) {
	s, h, _, _, _ := controlFixture(t)
	p1 := proposeControl(t, s, "first")
	p2 := proposeControl(t, s, "second")
	if _, e := s.Confirm(controlAuth(), controlOwner, p1.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Confirm(controlAuth(), controlOwner, p2.ID); !errors.Is(e, ErrControlConflict) {
		t.Fatalf("old state accepted %v", e)
	}
	if h.writes != 1 {
		t.Fatal("second write")
	}
}
func TestControlSessionIdempotencyAndRetention(t *testing.T) {
	s, h, cat, now, dir := controlFixture(t)
	first := proposeControl(t, s, "same")
	other := ControlActor{UserID: "alice", SessionID: "chat2"}
	o, e := s.Propose(controlAuth(), other, "same", ControlIntent{Kind: "on_off", EntityID: "light.test", Action: "turn_on"})
	if e != nil || o.Proposal.ID == first.ID {
		t.Fatalf("session collision %v %v", o, e)
	}
	got, e := s.Get(context.Background(), ControlActor{UserID: "alice"}, first.ID)
	if e != nil || got.SessionID != "chat1" {
		t.Fatalf("owner lookup %v %v", got, e)
	}
	s.Cancel(context.Background(), controlOwner, first.ID)
	*now = now.Add(31 * 24 * time.Hour)
	cat.snap.Meta.ObservedAt = now
	proposeControl(t, s, "new")
	if _, e = s.Get(context.Background(), controlOwner, first.ID); !errors.Is(e, ErrControlNotFound) {
		t.Fatalf("terminal not pruned %v", e)
	}
	restored, e := NewControlService(s.config, cat, h, dir, WithControlClock(func() time.Time { return *now }, nil))
	if e != nil {
		t.Fatal(e)
	}
	got, e = restored.Get(context.Background(), other, o.Proposal.ID)
	if e != nil || got.Status != "expired" {
		t.Fatalf("active record removed %v %v", got, e)
	}
}
func TestControlPolicyRevisionAndPostErrorsNeverWriteAgain(t *testing.T) {
	for _, postErr := range []error{newHAError("ha_auth_required"), newHAError("ha_unavailable"), context.Canceled} {
		s, h, cat, now, dir := controlFixture(t)
		h.postErr = postErr
		p := proposeControl(t, s, "error")
		got, e := s.Confirm(controlAuth(), controlOwner, p.ID)
		if e != nil || got.Status != "unknown" {
			t.Fatalf("%v %v", got, e)
		}
		*now = now.Add(31 * 24 * time.Hour)
		cat.snap.Meta.ObservedAt = now
		proposeControl(t, s, "other")
		got, e = s.Get(context.Background(), controlOwner, p.ID)
		if e != nil || got.Status != "unknown" {
			t.Fatalf("unknown removed %v %v", got, e)
		}
		restored, e := NewControlService(s.config, cat, h, dir)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = restored.Confirm(controlAuth(), controlOwner, p.ID); !errors.Is(e, ErrControlConflict) || h.writes != 1 {
			t.Fatalf("replay %v %d", e, h.writes)
		}
	}
	s, h, cat, now, dir := controlFixture(t)
	p := proposeControl(t, s, "policy")
	cfg := s.config
	cfg.Targets = append([]ControlTarget{}, cfg.Targets...)
	cfg.Targets[0].AllowedActions = []string{"turn_on"}
	changed, e := NewControlService(cfg, cat, h, dir, WithControlClock(func() time.Time { return *now }, nil))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = changed.Confirm(controlAuth(), controlOwner, p.ID); !errors.Is(e, ErrControlConflict) || h.writes != 0 {
		t.Fatalf("policy %v %d", e, h.writes)
	}
}
func TestControlCancelWaitingConfirmation(t *testing.T) {
	s, h, _, _, _ := controlFixture(t)
	p := proposeControl(t, s, "cancel-wait")
	lock := s.entityLock(p.EntityID)
	lock.Lock()
	started := make(chan struct{})
	ctx := WithControlAuthorization(context.Background(), func(context.Context) error { close(started); return nil })
	done := make(chan error, 1)
	go func() { _, e := s.Confirm(ctx, controlOwner, p.ID); done <- e }()
	<-started
	if _, e := s.Cancel(context.Background(), controlOwner, p.ID); e != nil {
		t.Fatal(e)
	}
	lock.Unlock()
	if e := <-done; !errors.Is(e, ErrControlConflict) {
		t.Fatal(e)
	}
	if h.writes != 0 {
		t.Fatal("cancelled queued write")
	}
}
func TestControlConfigRejectsDuplicateAndMalformedTargets(t *testing.T) {
	s, h, cat, _, _ := controlFixture(t)
	for _, cfg := range []ControlConfig{{Targets: []ControlTarget{s.config.Targets[0], s.config.Targets[0]}}, {ProposalTTL: -time.Second}, {MaxRecords: 10001}, {ReadbackTimeout: time.Second, ReadbackInterval: 2 * time.Second}, {Targets: []ControlTarget{{EntityID: "light.test/path", Name: "lamp", AreaName: "room", AllowedActions: []string{"turn_on"}, LoadLocationVerified: true}}}} {
		if _, e := NewControlService(cfg, cat, h, t.TempDir()); !errors.Is(e, ErrControlInvalid) {
			t.Fatalf("config accepted %v", e)
		}
	}
}
