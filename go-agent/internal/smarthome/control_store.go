package smarthome

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type controlRecord struct {
	Actor             ControlActor    `json:"actor"`
	RequestID         string          `json:"request_id"`
	Intent            ControlIntent   `json:"intent"`
	CreatedAt         time.Time       `json:"created_at"`
	BeforeLastChanged time.Time       `json:"before_last_changed"`
	Outcome           ProposalOutcome `json:"outcome"`
}
type controlStoreFile struct {
	Version int                      `json:"version"`
	Records map[string]controlRecord `json:"records"`
}

// ControlStore is a single-process atomic snapshot store. Never share its directory between processes.
type ControlStore struct {
	mu        sync.Mutex
	path      string
	records   map[string]controlRecord
	max       int
	retention time.Duration
	now       func() time.Time
}

func newControlStore(dataDir string, c ControlConfig, now func() time.Time) (*ControlStore, error) {
	if dataDir == "" {
		return nil, ErrControlInvalid
	}
	dir := filepath.Join(dataDir, "control")
	if st, e := os.Lstat(dir); e == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return nil, ErrControlUnavailable
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, ErrControlUnavailable
	}
	s := &ControlStore{path: filepath.Join(dir, "proposals.json"), records: map[string]controlRecord{}, max: c.MaxRecords, retention: c.Retention, now: now}
	st, e := os.Lstat(s.path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil || !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return nil, ErrControlUnavailable
	}
	raw, e := os.ReadFile(s.path)
	if e != nil {
		return nil, ErrControlUnavailable
	}
	var disk controlStoreFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&disk) != nil || disk.Version != 1 || disk.Records == nil || len(disk.Records) > 10000 {
		return nil, ErrControlUnavailable
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, ErrControlUnavailable
	}
	changed := false
	ids := map[string]bool{}
	for key, r := range disk.Records {
		if key != controlRequestKey(r.Actor, r.RequestID) || r.Actor.UserID == "" || r.Actor.SessionID == "" || r.RequestID == "" || r.CreatedAt.IsZero() {
			return nil, ErrControlUnavailable
		}
		p := r.Outcome.Proposal
		if r.Outcome.Kind == "proposal" {
			if p == nil || r.Outcome.DeviceResult != nil || p.ID == "" || ids[p.ID] || p.SessionID != r.Actor.SessionID || p.RequestID != r.RequestID || p.EntityID != r.Intent.EntityID || p.Action != r.Intent.Action || p.Before == nil || p.Before.EntityID != p.EntityID || p.PolicyRevision == "" || !p.ExpiresAt.After(p.CreatedAt) || !validControlStatus(p.Status) {
				return nil, ErrControlUnavailable
			}
			ids[p.ID] = true
			p.OwnerUserID = r.Actor.UserID
			if p.Status == "executing" {
				p.Status = "unknown"
				p.ErrorCode = "control_interrupted"
				changed = true
			}
		} else if r.Outcome.Kind != "already_satisfied" || p != nil || r.Outcome.DeviceResult == nil {
			return nil, ErrControlUnavailable
		}
		disk.Records[key] = r
	}
	s.records = disk.Records
	if changed {
		if e = s.persist(s.records); e != nil {
			return nil, e
		}
	}
	return s, nil
}
func validControlStatus(v string) bool {
	switch v {
	case "pending", "executing", "succeeded", "failed", "unknown", "cancelled", "expired":
		return true
	}
	return false
}
func controlRequestKey(a ControlActor, r string) string {
	raw, _ := json.Marshal([]string{a.UserID, a.SessionID, r})
	return string(raw)
}
func cloneControlOutcome(v ProposalOutcome) ProposalOutcome {
	if v.Proposal != nil {
		p := *v.Proposal
		p.Before = clonePtr(p.Before)
		p.After = clonePtr(p.After)
		v.Proposal = &p
	}
	v.DeviceResult = clonePtr(v.DeviceResult)
	return v
}
func (s *ControlStore) persist(records map[string]controlRecord) error {
	raw, e := json.Marshal(controlStoreFile{Version: 1, Records: records})
	if e != nil || len(raw) > 32<<20 {
		return ErrControlUnavailable
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".control-*.tmp")
	if e != nil {
		return ErrControlUnavailable
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(raw)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return ErrControlUnavailable
	}
	if e = os.Rename(tmp, s.path); e != nil {
		return ErrControlUnavailable
	}
	return nil
}
func (s *ControlStore) cloneRecords() map[string]controlRecord {
	v := make(map[string]controlRecord, len(s.records))
	for k, r := range s.records {
		r.Outcome = cloneControlOutcome(r.Outcome)
		v[k] = r
	}
	return v
}
func (s *ControlStore) findLocked(id string) (string, controlRecord, bool) {
	for k, r := range s.records {
		if r.Outcome.Proposal != nil && r.Outcome.Proposal.ID == id {
			return k, r, true
		}
	}
	return "", controlRecord{}, false
}
func (s *ControlStore) Update(ctx context.Context, id, expectedStatus string, next ControlProposal) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(id, expectedStatus, next)
}
func (s *ControlStore) updateLocked(id, expected string, next ControlProposal) error {
	k, r, ok := s.findLocked(id)
	if !ok {
		return ErrControlNotFound
	}
	p := r.Outcome.Proposal
	if p.Status != expected {
		return ErrControlConflict
	}
	if next.Name != p.Name || next.AreaName != p.AreaName || !sameControlObservation(next.Before, p.Before) || !validControlTransition(expected, next.Status) || next.ID != p.ID || next.EntityID != p.EntityID || next.Action != p.Action || next.PolicyRevision != p.PolicyRevision || next.OwnerUserID != p.OwnerUserID || next.SessionID != p.SessionID || next.RequestID != p.RequestID || !next.CreatedAt.Equal(p.CreatedAt) || !next.ExpiresAt.Equal(p.ExpiresAt) {
		return ErrControlConflict
	}
	v := s.cloneRecords()
	r.Outcome.Proposal = cloneControlOutcome(ProposalOutcome{Proposal: &next}).Proposal
	v[k] = r
	if e := s.persist(v); e != nil {
		return e
	}
	s.records = v
	return nil
}
func (s *ControlStore) pruneLocked(v map[string]controlRecord) {
	cutoff := s.now().Add(-s.retention)
	for k, r := range v {
		if !r.CreatedAt.Before(cutoff) {
			continue
		}
		p := r.Outcome.Proposal
		if p == nil || p.Status == "succeeded" || p.Status == "failed" || p.Status == "cancelled" || p.Status == "expired" {
			delete(v, k)
		}
	}
}

func sameControlObservation(a, b *DeviceQueryResult) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.EntityID == b.EntityID && a.Name == b.Name && a.State == b.State && a.ObservedAt.Equal(b.ObservedAt)
}
func validControlTransition(from, to string) bool {
	switch from {
	case "pending":
		return to == "executing" || to == "cancelled" || to == "expired"
	case "executing":
		return to == "succeeded" || to == "failed" || to == "unknown"
	case "unknown":
		return to == "unknown"
	}
	return false
}
