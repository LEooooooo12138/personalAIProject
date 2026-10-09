package smarthome

import "context"

func (s *ControlService) Confirm(ctx context.Context, actor ControlActor, id string) (*ControlProposal, error) {
	if e := authorizeControl(ctx); e != nil {
		return nil, e
	}
	p, _, e := s.lookup(ctx, actor, id, false)
	if e != nil {
		return nil, e
	}
	lock := s.entityLock(p.EntityID)
	lock.Lock()
	defer lock.Unlock()
	p, changed, e := s.lookup(ctx, actor, id, false)
	if e != nil {
		return nil, e
	}
	if p.Status == "expired" {
		return p, ErrControlExpired
	}
	if p.Status != "pending" {
		return p, ErrControlConflict
	}
	if p.PolicyRevision != s.revision {
		return p, ErrControlConflict
	}
	target, e := s.ValidateControlTarget(ctx, actor, p.EntityID, p.Action)
	if e != nil {
		return p, e
	}
	current, st, e := s.read(ctx, p.EntityID, target.Name)
	if e != nil {
		return p, e
	}
	if p.Before == nil || current.State != p.Before.State || (!changed.IsZero() && !st.LastChanged.Equal(changed)) {
		return p, ErrControlConflict
	}
	if !s.now().Before(p.ExpiresAt) {
		return p, ErrControlExpired
	}
	if e = ctx.Err(); e != nil {
		return p, e
	}
	p.Status = "executing"
	if e = s.store.Update(ctx, id, "pending", *p); e != nil {
		return nil, e
	}
	if e = authorizeControl(ctx); e != nil {
		p.Status = "failed"
		p.ErrorCode = "control_authorization_changed"
		s.finish(p)
		return p, e
	}
	if e = s.client.CallService(ctx, target.Domain, p.Action, map[string]interface{}{"entity_id": p.EntityID}); e != nil {
		p.Status = "unknown"
		p.ErrorCode = HAErrorCode(e)
		s.finish(p)
		return p, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, s.config.ReadbackTimeout)
	defer cancel()
	deadline := s.now().Add(s.config.ReadbackTimeout)
	p.Status = "unknown"
	p.ErrorCode = "control_readback_timeout"
	for {
		after, _, readErr := s.read(readCtx, p.EntityID, p.Name)
		if readErr == nil {
			p.After = after
			if after.State == expectedControlState(p.Action) {
				p.Status = "succeeded"
				p.ErrorCode = ""
				break
			}
		}
		remaining := deadline.Sub(s.now())
		if remaining <= 0 || readCtx.Err() != nil {
			break
		}
		pause := s.config.ReadbackInterval
		if pause > remaining {
			pause = remaining
		}
		if s.wait(readCtx, pause) != nil {
			break
		}
	}
	s.finish(p)
	return p, nil
}

// finish preserves the on-disk executing marker on save failure and marks memory unknown.
func (s *ControlService) finish(p *ControlProposal) {
	if e := s.store.Update(context.Background(), p.ID, "executing", *p); e != nil {
		p.Status = "unknown"
		p.ErrorCode = "control_store_unavailable"
		s.store.mu.Lock()
		key, r, ok := s.store.findLocked(p.ID)
		if ok {
			r.Outcome.Proposal = cloneControlOutcome(ProposalOutcome{Proposal: p}).Proposal
			s.store.records[key] = r
		}
		s.store.mu.Unlock()
	}
}

// Reconcile observes current state only; unknown remains unknown because a GET cannot prove causality.
func (s *ControlService) Reconcile(ctx context.Context, actor ControlActor, id string) (*ControlProposal, error) {
	p, _, e := s.lookup(ctx, actor, id, false)
	if e != nil {
		return nil, e
	}
	if p.Status != "unknown" {
		return p, ErrControlConflict
	}
	after, _, e := s.read(ctx, p.EntityID, p.Name)
	if e != nil {
		return p, e
	}
	p.After = after
	if e = s.store.Update(ctx, id, "unknown", *p); e != nil {
		return p, e
	}
	return p, nil
}
