package smarthome

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Analyzer detects patterns in device history and generates rule suggestions.
type Analyzer struct {
	store      *DeviceStore
	logger     *zap.Logger
	minDays    int     // minimum days of data needed for analysis
	minHitRate float64 // minimum hit rate (0-1) to suggest a rule
}

// NewAnalyzer creates a pattern analyzer.
func NewAnalyzer(store *DeviceStore, logger *zap.Logger) *Analyzer {
	return &Analyzer{
		store:      store,
		logger:     logger,
		minDays:    7,
		minHitRate: 0.75,
	}
}

// Analyze runs all pattern detectors and returns a DeviceReport.
func (a *Analyzer) Analyze(now time.Time, lookbackDays int) (*DeviceReport, error) {
	return a.AnalyzeInLocation(now, lookbackDays, time.UTC)
}
func (a *Analyzer) AnalyzeInLocation(now time.Time, days int, loc *time.Location) (*DeviceReport, error) {
	return a.AnalyzeContext(context.Background(), now, days, loc)
}
func (a *Analyzer) AnalyzeContext(ctx context.Context, now time.Time, lookbackDays int, location *time.Location) (*DeviceReport, error) {
	if location == nil || lookbackDays < 1 || lookbackDays > 366 {
		return nil, fmt.Errorf("explicit valid home timezone and 1-366 days required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	local := now.In(location)
	end := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	start := end.AddDate(0, 0, -lookbackDays)
	ids, corrupt, err := a.store.analysisEntityIDs(ctx, TimeWindow{Start: start.UTC(), End: end.UTC()})
	if err != nil {
		return nil, err
	}
	read := HistoryReadResult{Coverage: HistoryCoverage{Entities: map[string]EntityHistoryCoverage{}, CorruptFiles: corrupt}}
	if len(ids) > 0 {
		read, err = a.store.ReadHistory(ctx, HistoryQuery{EntityIDs: ids, Window: TimeWindow{Start: start.UTC(), End: end.UTC()}})
		if err != nil {
			return nil, err
		}
	}
	summaries, byEntity, verified := map[string][]HistoryEntry{}, map[string][]HistoryEntry{}, map[string][]HistoryEntry{}
	for i, e := range append(append([]HistoryEntry(nil), read.BaselineEntries...), read.Entries...) {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		e.Timestamp = e.Timestamp.In(location)
		summaries[e.EntityID] = append(summaries[e.EntityID], e)
		if !e.Timestamp.Before(start) {
			byEntity[e.EntityID] = append(byEntity[e.EntityID], e)
		}
	}
	report := &DeviceReport{PeriodStart: start, PeriodEnd: end, Coverage: read.Coverage, Counts: AnalysisCounts{Entries: len(read.Entries), Excluded: map[string]int{}}}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		changes, err := stateTransitionsContext(ctx, summaries[id])
		if err != nil {
			return nil, err
		}
		for _, e := range changes {
			if !e.At.Before(start) && e.From == "off" && e.To == "on" {
				verified[id] = append(verified[id], HistoryEntry{EntityID: id, State: e.To, Timestamp: e.At, ObservationKind: "change"})
			}
		}
		if isBinaryDevice(id) {
			patterns, err := a.timePatternsContext(ctx, id, verified[id], lookbackDays)
			if err != nil {
				return nil, err
			}
			report.Patterns = append(report.Patterns, patterns...)
		}
		if isSensorDevice(id) {
			patterns, err := a.anomaliesContext(ctx, id, byEntity[id])
			if err != nil {
				return nil, err
			}
			report.Patterns = append(report.Patterns, patterns...)
		}
	}
	correlations, err := a.correlationsContext(ctx, verified, lookbackDays)
	if err != nil {
		return nil, err
	}
	report.Patterns = append(report.Patterns, correlations...)
	valid := map[string][]HistoryEntry{}
	excludedByEntity := map[string]int{}
	for _, id := range ids {
		normalized, err := unambiguousHistoryContext(ctx, byEntity[id])
		if err != nil {
			return nil, err
		}
		for i, e := range normalized {
			if i%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if e.State == "" || e.State == "unknown" || e.State == "unavailable" {
				report.Counts.Excluded["invalid_or_ambiguous_state"]++
				excludedByEntity[id]++
				continue
			}
			valid[id] = append(valid[id], e)
		}
	}
	eligibleDayCounts := map[string]int{}
	for _, id := range ids {
		dates, err := localObservationDates(ctx, valid[id])
		if err != nil {
			return nil, err
		}
		eligibleDayCounts[id] = len(dates)
	}
	for i := range report.Patterns {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		p := &report.Patterns[i]
		eligible := eligibleDayCounts[p.EntityID]
		p.EligibleDays = &eligible
		p.ExcludedCounts = map[string]int{"invalid_or_ambiguous_state": excludedByEntity[p.EntityID] + excludedByEntity[p.RelatedID]}
	}
	report.Outcomes, err = a.patternOutcomes(ctx, ids, valid, verified, report.Patterns, lookbackDays)
	if err != nil {
		return nil, err
	}
	report.Outcome = "no_pattern"
	applicable := false
	for _, out := range report.Outcomes {
		if !out.Applicable {
			continue
		}
		applicable = true
		if out.Outcome == "matched" {
			report.Outcome = "matched"
			break
		}
		if out.Outcome == "insufficient_data" {
			report.Outcome = "insufficient_data"
		}
	}
	if !applicable {
		report.Outcome = "insufficient_data"
	}
	report.Suggestions = a.generateSuggestionsInLocation(report.Patterns, location)
	report.Counts.Patterns = len(report.Patterns)
	report.Counts.Suggestions = len(report.Suggestions)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	report.Devices, err = a.summariesContext(ctx, summaries, lookbackDays, start, end)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return report, nil
}

// Local dates are counted from observations, never inferred from request coverage.
func localObservationDates(ctx context.Context, es []HistoryEntry) (map[string]struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dates := map[string]struct{}{}
	for i, e := range es {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		dates[e.Timestamp.Format("2006-01-02")] = struct{}{}
	}
	return dates, ctx.Err()
}

// Prerequisites explain observed evidence, not recorder completeness. They never
// gate an existing detector match or replace its original denominator.
func (a *Analyzer) patternOutcomes(ctx context.Context, ids []string, valid, verified map[string][]HistoryEntry, patterns []DetectedPattern, days int) (map[string]PatternOutcome, error) {
	result := map[string]PatternOutcome{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Index matches once instead of rescanning every pattern for every candidate.
	type patternKey struct{ kind, source, target string }
	matched := map[patternKey]struct{}{}
	for i, p := range patterns {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		matched[patternKey{p.Type, p.EntityID, p.RelatedID}] = struct{}{}
	}
	observedDates, transitionDates := map[string]map[string]struct{}{}, map[string]map[string]struct{}{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dates, err := localObservationDates(ctx, valid[id])
		if err != nil {
			return nil, err
		}
		observedDates[id] = dates
		dates, err = localObservationDates(ctx, verified[id])
		if err != nil {
			return nil, err
		}
		transitionDates[id] = dates
	}
	required := max(a.minDays, int(math.Ceil(a.minHitRate*float64(days))))
	for _, kind := range []string{"time", "correlation", "anomaly"} {
		out := PatternOutcome{Outcome: "insufficient_data", Reason: "no_eligible_entities"}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			switch kind {
			case "time":
				if !isBinaryDevice(id) {
					continue
				}
				c := PatternCandidate{EntityID: id, ObservationDays: len(observedDates[id]), RequiredDays: required, SourceTransitions: len(verified[id]), SourceTransitionDays: len(transitionDates[id])}
				c.Outcome = "no_pattern"
				c.Reason = "observed_days_sufficient_no_match"
				if c.ObservationDays < required {
					c.Outcome = "insufficient_data"
					c.Reason = "insufficient_observed_days"
				}
				out.Candidates = append(out.Candidates, c)
			case "anomaly":
				if !isSensorDevice(id) {
					continue
				}
				c := PatternCandidate{EntityID: id, ObservationDays: len(observedDates[id])}
				for i, e := range valid[id] {
					if i%256 == 0 {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
					}
					var n float64
					if _, err := fmt.Sscanf(e.State, "%f", &n); err == nil {
						c.NumericSamples++
					}
				}
				c.Outcome = "no_pattern"
				c.Reason = "numeric_samples_sufficient_no_match"
				if c.NumericSamples < 10 {
					c.Outcome = "insufficient_data"
					c.Reason = "insufficient_numeric_samples"
				}
				out.Candidates = append(out.Candidates, c)
			case "correlation":
				if !isBinaryDevice(id) && len(verified[id]) == 0 {
					continue
				}
				for _, target := range ids {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if id == target || (!isBinaryDevice(target) && len(verified[target]) == 0) {
						continue
					}
					sourceDays := transitionDates[id]
					overlap := 0
					checked := 0
					for date := range sourceDays {
						if checked%256 == 0 {
							if err := ctx.Err(); err != nil {
								return nil, err
							}
						}
						checked++
						if _, ok := observedDates[target][date]; ok {
							overlap++
						}
					}
					c := PatternCandidate{EntityID: id, RelatedID: target, ObservationDays: overlap, RequiredDays: a.minDays, SourceTransitions: len(verified[id]), SourceTransitionDays: len(sourceDays), Outcome: "no_pattern", Reason: "source_opportunities_sufficient_no_match"}
					if c.SourceTransitionDays < a.minDays {
						c.Outcome = "insufficient_data"
						c.Reason = "insufficient_source_transition_days"
					} else if overlap < a.minDays {
						c.Outcome = "insufficient_data"
						c.Reason = "insufficient_target_observation_days"
					}
					out.Candidates = append(out.Candidates, c)
				}
			}
		}
		out.Applicable = len(out.Candidates) > 0
		if out.Applicable {
			out.Outcome = "no_pattern"
			out.Reason = "observed_prerequisites_satisfied"
			for i := range out.Candidates {
				if i%256 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				c := &out.Candidates[i]
				if _, ok := matched[patternKey{kind, c.EntityID, c.RelatedID}]; ok {
					c.Outcome = "matched"
					c.Reason = "detector_matched"
				}
				if c.Outcome == "insufficient_data" {
					out.Outcome = "insufficient_data"
					out.Reason = "candidate_data_insufficient"
				}
			}
			for i, c := range out.Candidates {
				if i%256 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				if c.Outcome == "matched" {
					out.Outcome = "matched"
					out.Reason = "detector_matched"
					break
				}
			}
		}
		result[kind] = out
	}
	return result, ctx.Err()
}

// detectTimePatterns finds regular on/off time patterns for a device.
func (a *Analyzer) detectTimePatterns(entityID string, entries []HistoryEntry, lookbackDays int) []DetectedPattern {
	return a.timePatternsFromTransitions(entityID, turnOnEntries(entries), lookbackDays)
}
func (a *Analyzer) timePatternsFromTransitions(entityID string, entries []HistoryEntry, lookbackDays int) []DetectedPattern {
	p, _ := a.timePatternsContext(context.Background(), entityID, entries, lookbackDays)
	return p
}
func (a *Analyzer) timePatternsContext(ctx context.Context, entityID string, entries []HistoryEntry, lookbackDays int) ([]DetectedPattern, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var patterns []DetectedPattern

	// Collect "turned on" events with their hour of day.
	type turnOn struct {
		hour int
		day  string // "2006-01-02"
	}

	var turnOns []turnOn
	hourRefs := map[int][]LocalEventReference{}
	for i, e := range entries {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if strings.Contains(strings.ToLower(e.State), "on") {
			hourRefs[e.Timestamp.Hour()] = append(hourRefs[e.Timestamp.Hour()], LocalEventReference{e.EntityID, e.Timestamp.UTC()})
			turnOns = append(turnOns, turnOn{
				hour: e.Timestamp.Hour(),
				day:  e.Timestamp.Format("2006-01-02"),
			})
		}
	}

	if len(turnOns) < a.minDays {
		return patterns, ctx.Err()
	}

	// Group by hour and count unique days.
	hourCounts := make(map[int]int)
	hourDays := make(map[int]map[string]struct{})
	for i, t := range turnOns {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		hourCounts[t.hour]++
		if hourDays[t.hour] == nil {
			hourDays[t.hour] = make(map[string]struct{})
		}
		hourDays[t.hour][t.day] = struct{}{}
	}

	// Find hours that have consistent daily patterns.
	for hour := 0; hour < 24; hour++ {
		count := hourCounts[hour]
		uniqueDays := len(hourDays[hour])
		if uniqueDays < a.minDays {
			continue
		}

		// Calculate hit rate: days with event / total observed days.
		hitRate := float64(uniqueDays) / float64(lookbackDays)
		confidence := hitRate

		if confidence >= a.minHitRate {
			patterns = append(patterns, DetectedPattern{
				Type:     "time",
				EntityID: entityID,
				Description: fmt.Sprintf("Device turned on around %02d:00 on %d of %d days (%.0f%%)",
					hour, uniqueDays, lookbackDays, hitRate*100),
				Confidence: confidence,
				SampleSize: count,
				PeriodDays: lookbackDays,
				TimeOfDay:  fmt.Sprintf("%02d:00", hour),
				HitDays:    &uniqueDays, Denominator: lookbackDays, DenominatorType: "requested_days", EventRefs: hourRefs[hour],
			})
		}
	}

	return patterns, ctx.Err()
}

// detectAnomalies finds unusual sensor values.
func (a *Analyzer) detectAnomalies(entityID string, entries []HistoryEntry) []DetectedPattern {
	p, _ := a.anomaliesContext(context.Background(), entityID, entries)
	return p
}
func (a *Analyzer) anomaliesContext(ctx context.Context, entityID string, entries []HistoryEntry) ([]DetectedPattern, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var patterns []DetectedPattern
	if len(entries) < 10 {
		return patterns, ctx.Err()
	}

	// Calculate mean and stddev for numeric sensor values.
	var values []float64
	for i, e := range entries {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		var v float64
		if _, err := fmt.Sscanf(e.State, "%f", &v); err == nil {
			values = append(values, v)
		}
	}
	if len(values) < 10 {
		return patterns, ctx.Err()
	}

	mean, stddev, err := statsContext(ctx, values)
	if err != nil {
		return nil, err
	}
	if stddev < 0.001 {
		return patterns, ctx.Err()
	}

	// Detect outliers (values beyond 3 stddev).
	anomalyCount := 0
	threshold := 3.0
	for i, v := range values {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if math.Abs(v-mean) > threshold*stddev {
			anomalyCount++
		}
	}

	if anomalyCount > 0 {
		confidence := math.Min(float64(anomalyCount)/float64(len(values))*2, 0.9)
		patterns = append(patterns, DetectedPattern{
			Type:     "anomaly",
			EntityID: entityID,
			Description: fmt.Sprintf("Detected %d anomalous readings (mean=%.2f, stddev=%.2f, threshold=%.0fσ)",
				anomalyCount, mean, stddev, threshold),
			Confidence: confidence,
			SampleSize: anomalyCount,
			PeriodDays: len(values), Denominator: len(values), DenominatorType: "numeric_samples",
		})
	}

	return patterns, ctx.Err()
}

// Ordered pairs are independent: A->B does not imply B->A.
func (a *Analyzer) detectCorrelations(byEntity map[string][]HistoryEntry, days int) []DetectedPattern {
	verified := map[string][]HistoryEntry{}
	for id, entries := range byEntity {
		verified[id] = turnOnEntries(entries)
	}
	return a.correlationsFromTransitions(verified, days)
}
func (a *Analyzer) correlationsFromTransitions(byEntity map[string][]HistoryEntry, days int) []DetectedPattern {
	p, _ := a.correlationsContext(context.Background(), byEntity, days)
	return p
}
func (a *Analyzer) correlationsContext(ctx context.Context, byEntity map[string][]HistoryEntry, days int) ([]DetectedPattern, error) {
	var ids []string
	for id := range byEntity {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var patterns []DetectedPattern
	for _, source := range ids {
		for _, target := range ids {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if source == target {
				continue
			}
			hits := 0
			hitDays := map[string]bool{}
			total := len(byEntity[source])
			var refs []LocalEventReference
			for _, from := range byEntity[source] {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				for i, to := range byEntity[target] {
					if i%256 == 0 {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
					}
					delta := to.Timestamp.Sub(from.Timestamp)
					if delta > 0 && delta < 5*time.Minute {
						hits++
						hitDays[from.Timestamp.Format("2006-01-02")] = true
						refs = append(refs, LocalEventReference{source, from.Timestamp.UTC()}, LocalEventReference{target, to.Timestamp.UTC()})
						break
					}
				}
			}
			if total == 0 || len(hitDays) < a.minDays {
				continue
			}
			confidence := float64(hits) / float64(total)
			if confidence < a.minHitRate {
				continue
			}
			hitDayCount := len(hitDays)
			patterns = append(patterns, DetectedPattern{Type: "correlation", EntityID: source, RelatedID: target, Description: fmt.Sprintf("When %s changes off to on, %s changes off to on within 5 minutes %.0f%% of the time", source, target, confidence*100), Confidence: confidence, SampleSize: hits, PeriodDays: days, HitDays: &hitDayCount, Denominator: total, DenominatorType: "source_transitions", EventRefs: refs})
		}
	}
	return patterns, ctx.Err()
}

// generateSuggestions creates RuleSuggestion from detected patterns.
func (a *Analyzer) generateSuggestions(patterns []DetectedPattern) []RuleSuggestion {
	return a.generateSuggestionsInLocation(patterns, time.UTC)
}
func (a *Analyzer) generateSuggestionsInLocation(patterns []DetectedPattern, location *time.Location) []RuleSuggestion {
	var suggestions []RuleSuggestion
	for _, p := range patterns {
		if p.Type != "time" && p.Type != "correlation" {
			continue
		}
		intent := &SuggestionIntent{SchemaVersion: 1, Kind: p.Type, EntityID: p.EntityID, RelatedID: p.RelatedID, At: p.TimeOfDay, TimeZone: location.String()}
		if p.Type == "time" {
			intent.RequiredConditions = []string{"presence_home"}
		} else {
			intent.RequiredConditions = []string{"sunset_to_midnight"}
		}
		s := RuleSuggestion{Intent: intent, Description: p.Description, Confidence: p.Confidence, DataSource: fmt.Sprintf("%d-day history, %.0f%% hit rate", p.PeriodDays, p.Confidence*100), CreatedAt: time.Now(), Status: "pending"}
		renderSuggestion(&s)
		s.ID = stableSuggestionID(s)
		suggestions = append(suggestions, s)
	}
	return suggestions
}

// buildSummaries creates aggregated device summaries.
func (a *Analyzer) buildSummaries(byEntity map[string][]HistoryEntry, lookbackDays int, startTime, endTime time.Time) []DeviceSummary {
	r, _ := a.summariesContext(context.Background(), byEntity, lookbackDays, startTime, endTime)
	return r
}
func (a *Analyzer) summariesContext(ctx context.Context, byEntity map[string][]HistoryEntry, lookbackDays int, startTime, endTime time.Time) ([]DeviceSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var summaries []DeviceSummary

	for entityID, entries := range byEntity {
		var onCount, offCount int
		var totalOnSeconds float64
		var previousAt time.Time

		ordered, err := unambiguousHistoryContext(ctx, entries)
		if err != nil {
			return nil, err
		}
		lastState := ""
		addOnInterval := func(from, to time.Time) {
			if from.Before(startTime) {
				from = startTime
			}
			if to.After(endTime) {
				to = endTime
			}
			if to.After(from) {
				totalOnSeconds += to.Sub(from).Seconds()
			}
		}
		for i, e := range ordered {
			if i%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if e.Timestamp.After(endTime) {
				break
			}
			if lastState == "on" {
				addOnInterval(previousAt, e.Timestamp)
			}
			state := strings.ToLower(e.State)
			lastState = state
			previousAt = e.Timestamp
		}
		if lastState == "on" {
			addOnInterval(previousAt, endTime)
		}

		transitions, err := stateTransitionsContext(ctx, entries)
		if err != nil {
			return nil, err
		}
		for i, transition := range transitions {
			if i%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if !transition.At.Before(startTime) && transition.At.Before(endTime) {
				if transition.From == "off" && transition.To == "on" {
					onCount++
				}
				if transition.From == "on" && transition.To == "off" {
					offCount++
				}
			}
		}
		avgOnHour := totalOnSeconds / 3600.0 / float64(max(1, lookbackDays))

		summaries = append(summaries, DeviceSummary{
			EntityID:    entityID,
			Name:        friendlyName(entityID),
			Type:        deviceType(entityID),
			OnCount:     onCount,
			OffCount:    offCount,
			AvgOnHour:   math.Round(avgOnHour*10) / 10,
			TotalOnTime: math.Round(totalOnSeconds/3600.0*10) / 10,
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].EntityID < summaries[j].EntityID
	})

	return summaries, ctx.Err()
}

// --- helpers ---

func isBinaryDevice(entityID string) bool {
	prefixes := []string{"light.", "switch.", "lock.", "cover.", "fan."}
	for _, p := range prefixes {
		if strings.HasPrefix(entityID, p) {
			return true
		}
	}
	return false
}

func isSensorDevice(entityID string) bool {
	prefixes := []string{"sensor.", "binary_sensor.", "climate."}
	for _, p := range prefixes {
		if strings.HasPrefix(entityID, p) {
			return true
		}
	}
	return false
}

func friendlyName(entityID string) string {
	// light.living_room -> Living Room Light
	parts := strings.SplitN(entityID, ".", 2)
	name := entityID
	if len(parts) == 2 {
		name = strings.ReplaceAll(parts[1], "_", " ")
		name = simpleTitle(name)
	}
	return name
}

func deviceType(entityID string) string {
	parts := strings.SplitN(entityID, ".", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return "unknown"
}

// simpleTitle capitalizes the first letter of each word.
func simpleTitle(s string) string {
	if len(s) == 0 {
		return s
	}
	words := make([]string, 0)
	for _, w := range strings.Split(s, " ") {
		if len(w) > 0 {
			words = append(words, strings.ToUpper(w[:1])+w[1:])
		}
	}
	return strings.Join(words, " ")
}

func stats(values []float64) (mean, stddev float64) {
	mean, stddev, _ = statsContext(context.Background(), values)
	return
}

func statsContext(ctx context.Context, values []float64) (mean, stddev float64, err error) {
	if len(values) == 0 {
		return 0, 0, ctx.Err()
	}
	for i, v := range values {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		mean += v
	}
	mean /= float64(len(values))
	var sumSq float64
	for i, v := range values {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
		}
		sumSq += (v - mean) * (v - mean)
	}
	return mean, math.Sqrt(sumSq / float64(len(values))), ctx.Err()
}
