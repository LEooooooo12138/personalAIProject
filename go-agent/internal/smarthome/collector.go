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
	collectMu sync.Mutex
	status    CollectionStatus
	client    *HomeAssistantClient
	store     *DeviceStore
	interval  time.Duration
	logger    *zap.Logger
	mu        sync.Mutex
	running   bool
	stopped   bool
	cancel    context.CancelFunc
	done      chan struct{}
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
	c.collectMu.Lock()
	defer c.collectMu.Unlock()
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
		queryStart := start.Add(-time.Minute)
		var boundaries []int
		for offset := 0; offset < len(ids); {
			n := offset
			for n < len(ids) && n-offset < 50 && len(c.client.BaseURL+historyPath(ids[offset:n+1], queryStart, windowEnd)) <= 6000 {
				n++
			}
			if n == offset {
				return fmt.Errorf("history entity exceeds URL limit")
			}
			boundaries = append(boundaries, n)
			offset = n
		}
		windowStart := start
		c.observe(func(s *CollectionStatus) {
			s.Phase = "history"
			s.WindowStart = &windowStart
			s.WindowEnd = &windowEnd
			s.CompletedBatches = 0
			s.TotalBatches = len(boundaries)
		})
		var all []HistoryEntry
		offset := 0
		for _, n := range boundaries {
			batch, err := c.client.GetHistoryForEntities(ctx, ids[offset:n], queryStart, windowEnd)
			if err != nil {
				return fmt.Errorf("history batch %d-%d: %w", offset, n, err)
			}
			all = append(all, batch...)
			offset = n
			c.observe(func(s *CollectionStatus) { s.CompletedBatches++ })
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err = c.store.SaveHistoryWindow(start, windowEnd, all); err != nil {
			return fmt.Errorf("save history window: %w", err)
		}
		start = windowEnd
	}
	return nil
}
