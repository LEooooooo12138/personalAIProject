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
	analysisMu     sync.Mutex
	analysisRunsMu sync.Mutex
	analysisRuns   map[string]AnalysisRun
	analysisActive string
	analysisCancel context.CancelFunc
	cfg            HAConfig
	client         *HomeAssistantClient
	catalog        *CatalogService
	store          *DeviceStore
	collector      *Collector
	analyzer       *Analyzer
	logger         *zap.Logger
	actionMu       sync.Mutex
	lifecycleMu    sync.Mutex
	started        bool
	stopped        bool
	cancel         context.CancelFunc
	workers        sync.WaitGroup
	serviceCtx     context.Context
	stopParent     func() bool
	backfillMu     sync.Mutex
	backfillRuns   map[string]BackfillRun
	backfillActive string
	backfillCancel context.CancelFunc
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

	if err := store.migrateManagementState(); err != nil {
		client.Close()
		return nil, err
	}
	if err := store.recoverInstallOperations(); err != nil {
		client.Close()
		return nil, fmt.Errorf("install state: %w", err)
	}
	if err := store.recoverManagementOperations(); err != nil {
		client.Close()
		return nil, err
	}

	m := &Manager{
		cfg:      cfg,
		client:   client,
		store:    store,
		analyzer: NewAnalyzer(store, logger),
		logger:   logger,
	}

	m.serviceCtx, m.cancel = context.WithCancel(context.Background())
	m.backfillRuns, err = store.loadBackfillRuns()
	if err != nil {
		m.cancel()
		client.Close()
		return nil, fmt.Errorf("backfill state: %w", err)
	}
	m.analysisRuns, err = store.loadAnalysisRuns()
	if err != nil {
		m.cancel()
		client.Close()
		return nil, fmt.Errorf("analysis state: %w", err)
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
	m.stopParent = context.AfterFunc(ctx, m.cancel)
	if ctx.Err() != nil {
		m.cancel()
	}
	ctx = m.serviceCtx
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
	if m.stopParent != nil {
		m.stopParent()
	}
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
	run, err := m.StartAnalysis(JobScope{Context: ctx, OwnerID: "system"}, lookbackDays)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := m.GetAnalysisRun(run.ID)
		if err != nil {
			return nil, err
		}
		switch current.Status {
		case "completed":
			return current.Report, nil
		case "failed":
			return nil, errors.New(current.ErrorCode)
		case "cancelled":
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, context.Canceled
		}
		<-ticker.C
	}
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

// JobScope is an authorization lifetime supplied by the gateway, independent of
// the completed HTTP request. Every Manager job consumes its scope exactly once.
type JobScope struct {
	Context context.Context
	Release func()
	OwnerID string
}

// acquireJob registers before Stop may Wait, merges service and authorization
// cancellation, and returns an idempotent finish for accepted or rejected work.
// A3 asynchronous analysis shares this lifecycle with backfill.
func (m *Manager) acquireJob(scope JobScope) (context.Context, func(), error) {
	var once sync.Once
	release := func() {
		once.Do(func() {
			if scope.Release != nil {
				scope.Release()
			}
		})
	}
	if scope.Context == nil || scope.OwnerID == "" {
		release()
		return nil, nil, errors.New("invalid_job_scope")
	}
	m.lifecycleMu.Lock()
	if m.stopped || m.serviceCtx.Err() != nil {
		m.lifecycleMu.Unlock()
		release()
		return nil, nil, errors.New("manager_stopped")
	}
	if err := scope.Context.Err(); err != nil {
		m.lifecycleMu.Unlock()
		release()
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(scope.Context)
	stop := context.AfterFunc(m.serviceCtx, cancel)
	m.workers.Add(1)
	m.lifecycleMu.Unlock()
	var finished sync.Once
	finish := func() { finished.Do(func() { stop(); cancel(); release(); m.workers.Done() }) }
	return ctx, finish, nil
}
