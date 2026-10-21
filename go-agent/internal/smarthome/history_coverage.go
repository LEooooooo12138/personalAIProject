package smarthome

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// TimeWindow is a half-open interval [Start, End). Stored instants are UTC.
type TimeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
type HistoryQuery struct {
	EntityIDs []string   `json:"entity_ids"`
	Window    TimeWindow `json:"window"`
}

// HistoryReceipt records a request outcome, never a promise of recorder retention.
// SaveHistoryBatch generates ID, SchemaVersion and CompletedAt on the server.
type HistoryReceipt struct {
	// AcquisitionScope isolates a resumable backfill run's staging/publication.
	// Empty retains the legacy/live window; nonempty is a server-generated run ID.
	AcquisitionScope    string     `json:"acquisition_scope,omitempty"`
	SchemaVersion       int        `json:"schema_version"`
	ID                  string     `json:"id"`
	Window              TimeWindow `json:"window"`
	RequestedEntityIDs  []string   `json:"requested_entity_ids"`
	SuccessfulEntityIDs []string   `json:"successful_entity_ids"`
	CompletedAt         time.Time  `json:"completed_at"`
	Source              string     `json:"source"` // live or backfill
	ErrorCode           string     `json:"error_code,omitempty"`
}
type EntityHistoryCoverage struct {
	RequestedUnionDuration  time.Duration `json:"requested_union_duration"`
	SuccessfulUnionDuration time.Duration `json:"successful_union_duration"`
	ValidStateCount         int           `json:"valid_state_count"`
	ChangeCount             int           `json:"change_count"` // verified transitions, not initial or repeated states
	Gaps                    []TimeWindow  `json:"gaps"`         // gaps in successful requests, not proof of missing recorder data
	Retention               string        `json:"retention"`    // unknown until a reliable capability source is available
}
type HistoryCoverage struct {
	RequestedUnionDuration  time.Duration                    `json:"requested_union_duration"`
	SuccessfulUnionDuration time.Duration                    `json:"successful_union_duration"`
	CorruptFiles            int                              `json:"corrupt_files"`
	Entities                map[string]EntityHistoryCoverage `json:"entities"`
	Receipts                []HistoryReceipt                 `json:"receipts"`
}
type HistoryReadResult struct {
	Entries []HistoryEntry `json:"entries"`
	// BaselineEntries retains every observation at the latest preceding instant per
	// entity, including conflicting states; it must not count as an in-window event.
	BaselineEntries []HistoryEntry  `json:"baseline_entries"`
	Coverage        HistoryCoverage `json:"coverage"`
}

// A single atomic envelope holds published data, staged data and request facts.
// Failed/incomplete windows never expose PendingEntries to either reader.
type historyWindowFile struct {
	AcquisitionScope string           `json:"acquisition_scope,omitempty"`
	SchemaVersion    int              `json:"schema_version"`
	Window           TimeWindow       `json:"window"`
	Entries          []HistoryEntry   `json:"entries"`
	PendingEntries   []HistoryEntry   `json:"pending_entries,omitempty"`
	Receipts         []HistoryReceipt `json:"receipts"`
}

func validateHistoryWindow(w TimeWindow) error {
	if w.Start.IsZero() || !w.End.After(w.Start) {
		return fmt.Errorf("invalid history window")
	}
	return nil
}

// HA 2026.7.2 core.VALID_ENTITY_ID permits decimal-digit-leading slugs.
// Keep this read/storage validator separate from the restricted device-write
// entityIDPattern; accepting observed history grants no execution capability.
var historyEntityIDPattern = regexp.MustCompile(`^[a-z\p{Nd}]+(?:_[a-z\p{Nd}]+)*\.[a-z\p{Nd}]+(?:_[a-z\p{Nd}]+)*$`)

func validateHistoryIDs(ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("history requires explicit entity IDs")
	}
	for _, id := range ids {
		if !historyEntityIDPattern.MatchString(id) {
			return fmt.Errorf("invalid history entity ID")
		}
	}
	return nil
}
func historyErrorCodeValid(code string) bool {
	switch code {
	case "", "cancelled", "ha_auth_required", "ha_forbidden", "ha_timeout", "ha_unavailable", "ha_invalid_response":
		return true
	}
	return false
}
func validateHistoryScope(scope string) error {
	if scope == "" {
		return nil
	}
	raw, err := hex.DecodeString(scope)
	if err != nil || len(raw) != 16 || hex.EncodeToString(raw) != scope {
		return fmt.Errorf("invalid history acquisition scope")
	}
	return nil
}
func validateHistoryReceipt(r HistoryReceipt, persisted bool) error {
	if err := validateHistoryScope(r.AcquisitionScope); err != nil {
		return err
	}
	if r.AcquisitionScope != "" && r.Source != "backfill" {
		return fmt.Errorf("only backfill has an acquisition scope")
	}
	if err := validateHistoryWindow(r.Window); err != nil {
		return err
	}
	if err := validateHistoryIDs(r.RequestedEntityIDs); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, id := range r.RequestedEntityIDs {
		allowed[id] = true
	}
	for _, id := range r.SuccessfulEntityIDs {
		if !allowed[id] {
			return fmt.Errorf("successful entity outside history scope")
		}
	}
	if r.Source != "live" && r.Source != "backfill" {
		return fmt.Errorf("invalid history receipt source")
	}
	if !historyErrorCodeValid(r.ErrorCode) {
		return fmt.Errorf("invalid history error code")
	}
	if len(uniqueEntityIDs(r.SuccessfulEntityIDs)) < len(uniqueEntityIDs(r.RequestedEntityIDs)) && r.ErrorCode == "" {
		return fmt.Errorf("partial history receipt requires error code")
	}
	if persisted && (r.SchemaVersion != 1 || len(r.ID) != 32 || r.CompletedAt.IsZero()) {
		return fmt.Errorf("invalid persisted history receipt")
	}
	return nil
}
func validateHistoryEntries(entries []HistoryEntry) error {
	for _, e := range entries {
		if !historyEntityIDPattern.MatchString(e.EntityID) || e.Timestamp.IsZero() {
			return fmt.Errorf("invalid history entry")
		}
		switch e.ObservationKind {
		case "", "initial", "change":
		default:
			return fmt.Errorf("invalid history observation kind")
		}
	}
	return nil
}
func decodeHistoryFile(data []byte) (historyWindowFile, error) {
	var f historyWindowFile
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") || trimmed == "null" {
		err := json.Unmarshal(data, &f.Entries)
		if err == nil {
			err = validateHistoryEntries(f.Entries)
		}
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, err
	}
	if err := validateHistoryScope(f.AcquisitionScope); err != nil {
		return f, err
	}
	if f.SchemaVersion != 1 {
		return f, fmt.Errorf("unsupported history schema")
	}
	if err := validateHistoryWindow(f.Window); err != nil {
		return f, err
	}
	if err := validateHistoryEntries(f.Entries); err != nil {
		return f, err
	}
	if err := validateHistoryEntries(f.PendingEntries); err != nil {
		return f, err
	}
	seen := map[string]bool{}
	for _, r := range f.Receipts {
		if err := validateHistoryReceipt(r, true); err != nil {
			return f, err
		}
		if r.AcquisitionScope != f.AcquisitionScope || !r.Window.Start.Equal(f.Window.Start) || !r.Window.End.Equal(f.Window.End) || seen[r.ID] {
			return f, fmt.Errorf("invalid history receipt window or ID")
		}
		seen[r.ID] = true
	}
	return f, nil
}
func (s *DeviceStore) historyWindowPath(w TimeWindow) string {
	return s.scopedHistoryWindowPath(w, "")
}
func (s *DeviceStore) scopedHistoryWindowPath(w TimeWindow, scope string) string {
	suffix := ""
	if scope != "" {
		suffix = "-" + scope
	}
	return filepath.Join(s.basePath, "snapshots", fmt.Sprintf("window-%d-%d%s.json", w.Start.UnixNano(), w.End.UnixNano(), suffix))
}
func (s *DeviceStore) readHistoryWindow(w TimeWindow) (historyWindowFile, error) {
	return s.readScopedHistoryWindow(w, "")
}
func (s *DeviceStore) readScopedHistoryWindow(w TimeWindow, scope string) (historyWindowFile, error) {
	if err := validateHistoryScope(scope); err != nil {
		return historyWindowFile{}, err
	}
	data, err := os.ReadFile(s.scopedHistoryWindowPath(w, scope))
	if os.IsNotExist(err) {
		return historyWindowFile{SchemaVersion: 1, Window: w, AcquisitionScope: scope}, nil
	}
	if err != nil {
		return historyWindowFile{}, err
	}
	f, err := decodeHistoryFile(data)
	if err == nil && (f.AcquisitionScope != scope || (f.SchemaVersion != 0 && (!f.Window.Start.Equal(w.Start) || !f.Window.End.Equal(w.End)))) {
		return f, fmt.Errorf("history window mismatch")
	}
	f.SchemaVersion = 1
	f.Window = w
	return f, err
}
func (s *DeviceStore) writeHistoryWindow(f historyWindowFile) error {
	f.Entries = deduplicateHistory(f.Entries)
	f.PendingEntries = deduplicateHistory(f.PendingEntries)
	for i := range f.Entries {
		f.Entries[i].Timestamp = f.Entries[i].Timestamp.UTC()
	}
	for i := range f.PendingEntries {
		f.PendingEntries[i].Timestamp = f.PendingEntries[i].Timestamp.UTC()
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return atomicWrite(s.scopedHistoryWindowPath(f.Window, f.AcquisitionScope), data)
}

// SaveHistoryBatch stages validated events and a server-generated receipt in one
// atomic version. It never publishes staged events or changes the live checkpoint.
// Empty success is request coverage only. Repeated/same-window batches merge.
func (s *DeviceStore) SaveHistoryBatch(receipt HistoryReceipt, entries []HistoryEntry) error {
	if err := validateHistoryReceipt(receipt, false); err != nil {
		return err
	}
	if err := validateHistoryEntries(entries); err != nil {
		return err
	}
	success := map[string]bool{}
	for _, id := range receipt.SuccessfulEntityIDs {
		success[id] = true
	}
	for _, e := range entries {
		if !success[e.EntityID] || !e.Timestamp.Before(receipt.Window.End) {
			return fmt.Errorf("history entry outside successful request")
		}
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	receipt.SchemaVersion = 1
	receipt.ID = hex.EncodeToString(raw)
	receipt.CompletedAt = time.Now().UTC()
	receipt.Window.Start = receipt.Window.Start.UTC()
	receipt.Window.End = receipt.Window.End.UTC()
	receipt.RequestedEntityIDs = uniqueEntityIDs(receipt.RequestedEntityIDs)
	receipt.SuccessfulEntityIDs = uniqueEntityIDs(receipt.SuccessfulEntityIDs)
	entries = append([]HistoryEntry(nil), entries...)
	for i := range entries {
		entries[i].Timestamp = entries[i].Timestamp.UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readScopedHistoryWindow(receipt.Window, receipt.AcquisitionScope)
	if err != nil {
		return err
	}
	f.PendingEntries = append(f.PendingEntries, entries...)
	f.Receipts = append(f.Receipts, receipt)
	return s.writeHistoryWindow(f)
}

// CommitHistoryWindow explicitly publishes staged events without moving the live
// checkpoint. The caller must first finish every intended entity batch.
func (s *DeviceStore) CommitHistoryWindow(start, end time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitHistoryWindow(TimeWindow{Start: start.UTC(), End: end.UTC()}, nil)
}

// CommitScopedHistoryWindow publishes only one backfill acquisition's staged
// data. The caller must finish every intended batch in this scope and window.
// It cannot alter legacy/live pending entries or the live checkpoint.
func (s *DeviceStore) CommitScopedHistoryWindow(scope string, start, end time.Time) error {
	if scope == "" {
		return fmt.Errorf("backfill acquisition scope required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitScopedHistoryWindow(TimeWindow{Start: start.UTC(), End: end.UTC()}, nil, scope)
}
func (s *DeviceStore) commitHistoryWindow(w TimeWindow, entries []HistoryEntry) error {
	return s.commitScopedHistoryWindow(w, entries, "")
}
func (s *DeviceStore) commitScopedHistoryWindow(w TimeWindow, entries []HistoryEntry, scope string) error {
	if err := validateHistoryWindow(w); err != nil {
		return err
	}
	if err := validateHistoryEntries(entries); err != nil {
		return err
	}
	f, err := s.readScopedHistoryWindow(w, scope)
	if err != nil {
		return err
	}
	f.Entries = append(f.Entries, f.PendingEntries...)
	f.Entries = append(f.Entries, entries...)
	f.PendingEntries = nil
	return s.writeHistoryWindow(f)
}

func unionHistoryWindows(windows []TimeWindow) []TimeWindow {
	sort.Slice(windows, func(i, j int) bool { return windows[i].Start.Before(windows[j].Start) })
	var union []TimeWindow
	for _, w := range windows {
		if len(union) == 0 || w.Start.After(union[len(union)-1].End) {
			union = append(union, w)
			continue
		}
		if w.End.After(union[len(union)-1].End) {
			union[len(union)-1].End = w.End
		}
	}
	return union
}
func historyDuration(windows []TimeWindow) time.Duration {
	var d time.Duration
	for _, w := range windows {
		d += w.End.Sub(w.Start)
	}
	return d
}
func clippedHistoryWindow(a, b TimeWindow) (TimeWindow, bool) {
	if a.Start.Before(b.Start) {
		a.Start = b.Start
	}
	if a.End.After(b.End) {
		a.End = b.End
	}
	return a, a.End.After(a.Start)
}
func historyGaps(w TimeWindow, covered []TimeWindow) []TimeWindow {
	var gaps []TimeWindow
	at := w.Start
	for _, c := range covered {
		if c.Start.After(at) {
			gaps = append(gaps, TimeWindow{Start: at, End: c.Start})
		}
		at = c.End
	}
	if at.Before(w.End) {
		gaps = append(gaps, TimeWindow{Start: at, End: w.End})
	}
	return gaps
}

// ReadHistory reads explicit entities in [start,end), reporting corrupt files
// rather than silently treating them as empty history. Unknown retention stays
// unknown for legacy data and successful empty requests alike.
func (s *DeviceStore) ReadHistory(ctx context.Context, q HistoryQuery) (HistoryReadResult, error) {
	var result HistoryReadResult
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validateHistoryWindow(q.Window); err != nil {
		return result, err
	}
	if err := validateHistoryIDs(q.EntityIDs); err != nil {
		return result, err
	}
	ids := uniqueEntityIDs(q.EntityIDs)
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	result.Coverage.Entities = map[string]EntityHistoryCoverage{}
	requested := map[string][]TimeWindow{}
	successful := map[string][]TimeWindow{}
	var all []HistoryEntry
	s.mu.RLock()
	defer s.mu.RUnlock()
	files, err := os.ReadDir(filepath.Join(s.basePath, "snapshots"))
	if err != nil {
		return result, err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return HistoryReadResult{}, err
		}
		if file.IsDir() || file.Name() == "latest.json" || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.basePath, "snapshots", file.Name()))
		if err != nil {
			result.Coverage.CorruptFiles++
			continue
		}
		if err := ctx.Err(); err != nil {
			return HistoryReadResult{}, err
		}
		f, err := decodeHistoryFile(data)
		if err != nil {
			result.Coverage.CorruptFiles++
			continue
		}
		for i, e := range f.Entries {
			if i%256 == 0 {
				if err := ctx.Err(); err != nil {
					return HistoryReadResult{}, err
				}
			}
			if allowed[e.EntityID] && e.Timestamp.Before(q.Window.End) {
				all = append(all, e)
			}
		}
		for _, r := range f.Receipts {
			if err := ctx.Err(); err != nil {
				return HistoryReadResult{}, err
			}
			w, ok := clippedHistoryWindow(r.Window, q.Window)
			if !ok {
				continue
			}
			scoped := r
			scoped.RequestedEntityIDs = nil
			scoped.SuccessfulEntityIDs = nil
			for _, id := range r.RequestedEntityIDs {
				if allowed[id] {
					requested[id] = append(requested[id], w)
					scoped.RequestedEntityIDs = append(scoped.RequestedEntityIDs, id)
				}
			}
			for _, id := range r.SuccessfulEntityIDs {
				if allowed[id] {
					successful[id] = append(successful[id], w)
					scoped.SuccessfulEntityIDs = append(scoped.SuccessfulEntityIDs, id)
				}
			}
			if len(scoped.RequestedEntityIDs) > 0 {
				result.Coverage.Receipts = append(result.Coverage.Receipts, scoped)
			}
		}
	}
	all, err = deduplicateHistoryContext(ctx, all)
	if err != nil {
		return HistoryReadResult{}, err
	}
	latest := map[string]time.Time{}
	for i, e := range all {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return HistoryReadResult{}, err
			}
		}
		if e.Timestamp.Before(q.Window.Start) {
			latest[e.EntityID] = e.Timestamp
		} else {
			result.Entries = append(result.Entries, e)
		}
	}
	for i, e := range all {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return HistoryReadResult{}, err
			}
		}
		if at, ok := latest[e.EntityID]; ok && e.Timestamp.Equal(at) {
			result.BaselineEntries = append(result.BaselineEntries, e)
		}
	}
	var requestedAll, successfulAll []TimeWindow
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return HistoryReadResult{}, err
		}
		req := unionHistoryWindows(requested[id])
		succ := unionHistoryWindows(successful[id])
		result.Coverage.Entities[id] = EntityHistoryCoverage{RequestedUnionDuration: historyDuration(req), SuccessfulUnionDuration: historyDuration(succ), Gaps: historyGaps(q.Window, succ), Retention: "unknown"}
		requestedAll = append(requestedAll, req...)
		successfulAll = append(successfulAll, succ...)
	}
	valid, err := unambiguousHistoryContext(ctx, result.Entries)
	if err != nil {
		return HistoryReadResult{}, err
	}
	for i, e := range valid {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return HistoryReadResult{}, err
			}
		}
		if e.State != "" && e.State != "unknown" && e.State != "unavailable" {
			quality := result.Coverage.Entities[e.EntityID]
			quality.ValidStateCount++
			result.Coverage.Entities[e.EntityID] = quality
		}
	}
	transitions := append(append([]HistoryEntry(nil), result.BaselineEntries...), result.Entries...)
	changes, err := stateTransitionsContext(ctx, transitions)
	if err != nil {
		return HistoryReadResult{}, err
	}
	for i, change := range changes {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return HistoryReadResult{}, err
			}
		}
		if !change.At.Before(q.Window.Start) {
			quality := result.Coverage.Entities[change.EntityID]
			quality.ChangeCount++
			result.Coverage.Entities[change.EntityID] = quality
		}
	}
	result.Coverage.RequestedUnionDuration = historyDuration(unionHistoryWindows(requestedAll))
	result.Coverage.SuccessfulUnionDuration = historyDuration(unionHistoryWindows(successfulAll))
	if err := ctx.Err(); err != nil {
		return HistoryReadResult{}, err
	}
	return result, nil
}

// Discover eligible entities only from published observations inside the request
// or persisted receipt scopes overlapping it. Predecessor observations supply
// continuity in ReadHistory only after eligibility is established here. Pending
// observations never qualify an entity.
func (s *DeviceStore) analysisEntityIDs(ctx context.Context, window TimeWindow) ([]string, int, error) {
	if err := validateHistoryWindow(window); err != nil {
		return nil, 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	files, err := os.ReadDir(filepath.Join(s.basePath, "snapshots"))
	if err != nil {
		return nil, 0, err
	}
	ids := map[string]bool{}
	corrupt := 0
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if f.IsDir() || f.Name() == "latest.json" || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.basePath, "snapshots", f.Name()))
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if err != nil {
			corrupt++
			continue
		}
		record, err := decodeHistoryFile(b)
		if err != nil {
			corrupt++
			continue
		}
		for i, e := range record.Entries {
			if i%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, 0, err
				}
			}
			if !e.Timestamp.Before(window.Start) && e.Timestamp.Before(window.End) && validateHistoryIDs([]string{e.EntityID}) == nil {
				ids[e.EntityID] = true
			}
		}
		for _, r := range record.Receipts {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			if _, overlaps := clippedHistoryWindow(r.Window, window); !overlaps {
				continue
			}
			for i, id := range r.RequestedEntityIDs {
				if i%256 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, 0, err
					}
				}
				ids[id] = true
			}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, corrupt, ctx.Err()
}
