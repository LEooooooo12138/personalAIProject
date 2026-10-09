package smarthome

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Manager coordinates the smart home subsystem: data collection, analysis,
// and rule suggestion.
var ErrAnalysisBusy = errors.New("analysis busy")

type Manager struct {
	analysisMu  sync.Mutex
	cfg         HAConfig
	client      *HomeAssistantClient
	catalog     *CatalogService
	store       *DeviceStore
	collector   *Collector
	analyzer    *Analyzer
	logger      *zap.Logger
	actionMu    sync.Mutex
	lifecycleMu sync.Mutex
	started     bool
	stopped     bool
	cancel      context.CancelFunc
	workers     sync.WaitGroup
}

// NewManager creates and initializes the smart home manager.
func NewManager(cfg HAConfig, logger *zap.Logger) (*Manager, error) {
	if cfg.PollIntervalSec <= 0 {
		cfg.PollIntervalSec = 3600
	}
	client := NewHomeAssistantClient(cfg.BaseURL, cfg.Token, 30*time.Second)
	if cfg.OAuthCredentialsFile != "" {
		if cfg.Token != "" {
			client.Close()
			return nil, fmt.Errorf("smarthome manager: conflicting authentication configuration")
		}
		provider, err := NewOAuthTokenProvider(cfg.BaseURL, cfg.OAuthCredentialsFile, 10*time.Second)
		if err != nil {
			client.Close()
			return nil, fmt.Errorf("smarthome manager: OAuth configuration: %w", err)
		}
		client.tokenProvider = provider
	}

	vaultPath := cfg.AgentVaultPath
	if vaultPath == "" {
		vaultPath = "agent-vault/smart-home"
	}

	store, err := NewDeviceStore(vaultPath)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("smarthome manager: %w", err)
	}

	m := &Manager{
		cfg:      cfg,
		client:   client,
		store:    store,
		analyzer: NewAnalyzer(store, logger),
		logger:   logger,
	}

	m.catalog = NewCatalogService(context.Background(), client)
	m.collector = NewCollector(client, store, cfg.PollIntervalSec, logger)

	return m, nil
}

// Start begins data collection and the daily analysis scheduler.
func (m *Manager) Start(ctx context.Context) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.started || m.stopped {
		return
	}
	ctx, m.cancel = context.WithCancel(ctx)
	m.started = true
	context.AfterFunc(ctx, m.catalog.Close)
	m.collector.Start(ctx)

	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		m.dailyAnalysisLoop(ctx)
	}()

	m.logger.Info("smarthome manager started",
		zap.String("base_url", m.cfg.BaseURL),
		zap.Int("poll_interval_sec", m.cfg.PollIntervalSec),
		zap.Int("analysis_hour", m.cfg.AnalysisHour),
	)
}

// Stop shuts down the smart home subsystem.
func (m *Manager) Stop() {
	m.lifecycleMu.Lock()
	m.stopped = true
	if m.cancel != nil {
		m.cancel()
	}
	m.lifecycleMu.Unlock()
	m.client.Close()
	m.catalog.Close()
	m.collector.Stop()
	m.workers.Wait()
	m.logger.Info("smarthome manager stopped")
}

// dailyAnalysisLoop runs at the configured analysis hour each day.
func (m *Manager) dailyAnalysisLoop(ctx context.Context) {
	for {
		location, err := m.homeLocation(ctx)
		if err != nil {
			m.logger.Warn("cannot schedule household analysis", zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
				continue
			}
		}
		next := nextAnalysisTime(time.Now(), m.cfg.AnalysisHour, location)
		m.logger.Debug("smarthome: next analysis scheduled", zap.Time("at", next))

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}

		// Run daily analysis.
		if err := m.runDailyAnalysis(ctx); err != nil {
			m.logger.Error("daily analysis failed", zap.Error(err))
		}
	}
}

// runDailyAnalysis performs a full analysis cycle and saves results.
func (m *Manager) runDailyAnalysis(ctx context.Context) error {
	m.logger.Info("smarthome: running daily analysis")
	report, err := m.TriggerAnalysis(ctx, 14)
	if err != nil {
		return fmt.Errorf("analysis: %w", err)
	}

	// Log summary.
	for _, s := range report.Suggestions {
		m.logger.Info("smarthome suggestion",
			zap.String("id", s.ID),
			zap.String("title", s.Title),
			zap.Float64("confidence", s.Confidence),
		)
	}

	return nil
}

// GetClient returns the Home Assistant client.
func (m *Manager) GetClient() *HomeAssistantClient {
	return m.client
}

// GetStore returns the device store.
func (m *Manager) GetStore() *DeviceStore {
	return m.store
}

// GetAnalyzer returns the pattern analyzer.
func (m *Manager) GetAnalyzer() *Analyzer {
	return m.analyzer
}

// TriggerAnalysis manually runs analysis and returns the report.
func (m *Manager) TriggerAnalysis(ctx context.Context, lookbackDays int) (*DeviceReport, error) {
	if !m.analysisMu.TryLock() {
		return nil, ErrAnalysisBusy
	}
	defer m.analysisMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lookbackDays < 1 || lookbackDays > 366 {
		return nil, fmt.Errorf("lookback days must be between 1 and 366")
	}
	location, err := m.homeLocation(ctx)
	if err != nil {
		return nil, err
	}
	report, err := m.analyzer.AnalyzeInLocation(time.Now(), lookbackDays, location)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.store.SaveSuggestions(report.Suggestions); err != nil {
		return nil, fmt.Errorf("save suggestions: %w", err)
	}
	stored, err := m.store.GetSuggestions()
	if err != nil {
		return nil, fmt.Errorf("read published suggestions: %w", err)
	}
	byID := make(map[string]RuleSuggestion, len(stored))
	for _, suggestion := range stored {
		byID[suggestion.ID] = suggestion
	}
	for i, suggestion := range report.Suggestions {
		report.Suggestions[i] = byID[suggestion.ID]
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.store.SaveReport(*report); err != nil {
		return nil, fmt.Errorf("save report: %w", err)
	}
	return report, nil
}

// nextAnalysisTime calculates the next occurrence of the specified hour.
func nextAnalysisTime(now time.Time, hour int, location *time.Location) time.Time {
	local := now.In(location)
	next := time.Date(local.Year(), local.Month(), local.Day(), hour, 0, 0, 0, location)
	if !next.After(now) {
		day := time.Date(local.Year(), local.Month(), local.Day()+1, 12, 0, 0, 0, location)
		next = time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, location)
	}
	return next
}

func (m *Manager) Catalog(ctx context.Context) (CatalogSnapshot, error) { return m.catalog.Get(ctx) }
