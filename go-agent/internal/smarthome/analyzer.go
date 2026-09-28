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
func (a *Analyzer) Analyze(endTime time.Time, lookbackDays int) (*DeviceReport, error) {
	startTime := endTime.AddDate(0, 0, -lookbackDays)

	// Keep earlier state transitions to know whether an entity was already on
	// at the left edge of this window. Pattern detection still uses only the window.
	entries, err := a.store.GetHistoryRange("", time.Time{}, endTime)
	if err != nil {
		return nil, fmt.Errorf("analyze: get history: %w", err)
	}
	if len(entries) == 0 {
		return &DeviceReport{
			PeriodStart: startTime,
			PeriodEnd:   endTime,
		}, nil
	}

	// Group entries by entity.
	byEntity := make(map[string][]HistoryEntry)
	summaryEntries := make(map[string][]HistoryEntry)
	for _, e := range entries {
		summaryEntries[e.EntityID] = append(summaryEntries[e.EntityID], e)
		if !e.Timestamp.Before(startTime) {
			byEntity[e.EntityID] = append(byEntity[e.EntityID], e)
		}
	}

	report := &DeviceReport{
		PeriodStart: startTime,
		PeriodEnd:   endTime,
	}

	// Detect patterns per entity.
	for entityID, entEntries := range byEntity {
		a.logger.Debug("analyzing entity", zap.String("entity", entityID), zap.Int("entries", len(entEntries)))

		// Time pattern detection for binary devices (lights, switches).
		if isBinaryDevice(entityID) {
			patterns := a.detectTimePatterns(entityID, entEntries, lookbackDays)
			report.Patterns = append(report.Patterns, patterns...)
		}

		// Anomaly detection for sensor devices.
		if isSensorDevice(entityID) {
			anomalies := a.detectAnomalies(entityID, entEntries)
			report.Patterns = append(report.Patterns, anomalies...)
		}
	}

	// Detect correlation patterns between entities.
	correlations := a.detectCorrelations(byEntity, lookbackDays)
	report.Patterns = append(report.Patterns, correlations...)

	// Generate rule suggestions from detected patterns.
	report.Suggestions = a.generateSuggestions(report.Patterns)

	// Build device summaries.
	report.Devices = a.buildSummaries(summaryEntries, lookbackDays, startTime, endTime)

	return report, nil
}

// detectTimePatterns finds regular on/off time patterns for a device.
func (a *Analyzer) detectTimePatterns(entityID string, entries []HistoryEntry, lookbackDays int) []DetectedPattern {
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
	for hour, count := range hourCounts {
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

// detectCorrelations finds pairs of entities that change state close together.
func (a *Analyzer) detectCorrelations(byEntity map[string][]HistoryEntry, lookbackDays int) []DetectedPattern {
	var patterns []DetectedPattern

	entityIDs := make([]string, 0, len(byEntity))
	for id := range byEntity {
		entityIDs = append(entityIDs, id)
	}

	// Compare each pair of entities.
	for i := 0; i < len(entityIDs); i++ {
		for j := i + 1; j < len(entityIDs); j++ {
			ea := entityIDs[i]
			eb := entityIDs[j]

			// Skip if both are the same type (not useful).
			if isSensorDevice(ea) && isSensorDevice(eb) {
				continue
			}

			entriesA := byEntity[ea]
			entriesB := byEntity[eb]

			// Count events where B changes within 5 minutes after A.
			correlations := 0
			totalA := 0
			for _, eA := range entriesA {
				if !strings.Contains(strings.ToLower(eA.State), "on") {
					continue
				}
				totalA++
				for _, eB := range entriesB {
					diff := eB.Timestamp.Sub(eA.Timestamp)
					if diff > 0 && diff < 5*time.Minute {
						if strings.Contains(strings.ToLower(eB.State), "on") {
							correlations++
							break
						}
					}
				}
			}

			if totalA >= a.minDays && correlations > 0 {
				hitRate := float64(correlations) / float64(totalA)
				if hitRate >= a.minHitRate {
					patterns = append(patterns, DetectedPattern{
						Type:      "correlation",
						EntityID:  ea,
						RelatedID: eb,
						Description: fmt.Sprintf("When %s turns on, %s follows within 5 minutes %.0f%% of the time",
							ea, eb, hitRate*100),
						Confidence: hitRate,
						SampleSize: correlations,
						PeriodDays: lookbackDays,
					})
				}
			}
		}
	}

	return patterns
}

// generateSuggestions creates RuleSuggestion from detected patterns.
func (a *Analyzer) generateSuggestions(patterns []DetectedPattern) []RuleSuggestion {
	var suggestions []RuleSuggestion
	now := time.Now()

	for _, p := range patterns {
		switch p.Type {
		case "time":
			suggestions = append(suggestions, RuleSuggestion{
				Title:       fmt.Sprintf("Scheduled: Turn on %s at %s", friendlyName(p.EntityID), p.TimeOfDay),
				Description: p.Description,
				Trigger:     p.TimeOfDay,
				Condition:   "someone is home",
				Action:      p.EntityID,
				Confidence:  p.Confidence,
				DataSource:  fmt.Sprintf("%d-day history, %.0f%% hit rate", p.PeriodDays, p.Confidence*100),
				CreatedAt:   now,
				Status:      "pending",
			})

		case "correlation":
			suggestions = append(suggestions, RuleSuggestion{
				Title:       fmt.Sprintf("Auto: When %s opens, turn on %s", friendlyName(p.EntityID), friendlyName(p.RelatedID)),
				Description: p.Description,
				Trigger:     fmt.Sprintf("state change: %s", p.EntityID),
				Condition:   fmt.Sprintf("time is between sunset and midnight"),
				Action:      p.RelatedID,
				Confidence:  p.Confidence,
				DataSource:  fmt.Sprintf("%d-day history, %.0f%% hit rate", p.PeriodDays, p.Confidence*100),
				CreatedAt:   now,
				Status:      "pending",
			})
		}
	}
	for i := range suggestions {
		suggestions[i].ID = stableSuggestionID(suggestions[i])
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

		ordered := append([]HistoryEntry(nil), entries...)
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
			if !e.Timestamp.Before(startTime) && state != lastState {
				if state == "on" {
					onCount++
				}
				if state == "off" {
					offCount++
				}
			}
			lastState = state
			previousAt = e.Timestamp
		}
		if lastState == "on" {
			addOnInterval(previousAt, endTime)
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
