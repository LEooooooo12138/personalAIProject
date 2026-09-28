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
	client   *HomeAssistantClient
	store    *DeviceStore
	interval time.Duration
	logger   *zap.Logger
	mu       sync.Mutex
	running  bool
	stopped  bool
	cancel   context.CancelFunc
	done     chan struct{}
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

// collect performs one round of data collection.
func (c *Collector) collect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.logger.Debug("smarthome: collecting device states")

	// Get current states
	states, err := c.client.GetStates(ctx)
	if err != nil {
		return fmt.Errorf("collect states: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := c.store.SaveSnapshot(states); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}

	// Get recent history (last interval)
	end := time.Now()
	start := end.Add(-c.interval)
	history, err := c.client.GetHistory(ctx, "", start, end)
	if err != nil {
		c.logger.Warn("smarthome: history collection failed (non-fatal)", zap.Error(err))
	} else if len(history) > 0 && ctx.Err() == nil {
		if err := c.store.SaveHistory(history); err != nil {
			c.logger.Warn("smarthome: save history failed (non-fatal)", zap.Error(err))
		}
	}

	c.logger.Debug("smarthome collection complete",
		zap.Int("states", len(states)),
		zap.Int("history_entries", len(history)),
	)

	return nil
}
