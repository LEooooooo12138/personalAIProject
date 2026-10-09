package smarthome

import (
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
func (a *Analyzer) AnalyzeInLocation(now time.Time, lookbackDays int, location *time.Location) (*DeviceReport, error) {
	if location == nil || lookbackDays < 1 || lookbackDays > 366 {
		return nil, fmt.Errorf("explicit valid home timezone and 1-366 days required")
	}
	local := now.In(location)
	end := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	start := end.AddDate(0, 0, -lookbackDays)
	entries, err := a.store.GetHistoryRange("", time.Time{}, end)
	if err != nil {
		return nil, err
	}
	summaries := map[string][]HistoryEntry{}
	byEntity := map[string][]HistoryEntry{}
	for _, entry := range entries {
		if !entry.Timestamp.Before(end) {
			continue
		}
		entry.Timestamp = entry.Timestamp.In(location)
		summaries[entry.EntityID] = append(summaries[entry.EntityID], entry)
	}
	report := &DeviceReport{PeriodStart: start, PeriodEnd: end}
	var ids []string
	for id := range summaries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, entry := range summaries[id] {
			if !entry.Timestamp.Before(start) {
				byEntity[id] = append(byEntity[id], entry)
			}
		}
		// Extract before clipping so a verified preceding baseline remains available.
		var ons []HistoryEntry
		for _, entry := range turnOnEntries(summaries[id]) {
			if !entry.Timestamp.Before(start) {
				ons = append(ons, entry)
			}
		}
		if isBinaryDevice(id) {
			report.Patterns = append(report.Patterns, a.timePatternsFromTransitions(id, ons, lookbackDays)...)
		}
		if isSensorDevice(id) {
			report.Patterns = append(report.Patterns, a.detectAnomalies(id, byEntity[id])...)
		}
	}
	// Correlations share the same verified transitions and completed-day window.
	verified := map[string][]HistoryEntry{}
	for id, es := range summaries {
		for _, e := range turnOnEntries(es) {
			if !e.Timestamp.Before(start) {
				verified[id] = append(verified[id], e)
			}
		}
	}
	report.Patterns = append(report.Patterns, a.correlationsFromTransitions(verified, lookbackDays)...)
	report.Suggestions = a.generateSuggestionsInLocation(report.Patterns, location)
	report.Devices = a.buildSummaries(summaries, lookbackDays, start, end)
	return report, nil
}

// detectTimePatterns finds regular on/off time patterns for a device.
func (a *Analyzer) detectTimePatterns(entityID string, entries []HistoryEntry, lookbackDays int) []DetectedPattern {
	return a.timePatternsFromTransitions(entityID, turnOnEntries(entries), lookbackDays)
}
func (a *Analyzer) timePatternsFromTransitions(entityID string, entries []HistoryEntry, lookbackDays int) []DetectedPattern {
	var patterns []DetectedPattern

	// Collect "turned on" events with their hour of day.
	type turnOn struct {
		hour int
		day  string // "2006-01-02"
	}

	var turnOns []turnOn
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.State), "on") {
			turnOns = append(turnOns, turnOn{
				hour: e.Timestamp.Hour(),
				day:  e.Timestamp.Format("2006-01-02"),
			})
		}
	}

	if len(turnOns) < a.minDays {
		return patterns
	}

	// Group by hour and count unique days.
	hourCounts := make(map[int]int)
	hourDays := make(map[int]map[string]struct{})
	for _, t := range turnOns {
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
			})
		}
	}

	return patterns
}

// detectAnomalies finds unusual sensor values.
func (a *Analyzer) detectAnomalies(entityID string, entries []HistoryEntry) []DetectedPattern {
	var patterns []DetectedPattern
	if len(entries) < 10 {
		return patterns
	}

	// Calculate mean and stddev for numeric sensor values.
	var values []float64
	for _, e := range entries {
		var v float64
		if _, err := fmt.Sscanf(e.State, "%f", &v); err == nil {
			values = append(values, v)
		}
	}
	if len(values) < 10 {
		return patterns
	}

	mean, stddev := stats(values)
	if stddev < 0.001 {
		return patterns
	}

	// Detect outliers (values beyond 3 stddev).
	anomalyCount := 0
	threshold := 3.0
	for _, v := range values {
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
			PeriodDays: len(values),
		})
	}

	return patterns
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
	var ids []string
	for id := range byEntity {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var patterns []DetectedPattern
	for _, source := range ids {
		for _, target := range ids {
			if source == target {
				continue
			}
			hits := 0
			hitDays := map[string]bool{}
			total := len(byEntity[source])
			for _, from := range byEntity[source] {
				for _, to := range byEntity[target] {
					delta := to.Timestamp.Sub(from.Timestamp)
					if delta > 0 && delta < 5*time.Minute {
						hits++
						hitDays[from.Timestamp.Format("2006-01-02")] = true
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
			patterns = append(patterns, DetectedPattern{Type: "correlation", EntityID: source, RelatedID: target, Description: fmt.Sprintf("When %s changes off to on, %s changes off to on within 5 minutes %.0f%% of the time", source, target, confidence*100), Confidence: confidence, SampleSize: hits, PeriodDays: days})
		}
	}
	return patterns
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
	var summaries []DeviceSummary

	for entityID, entries := range byEntity {
		var onCount, offCount int
		var totalOnSeconds float64
		var previousAt time.Time

		ordered := unambiguousHistory(entries)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Timestamp.Before(ordered[j].Timestamp) })
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
		for _, e := range ordered {
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

		for _, transition := range stateTransitions(entries) {
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

	return summaries
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
	if len(values) == 0 {
		return 0, 0
	}
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))

	var sumSq float64
	for _, v := range values {
		sumSq += (v - mean) * (v - mean)
	}
	stddev = math.Sqrt(sumSq / float64(len(values)))
	return
}
