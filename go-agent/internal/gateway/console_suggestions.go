package gateway

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/console"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

type consoleSuggestionRule struct {
	Triggers   []smarthome.AutomationTrigger   `json:"triggers"`
	Conditions []smarthome.AutomationCondition `json:"conditions"`
	Actions    []smarthome.AutomationAction    `json:"actions"`
}
type consoleSuggestionEvidence struct {
	SampleSize *int `json:"sample_size"`
	PeriodDays *int `json:"period_days"`
}
type consoleSuggestionView struct {
	ID                 string                    `json:"id"`
	Status             string                    `json:"status"`
	CreatedAt          time.Time                 `json:"created_at"`
	Title              string                    `json:"title"`
	Confidence         float64                   `json:"confidence"`
	TimeZone           string                    `json:"time_zone"`
	EntityIDs          []string                  `json:"entity_ids"`
	Rule               *consoleSuggestionRule    `json:"rule"`
	MissingBindings    []string                  `json:"missing_bindings"`
	UnsupportedCode    *string                   `json:"unsupported_code"`
	Shared             bool                      `json:"shared"`
	CanBind            bool                      `json:"can_bind"`
	CanConfirm         bool                      `json:"can_confirm"`
	CanIgnore          bool                      `json:"can_ignore"`
	SourceSuggestionID string                    `json:"source_suggestion_id,omitempty"`
	SupersededBy       string                    `json:"superseded_by,omitempty"`
	Evidence           consoleSuggestionEvidence `json:"evidence"`
}

func (s *Server) setupConsoleSmartHomeRoutes(g *gin.RouterGroup) {
	g.GET("/suggestions", s.handleConsoleSuggestions)
	g.POST("/admin/suggestions/:id/bindings", s.handleConsoleSuggestionBindings)
	g.POST("/admin/suggestions/:id/confirm", s.handleConsoleSuggestionConfirm)
	g.POST("/admin/suggestions/:id/ignore", s.handleConsoleSuggestionIgnore)
	g.GET("/admin/sharing", s.handleConsoleSharing)
	g.PUT("/admin/sharing", s.handleConsolePutSharing)
	g.GET("/admin/collection", s.handleConsoleCollection)
	g.POST("/admin/analyze", s.handleConsoleAnalyze)
}

// Bind cancellation before slow service work. Middleware still supplies Origin,
// CSRF and principal; handlers repeat role checks and publication is serialized
// against Store revocation without holding its lock during HA/network work.
func (s *Server) consoleSmartHomeContext(c *gin.Context, admin bool) (context.Context, func(), bool) {
	p := requestConsolePrincipal(c.Request)
	if admin && p.Role != "admin" {
		consoleError(c, 403, "forbidden", "Permission denied")
		return nil, nil, false
	}
	if s.smartHome == nil {
		consoleError(c, 503, "ha_not_configured", "Home service is not configured")
		return nil, nil, false
	}
	ctx, stop, err := s.consoleStore.BindSession(c.Request.Context(), consoleToken(c.Request))
	if err != nil {
		consoleStoreError(c, err)
		return nil, nil, false
	}
	bounded, cancel := context.WithTimeout(ctx, 60*time.Second)
	return bounded, func() { cancel(); stop() }, true
}
func (s *Server) consoleSmartHomeJSON(c *gin.Context, ctx context.Context, data any) {
	// Resolve first so revocation reports authentication, even when its bound
	// context was cancelled at the same instant.
	p, err := s.consoleStore.Resolve(consoleToken(c.Request))
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	if p.UserID != requestConsolePrincipal(c.Request).UserID {
		consoleStoreError(c, console.ErrUnauthenticated)
		return
	}
	err = s.consoleStore.WithSession(ctx, consoleToken(c.Request), func(bounded context.Context) error {
		if err := bounded.Err(); err != nil {
			return err
		}
		c.JSON(http.StatusOK, data)
		return nil
	})
	if err != nil {
		consoleStoreError(c, err)
	}
}
func (s *Server) consoleSuggestionError(c *gin.Context, err error) {
	if !s.consoleSmartHomeSessionValid(c) {
		return
	}
	switch {
	case errors.Is(err, smarthome.ErrSuggestionNotFound):
		consoleError(c, 404, "not_found", "Suggestion not found")
	case errors.Is(err, smarthome.ErrAnalysisBusy):
		consoleError(c, 409, "busy", "An analysis is already running")
	case errors.Is(err, smarthome.ErrSuggestionConflict):
		consoleError(c, 409, "conflict", "Suggestion state changed")
	case errors.Is(err, smarthome.ErrUnsupportedRule):
		consoleError(c, 422, "unsupported_rule", "Rule needs supported conditions and entities")
	case errors.Is(err, smarthome.ErrHARequest):
		consoleError(c, 502, smarthome.HAErrorCode(err), "Home service could not complete the operation")
	default:
		consoleError(c, 503, "unavailable", "Suggestion service is unavailable")
	}
}
func safeSuggestionID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func suggestionProjection(item smarthome.RuleSuggestion, admin, shared bool) consoleSuggestionView {
	out := consoleSuggestionView{ID: item.ID, Status: item.Status, CreatedAt: item.CreatedAt, Title: "自动化建议", TimeZone: "", EntityIDs: []string{}, MissingBindings: []string{}, Shared: shared}
	if !math.IsNaN(item.Confidence) && !math.IsInf(item.Confidence, 0) && item.Confidence >= 0 && item.Confidence <= 1 {
		out.Confidence = item.Confidence
	}
	if item.Intent != nil {
		if _, err := time.LoadLocation(item.Intent.TimeZone); err == nil && item.Intent.TimeZone != "Local" {
			out.TimeZone = item.Intent.TimeZone
		}
	}
	rule, err := smarthome.BuildAutomation(item)
	if err != nil {
		code := "unsupported_rule"
		out.UnsupportedCode = &code
		// Only expose a bind capability after validating every other intent field.
		// The synthetic occupancy below is validation-only and is never returned.
		if item.Intent != nil && item.Intent.Kind == "time" && item.PresenceBinding == nil {
			probe := item
			probe.PresenceBinding = &smarthome.PresenceBinding{EntityID: "binary_sensor.console_validation", State: "on"}
			if _, probeErr := smarthome.BuildAutomation(probe); probeErr == nil {
				code = "missing_bindings"
				out.MissingBindings = []string{"presence_home"}
				out.EntityIDs = []string{item.Intent.EntityID}
				out.Title = fmt.Sprintf("定时开启 %s（%s）", item.Intent.EntityID, item.Intent.At)
				out.CanBind = admin && item.Status == "pending"
			}
		}
	} else {
		out.Rule = &consoleSuggestionRule{Triggers: append([]smarthome.AutomationTrigger{}, rule.Trigger...), Conditions: append([]smarthome.AutomationCondition{}, rule.Condition...), Actions: append([]smarthome.AutomationAction{}, rule.Action...)}
		seen := map[string]bool{}
		for _, trigger := range rule.Trigger {
			if trigger.EntityID != "" {
				seen[trigger.EntityID] = true
			}
		}
		for _, condition := range rule.Condition {
			if condition.EntityID != "" {
				seen[condition.EntityID] = true
			}
		}
		for _, action := range rule.Action {
			seen[action.Target.EntityID] = true
		}
		for id := range seen {
			out.EntityIDs = append(out.EntityIDs, id)
		}
		sort.Strings(out.EntityIDs)
		if item.Intent != nil && item.Intent.Kind == "time" {
			out.Title = fmt.Sprintf("定时开启 %s（%s）", item.Intent.EntityID, item.Intent.At)
		} else if len(out.EntityIDs) > 0 {
			out.Title = "自动化规则：" + out.EntityIDs[0]
		}
		out.CanConfirm = admin && (item.Status == "pending" || item.Status == "failed" || item.Status == "applying")
	}
	out.CanIgnore = admin && item.Status == "pending"
	if admin {
		if safeSuggestionID(item.SourceSuggestionID) {
			out.SourceSuggestionID = item.SourceSuggestionID
		}
		if safeSuggestionID(item.SupersededBy) {
			out.SupersededBy = item.SupersededBy
		}
	}
	// RuleSuggestion has no typed historical sample/period fields. Free-form
	// DataSource is never parsed into invented numeric evidence.
	return out
}
func (s *Server) consoleFreshEntities(ctx context.Context) (map[string]bool, error) {
	snapshot, err := s.consoleCatalog(ctx)
	if err != nil || snapshot.Meta.Freshness != "fresh" || snapshot.Meta.Connection != "connected" || snapshot.Meta.ObservedAt == nil || time.Since(*snapshot.Meta.ObservedAt) > 30*time.Second || snapshot.Meta.ObservedAt.After(time.Now().Add(time.Second)) {
		return nil, smarthome.ErrCatalogUnavailable
	}
	areas, err := snapshot.ListAreas("")
	if err != nil {
		return nil, err
	}
	entities := map[string]bool{}
	for _, area := range areas.Areas {
		devices, err := snapshot.ListDevices(area.ID)
		if err != nil {
			return nil, err
		}
		for _, device := range devices.Devices {
			for _, entity := range device.Entities {
				entities[entity.EntityID] = true
			}
		}
	}
	return entities, nil
}
func visibleSuggestion(view consoleSuggestionView, entities map[string]bool) bool {
	if view.Status != "confirmed" || !view.Shared || view.Rule == nil || len(view.EntityIDs) == 0 {
		return false
	}
	for _, id := range view.EntityIDs {
		if !entities[id] {
			return false
		}
	}
	return true
}
func (s *Server) handleConsoleSuggestions(c *gin.Context) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, false)
	if !ok {
		return
	}
	defer stop()
	admin := requestConsolePrincipal(c.Request).Role == "admin"
	var entities map[string]bool
	if !admin {
		var err error
		entities, err = s.consoleFreshEntities(ctx)
		if err != nil {
			s.consoleSmartHomeFailure(c, 503, "catalog_unavailable", "Cannot verify current household entities")
			return
		}
	}
	items, err := s.smartHome.GetStore().GetSuggestions()
	if err != nil {
		s.consoleSuggestionError(c, err)
		return
	}
	sharing, err := s.consoleStore.GetSharing()
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	shared := map[string]bool{}
	for _, id := range sharing.SuggestionIDs {
		shared[id] = true
	}
	views := []consoleSuggestionView{}
	for _, item := range items {
		view := suggestionProjection(item, admin, shared[item.ID])
		if admin || visibleSuggestion(view, entities) {
			views = append(views, view)
		}
	}
	s.consoleSmartHomeJSON(c, ctx, gin.H{"suggestions": views, "count": len(views)})
}
func (s *Server) handleConsoleSuggestionBindings(c *gin.Context) {
	s.consoleSuggestionMutation(c, "bind")
}
func (s *Server) handleConsoleSuggestionConfirm(c *gin.Context) {
	s.consoleSuggestionMutation(c, "confirm")
}
func (s *Server) handleConsoleSuggestionIgnore(c *gin.Context) {
	s.consoleSuggestionMutation(c, "ignore")
}
func (s *Server) consoleSuggestionMutation(c *gin.Context, operation string) {
	ctx, stop, ok := s.consoleSmartHomeContext(c, true)
	if !ok {
		return
	}
	defer stop()
	id := c.Param("id")
	if !safeSuggestionID(id) {
		consoleError(c, 404, "not_found", "Suggestion not found")
		return
	}
	var item *smarthome.RuleSuggestion
	var err error
	switch operation {
	case "bind":
		var body smarthome.SuggestionBindings
		if !decodeConsoleJSON(c, &body) {
			return
		}
		item, err = s.smartHome.BindSuggestion(ctx, id, body)
	case "confirm":
		var body struct{}
		if !decodeConsoleJSON(c, &body) {
			return
		}
		item, err = s.smartHome.ConfirmSuggestion(ctx, id)
	case "ignore":
		var body struct{}
		if !decodeConsoleJSON(c, &body) {
			return
		}
		if err = ctx.Err(); err == nil {
			item, err = s.smartHome.IgnoreSuggestionContext(ctx, id)
		}
	}
	if err != nil {
		s.consoleSuggestionError(c, err)
		return
	}
	sharing, err := s.consoleStore.GetSharing()
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	shared := false
	for _, sharedID := range sharing.SuggestionIDs {
		shared = shared || sharedID == item.ID
	}
	s.consoleSmartHomeJSON(c, ctx, gin.H{"suggestion": suggestionProjection(*item, true, shared)})
}

func (s *Server) consoleSmartHomeSessionValid(c *gin.Context) bool {
	p, err := s.consoleStore.Resolve(consoleToken(c.Request))
	if err != nil {
		consoleStoreError(c, err)
		return false
	}
	original := requestConsolePrincipal(c.Request)
	if p.UserID != original.UserID || p.Role != original.Role || p.MustChangePassword {
		consoleStoreError(c, console.ErrUnauthenticated)
		return false
	}
	return true
}
func (s *Server) consoleSmartHomeStoreError(c *gin.Context, err error) {
	if s.consoleSmartHomeSessionValid(c) {
		consoleStoreError(c, err)
	}
}
func (s *Server) consoleSmartHomeFailure(c *gin.Context, status int, code, message string) {
	if s.consoleSmartHomeSessionValid(c) {
		consoleError(c, status, code, message)
	}
}
