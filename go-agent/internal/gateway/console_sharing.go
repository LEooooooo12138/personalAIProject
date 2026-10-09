package gateway

import (
	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/console"
)

func (s *Server) handleConsoleSharing(c *gin.Context) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, true)
	if !ok {
		return
	}
	defer stop()
	sharing, err := s.consoleStore.GetSharing()
	if err != nil {
		s.consoleSmartHomeStoreError(c, err)
		return
	}
	s.consoleSmartHomeJSON(c, ctx, gin.H{"revision": sharing.Revision, "suggestion_ids": append([]string{}, sharing.SuggestionIDs...)})
}
func (s *Server) handleConsolePutSharing(c *gin.Context) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, true)
	if !ok {
		return
	}
	defer stop()
	var body struct {
		Revision      *uint64  `json:"revision"`
		SuggestionIDs []string `json:"suggestion_ids"`
	}
	if !decodeConsoleJSON(c, &body) {
		return
	}
	if body.Revision == nil || body.SuggestionIDs == nil || len(body.SuggestionIDs) > 10000 {
		consoleError(c, 422, "invalid_request", "Supply revision and suggestion_ids")
		return
	}
	sharing, err := s.consoleStore.GetSharing()
	if err != nil {
		s.consoleSmartHomeStoreError(c, err)
		return
	}
	if *body.Revision != sharing.Revision {
		consoleStoreError(c, console.ErrConflict)
		return
	}
	existing := map[string]bool{}
	for _, id := range sharing.SuggestionIDs {
		existing[id] = true
	}
	seen := map[string]bool{}
	addsSharing := false
	for _, id := range body.SuggestionIDs {
		if !safeSuggestionID(id) || seen[id] {
			consoleError(c, 422, "invalid_request", "Supply unique suggestion IDs")
			return
		}
		seen[id] = true
		addsSharing = addsSharing || !existing[id]
	}
	// Withdrawal remains possible while HA is offline. New grants must verify
	// every requested rule and entity; a pure subset adds no authorization.
	if addsSharing {
		entities, err := s.consoleFreshEntities(ctx)
		if err != nil {
			s.consoleSmartHomeFailure(c, 503, "catalog_unavailable", "Cannot verify current household entities")
			return
		}
		items, err := s.smartHome.GetStore().GetSuggestions()
		if err != nil {
			s.consoleSuggestionError(c, err)
			return
		}
		byID := map[string]bool{}
		for _, item := range items {
			byID[item.ID] = visibleSuggestion(suggestionProjection(item, false, true), entities)
		}
		for _, id := range body.SuggestionIDs {
			if !byID[id] {
				consoleError(c, 422, "invalid_request", "Only verified confirmed suggestions may be shared")
				return
			}
		}
	}
	if err = ctx.Err(); err != nil {
		s.consoleSmartHomeStoreError(c, err)
		return
	}
	// Revision protects the EntityIDs read/merge/write; no new device ACL field
	// is accepted or exposed. Existing legacy values remain intact.
	updated, err := s.consoleStore.PutSharingForSession(ctx, consoleToken(c.Request), *body.Revision, sharing.EntityIDs, body.SuggestionIDs)
	if err != nil {
		s.consoleSmartHomeStoreError(c, err)
		return
	}
	s.consoleSmartHomeJSON(c, ctx, gin.H{"revision": updated.Revision, "suggestion_ids": append([]string{}, updated.SuggestionIDs...)})
}
