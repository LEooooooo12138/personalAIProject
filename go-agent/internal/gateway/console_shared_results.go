package gateway

import (
	"errors"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"time"
)

// Explicit sharing grants this immutable original result, not current managed
// targets, installation state, or diagnostic evidence. Keep this DTO closed.
type consoleSharedResult struct {
	ID            string                    `json:"id"`
	CreatedAt     time.Time                 `json:"created_at"`
	Title         string                    `json:"title"`
	TimeZone      string                    `json:"time_zone"`
	Rule          *consoleSuggestionRule    `json:"rule"`
	EntityIDs     []string                  `json:"entity_ids"`
	Entities      []consoleSuggestionEntity `json:"entities"`
	Evidence      consoleSuggestionEvidence `json:"evidence"`
	Shared        bool                      `json:"shared"`
	Historical    bool                      `json:"historical"`
	HistoryStatus string                    `json:"history_status"`
	DeletedAt     *time.Time                `json:"deleted_at,omitempty"`
}

func (s *Server) consoleSharedResults(items []smarthome.RuleSuggestion, admin bool, shared map[string]bool, directory map[string]consoleSuggestionEntity) ([]consoleSharedResult, error) {
	results := []consoleSharedResult{}
	fresh := map[string]bool{}
	for id := range directory {
		fresh[id] = true
	}
	for _, item := range items {
		if !admin && !shared[item.ID] {
			continue
		}
		view := suggestionProjection(item, false, true, directory)
		op, err := s.smartHome.GetInstallOperationForSuggestion(item.ID)
		if err != nil && !errors.Is(err, smarthome.ErrInstallOperationNotFound) {
			return nil, err
		}
		if err == nil && op.Status == "confirmed" {
			view.Status = "confirmed"
		}
		if !visibleSuggestion(view, fresh) {
			continue
		}
		result := consoleSharedResult{ID: item.ID, CreatedAt: view.CreatedAt, Title: view.Title, TimeZone: view.TimeZone, Rule: view.Rule, EntityIDs: view.EntityIDs, Entities: view.Entities, Evidence: view.Evidence, Shared: shared[item.ID], Historical: true, HistoryStatus: "legacy_unverified"}
		if op.ManagedAutomationID != "" {
			managed, e := s.smartHome.GetManagedAutomation(op.ManagedAutomationID)
			if e != nil {
				return nil, e
			}
			if managed.SuggestionID != item.ID {
				continue
			}
			result.HistoryStatus = "recorded"
			if managed.OperationID != op.ID || managed.SuggestionVersion != item.ID {
				result.HistoryStatus = "updated"
			}
			if managed.DeletedAt != nil {
				result.HistoryStatus = "deleted"
				result.DeletedAt = managed.DeletedAt
			}
		}
		results = append(results, result)
	}
	return results, nil
}
