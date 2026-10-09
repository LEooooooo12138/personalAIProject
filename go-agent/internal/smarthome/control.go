package smarthome

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

type ControlService struct {
	config      ControlConfig
	revision    string
	targets     map[string]ControlTarget
	catalog     ControlCatalogProvider
	client      ControlHAClient
	store       *ControlStore
	now         func() time.Time
	wait        func(context.Context, time.Duration) error
	entityMu    sync.Mutex
	entityLocks map[string]*sync.Mutex
}

func NewControlService(config ControlConfig, catalog ControlCatalogProvider, client ControlHAClient, dataDir string, options ...ControlOption) (*ControlService, error) {
	c, rev, e := normalizeControlConfig(config)
	if e != nil {
		return nil, e
	}
	if catalog == nil || client == nil {
		return nil, ErrControlUnavailable
	}
	s := &ControlService{config: c, revision: rev, catalog: catalog, client: client, targets: map[string]ControlTarget{}, entityLocks: map[string]*sync.Mutex{}, now: func() time.Time { return time.Now().UTC() }, wait: func(ctx context.Context, d time.Duration) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}}
	for _, v := range c.Targets {
		s.targets[v.EntityID] = v
	}
	for _, o := range options {
		o(s)
	}
	s.store, e = newControlStore(dataDir, c, s.now)
	if e != nil {
		return nil, e
	}
	return s, nil
}
func (s *ControlService) Query(ctx context.Context, id string) (*DeviceQueryResult, error) {
	snap, e := s.catalog.Get(ctx)
	if e != nil {
		return nil, ErrControlUnavailable
	}
	for _, t := range snap.QueryTargets() {
		if t.EntityID == id {
			v, _, e := s.read(ctx, id, t.Name)
			return v, e
		}
	}
	return nil, ErrControlNotFound
}
func (s *ControlService) read(ctx context.Context, id, name string) (*DeviceQueryResult, *EntityState, error) {
	st, e := s.client.GetState(ctx, id)
	if e != nil {
		return nil, nil, ErrControlUnavailable
	}
	if st == nil || st.EntityID != id || len(st.State) > 256 || strings.ContainsAny(st.State, "\x00\r\n") {
		return nil, nil, ErrControlUnavailable
	}
	return &DeviceQueryResult{EntityID: id, Name: name, State: st.State, ObservedAt: s.now().UTC()}, st, nil
}
func expectedControlState(action string) string {
	if action == "turn_on" {
		return "on"
	}
	return "off"
}
func (s *ControlService) Propose(ctx context.Context, actor ControlActor, requestID string, intent ControlIntent) (*ProposalOutcome, error) {
	if actor.UserID == "" || actor.SessionID == "" || len(actor.UserID) > 256 || len(actor.SessionID) > 256 || requestID == "" || len(requestID) > 256 || intent.Kind != "on_off" {
		return nil, ErrControlInvalid
	}
	if e := authorizeControl(ctx); e != nil {
		return nil, e
	}
	key := controlRequestKey(actor, requestID)
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if r, ok := s.store.records[key]; ok {
		if r.Intent != intent {
			return nil, ErrControlConflict
		}
		o := cloneControlOutcome(r.Outcome)
		if o.Proposal != nil && o.Proposal.Status == "pending" && !s.now().Before(o.Proposal.ExpiresAt) {
			o.Proposal.Status = "expired"
			if e := s.store.updateLocked(o.Proposal.ID, "pending", *o.Proposal); e != nil {
				return nil, e
			}
		}
		return &o, nil
	}
	target, e := s.ValidateControlTarget(ctx, actor, intent.EntityID, intent.Action)
	if e != nil {
		return nil, e
	}
	before, st, e := s.read(ctx, target.EntityID, target.Name)
	if e != nil {
		return nil, e
	}
	if before.State == "unknown" || before.State == "unavailable" || (before.State != "on" && before.State != "off") {
		return nil, ErrControlUnavailable
	}
	now := s.now().UTC()
	r := controlRecord{Actor: actor, RequestID: requestID, Intent: intent, CreatedAt: now, BeforeLastChanged: st.LastChanged}
	if before.State == expectedControlState(intent.Action) {
		r.Outcome = ProposalOutcome{Kind: "already_satisfied", DeviceResult: before}
	} else {
		random := make([]byte, 16)
		if _, e = rand.Read(random); e != nil {
			return nil, ErrControlUnavailable
		}
		r.Outcome = ProposalOutcome{Kind: "proposal", Proposal: &ControlProposal{ID: hex.EncodeToString(random), OwnerUserID: actor.UserID, SessionID: actor.SessionID, RequestID: requestID, EntityID: target.EntityID, Name: target.Name, AreaName: target.AreaName, Action: intent.Action, PolicyRevision: s.revision, Status: "pending", CreatedAt: now, ExpiresAt: now.Add(s.config.ProposalTTL), Before: before}}
	}
	v := s.store.cloneRecords()
	s.store.pruneLocked(v)
	if len(v) >= s.config.MaxRecords {
		return nil, ErrControlCapacity
	}
	v[key] = r
	if e = s.store.persist(v); e != nil {
		return nil, e
	}
	s.store.records = v
	o := cloneControlOutcome(r.Outcome)
	return &o, nil
}
func (s *ControlService) lookup(ctx context.Context, actor ControlActor, id string, ownerOnly bool) (*ControlProposal, time.Time, error) {
	if e := ctx.Err(); e != nil {
		return nil, time.Time{}, e
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	_, r, ok := s.store.findLocked(id)
	if !ok || actor.UserID == "" || r.Actor.UserID != actor.UserID || (!ownerOnly || actor.SessionID != "") && r.Actor.SessionID != actor.SessionID {
		return nil, time.Time{}, ErrControlNotFound
	}
	o := cloneControlOutcome(r.Outcome)
	p := o.Proposal
	if p.Status == "pending" && !s.now().Before(p.ExpiresAt) {
		p.Status = "expired"
		if e := s.store.updateLocked(id, "pending", *p); e != nil {
			return nil, time.Time{}, e
		}
	}
	return p, r.BeforeLastChanged, nil
}
func (s *ControlService) Get(ctx context.Context, actor ControlActor, id string) (*ControlProposal, error) {
	p, _, e := s.lookup(ctx, actor, id, true)
	return p, e
}
func (s *ControlService) Cancel(ctx context.Context, actor ControlActor, id string) (*ControlProposal, error) {
	p, _, e := s.lookup(ctx, actor, id, false)
	if e != nil {
		return nil, e
	}
	if p.Status == "expired" {
		return p, ErrControlExpired
	}
	if p.Status != "pending" {
		return p, ErrControlConflict
	}
	p.Status = "cancelled"
	if e = s.store.Update(ctx, id, "pending", *p); e != nil {
		return nil, e
	}
	return p, nil
}
func (s *ControlService) entityLock(id string) *sync.Mutex {
	s.entityMu.Lock()
	defer s.entityMu.Unlock()
	v := s.entityLocks[id]
	if v == nil {
		v = &sync.Mutex{}
		s.entityLocks[id] = v
	}
	return v
}

// Replay returns only an existing immutable request outcome. A miss never calls HA.
// It is used before chat rollover so retries retain the original session scope.
func (s *ControlService) Replay(ctx context.Context, actor ControlActor, requestID, inputHash string) (*ProposalOutcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if actor.UserID == "" || actor.SessionID == "" || requestID == "" {
		return nil, ErrControlInvalid
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	r, ok := s.store.records[controlRequestKey(actor, requestID)]
	if !ok {
		return nil, nil
	}
	if err := authorizeControl(ctx); err != nil {
		return nil, err
	}
	if inputHash == "" || r.Intent.InputHash != inputHash {
		return nil, ErrControlConflict
	}
	out := cloneControlOutcome(r.Outcome)
	if p := out.Proposal; p != nil && p.Status == "pending" && !s.now().Before(p.ExpiresAt) {
		p.Status = "expired"
		if err := s.store.updateLocked(p.ID, "pending", *p); err != nil {
			return nil, err
		}
	}
	return &out, nil
}
