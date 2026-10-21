package smarthome

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Collector periodically pulls data from Home Assistant and persists it.
type Collector struct {
	scheduleMu      sync.Mutex
	scheduleChanged chan struct{}
	scheduleBusy    bool
	liveWaiters     int
	status          CollectionStatus
	client          *HomeAssistantClient
	store           *DeviceStore
	interval        time.Duration
	logger          *zap.Logger
	mu              sync.Mutex
	running         bool
	stopped         bool
	cancel          context.CancelFunc
	done            chan struct{}
}

// NewCollector creates a data collector.
func NewCollector(client *HomeAssistantClient, store *DeviceStore, pollIntervalSec int, logger *zap.Logger) *Collector {
	return &Collector{
		client:   client,
		store:    store,
		interval: time.Duration(pollIntervalSec) * time.Second,
		logger:   logger,
	}
}

// Start begins the periodic collection loop.
func (c *Collector) Start(ctx context.Context) {
	c.mu.Lock()
	if c.running || c.stopped {
		c.mu.Unlock()
		return
	}
	c.running = true
	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	done := c.done
	c.mu.Unlock()

	c.logger.Info("smarthome collector started",
		zap.Duration("interval", c.interval),
	)

	go func() {
		defer close(done)
		// Do an immediate first collection.
		if err := c.collect(ctx); err != nil {
			c.logger.Warn("smarthome initial collection failed", zap.Error(err))
		}

		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				c.logger.Info("smarthome collector context cancelled")
				return
			case <-ticker.C:
				if err := c.collect(ctx); err != nil {
					c.logger.Warn("smarthome collection failed", zap.Error(err))
				}
			}
		}
	}()
}

// Stop shuts down the collector.
func (c *Collector) Stop() {
	c.mu.Lock()
	c.stopped = true
	if c.cancel != nil {
		c.cancel()
	}
	done := c.done
	c.mu.Unlock()
	if done != nil {
		<-done
	}
	c.mu.Lock()
	c.running = false
	c.mu.Unlock()
}

// collect persists complete windows only. Failed windows keep their checkpoint for retry.
func (c *Collector) collect(ctx context.Context) (result error) {
	release, err := c.acquireCollection(ctx, true)
	if err != nil {
		return err
	}
	defer release()
	c.observe(func(s *CollectionStatus) {
		s.Phase = "snapshot"
		s.LastAttemptAt = utcNow()
		s.ErrorCode = nil
		s.WindowStart = nil
		s.WindowEnd = nil
		s.CompletedBatches = 0
		s.TotalBatches = 0
	})
	defer func() {
		c.observe(func(s *CollectionStatus) {
			if result != nil {
				s.Phase = "failed"
				s.LastFailureAt = utcNow()
				code := "collection_failed"
				if ctx.Err() != nil {
					code = "cancelled"
				}
				s.ErrorCode = &code
			} else {
				s.Phase = "idle"
				s.LastSuccessAt = utcNow()
			}
		})
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	states, err := c.client.GetStates(ctx)
	if err != nil {
		return fmt.Errorf("collect states: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err = c.store.SaveSnapshot(states); err != nil {
		return err
	}
	c.observe(func(s *CollectionStatus) { s.SnapshotAt = utcNow() })
	var ids []string
	for _, state := range states {
		ids = append(ids, state.EntityID)
	}
	if err := validateHistoryIDs(ids); err != nil {
		return err
	}
	ids = uniqueEntityIDs(ids)
	if len(ids) == 0 {
		return fmt.Errorf("history collection has no state entities")
	}
	end := time.Now().UTC()
	start, err := c.store.LoadHistoryCheckpoint()
	if err != nil {
		return err
	}
	pending, err := c.store.pendingHistoryWindow()
	if err != nil {
		return err
	}
	if !pending.End.IsZero() && pending.End.After(start) {
		if start.IsZero() || pending.Start.Equal(start) {
			start = pending.Start
		} else {
			return fmt.Errorf("pending history window does not match checkpoint")
		}
	}
	if start.IsZero() {
		start = end.Add(-c.interval)
	}
	for start.Before(end) {
		windowEnd := start.Add(time.Hour)
		if windowEnd.After(end) {
			windowEnd = end
		}
		if pending.Start.Equal(start) && pending.End.After(start) {
			windowEnd = pending.End
		}
		if err := c.store.savePendingHistoryWindow(start, windowEnd); err != nil {
			return fmt.Errorf("persist pending window: %w", err)
		}
		window := TimeWindow{Start: start, End: windowEnd}
		batches, err := c.historyBatches(ids, window)
		if err != nil {
			return err
		}
		windowStart := start
		c.observe(func(s *CollectionStatus) {
			s.Phase = "history"
			s.WindowStart = &windowStart
			s.WindowEnd = &windowEnd
			s.CompletedBatches = 0
			s.TotalBatches = len(batches)
		})
		for _, ids := range batches {
			batch, receipt, err := c.fetchHistoryWindow(ctx, ids, window)
			if saveErr := c.store.SaveHistoryBatch(receipt, batch); saveErr != nil {
				return fmt.Errorf("save history batch: %w", saveErr)
			}
			if err != nil {
				return fmt.Errorf("history batch: %w", err)
			}
			c.observe(func(s *CollectionStatus) { s.CompletedBatches++ })
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err = c.store.SaveHistoryWindow(start, windowEnd, nil); err != nil {
			return fmt.Errorf("save history window: %w", err)
		}
		start = windowEnd
	}
	return nil
}

// acquireCollection gives queued live collection priority at every backfill
// window boundary. Waiting is cancellable and no store lock crosses a request.
func (c *Collector) acquireCollection(ctx context.Context, live bool) (func(), error) {
	c.scheduleMu.Lock()
	if c.scheduleChanged == nil {
		c.scheduleChanged = make(chan struct{})
	}
	if live {
		c.liveWaiters++
	}
	for {
		if err := ctx.Err(); err != nil {
			if live {
				c.liveWaiters--
				c.signalCollection()
			}
			c.scheduleMu.Unlock()
			return nil, err
		}
		if !c.scheduleBusy && (live || c.liveWaiters == 0) {
			if live {
				c.liveWaiters--
			}
			c.scheduleBusy = true
			c.scheduleMu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					c.scheduleMu.Lock()
					c.scheduleBusy = false
					c.signalCollection()
					c.scheduleMu.Unlock()
				})
			}, nil
		}
		changed := c.scheduleChanged
		c.scheduleMu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		c.scheduleMu.Lock()
	}
}
func (c *Collector) signalCollection() {
	close(c.scheduleChanged)
	c.scheduleChanged = make(chan struct{})
}
func (c *Collector) historyBatches(ids []string, window TimeWindow) ([][]string, error) {
	if err := validateHistoryIDs(ids); err != nil {
		return nil, err
	}
	if err := validateHistoryWindow(window); err != nil {
		return nil, err
	}
	ids = uniqueEntityIDs(ids)
	var batches [][]string
	for offset := 0; offset < len(ids); {
		n := offset
		for n < len(ids) && n-offset < 50 && len(c.client.BaseURL+historyPath(ids[offset:n+1], window.Start.Add(-time.Minute), window.End)) <= 6000 {
			n++
		}
		if n == offset {
			return nil, fmt.Errorf("history entity exceeds URL limit")
		}
		batches = append(batches, ids[offset:n])
		offset = n
	}
	return batches, nil
}

// fetchHistoryWindow reads one bounded batch, including the existing one-minute
// overlap. The receipt describes the progress window, not that overlap. The
// caller stages it and selects its source before publishing a complete window.
func (c *Collector) fetchHistoryWindow(ctx context.Context, ids []string, window TimeWindow) ([]HistoryEntry, HistoryReceipt, error) {
	ids = uniqueEntityIDs(ids)
	receipt := HistoryReceipt{Window: window, RequestedEntityIDs: ids, Source: "live"}
	batches, err := c.historyBatches(ids, window)
	if err == nil && len(batches) != 1 {
		err = fmt.Errorf("history batch exceeds limits")
	}
	var entries []HistoryEntry
	if err == nil {
		entries, err = c.client.GetHistoryForEntities(ctx, ids, window.Start.Add(-time.Minute), window.End)
	}
	if err == nil {
		receipt.SuccessfulEntityIDs = ids
	} else {
		receipt.ErrorCode = HAErrorCode(err)
		if ctx.Err() != nil {
			receipt.ErrorCode = "cancelled"
		}
	}
	return entries, receipt, err
}
