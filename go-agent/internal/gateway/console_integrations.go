package gateway

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

type consoleModelAvailability struct {
	Name      string `json:"name"`
	Available *bool  `json:"available"`
}
type consoleOllamaStatus struct {
	Connection string                     `json:"connection"`
	CheckedAt  *time.Time                 `json:"checked_at"`
	Models     []consoleModelAvailability `json:"models"`
	ErrorCode  *string                    `json:"error_code"`
}
type ollamaHealth struct {
	connection string
	checkedAt  *time.Time
	installed  map[string]bool
	errorCode  *string
}
type ollamaStatusCache struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
	now     func() time.Time
	health  ollamaHealth
	refresh chan struct{}
	closed  bool
}

func newOllamaStatusCache(now func() time.Time) *ollamaStatusCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &ollamaStatusCache{ctx: ctx, cancel: cancel, now: now, health: ollamaHealth{connection: "unknown"}}
}
func (cache *ollamaStatusCache) Close() {
	cache.mu.Lock()
	cache.closed = true
	cache.cancel()
	cache.mu.Unlock()
	cache.workers.Wait()
}
func (cache *ollamaStatusCache) Get(ctx context.Context, client inference.Client) (ollamaHealth, error) {
	if err := ctx.Err(); err != nil {
		return ollamaHealth{connection: "unknown"}, err
	}
	cache.mu.Lock()
	if cache.closed {
		cache.mu.Unlock()
		return ollamaHealth{connection: "unknown"}, context.Canceled
	}
	if cache.health.checkedAt != nil && cache.now().Sub(*cache.health.checkedAt) < 30*time.Second {
		health := cache.health
		cache.mu.Unlock()
		return health, nil
	}
	done := cache.refresh
	if done == nil {
		done = make(chan struct{})
		cache.refresh = done
		cache.workers.Add(1)
		go cache.fetch(client, done)
	}
	cache.mu.Unlock()
	select {
	case <-ctx.Done():
		return ollamaHealth{connection: "unknown"}, ctx.Err()
	case <-done:
	}
	cache.mu.Lock()
	health := cache.health
	cache.mu.Unlock()
	return health, nil
}
func (cache *ollamaStatusCache) fetch(client inference.Client, done chan struct{}) {
	defer cache.workers.Done()
	ctx, cancel := context.WithTimeout(cache.ctx, 10*time.Second)
	defer cancel()
	health := ollamaHealth{connection: "unavailable", installed: map[string]bool{}}
	checked := cache.now().UTC()
	health.checkedAt = &checked
	var models []inference.ModelInfo
	var err error
	if client == nil {
		err = context.Canceled
	} else {
		models, err = client.ListModels(ctx)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		health.connection = "connected"
		for _, m := range models {
			health.installed[ollamaModelName(m.ID)] = true
		}
	} else {
		code := "ollama_unavailable"
		health.errorCode = &code
	}
	cache.mu.Lock()
	cache.health = health
	cache.refresh = nil
	close(done)
	cache.mu.Unlock()
}

// An omitted tag means :latest. A registry hostname's colon is not a model tag.
func ollamaModelName(name string) string {
	tail := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		tail = name[i+1:]
	}
	if !strings.Contains(tail, ":") {
		return name + ":latest"
	}
	return name
}
func (s *Server) publicOllamaStatus(health ollamaHealth) consoleOllamaStatus {
	status := consoleOllamaStatus{Connection: health.connection, CheckedAt: health.checkedAt, Models: []consoleModelAvailability{}, ErrorCode: health.errorCode}
	seen := map[string]bool{}
	for _, name := range []string{s.cfg.Inference.Models.Local, s.cfg.Inference.Models.Vision, s.cfg.Inference.Models.Embedding} {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		available := health.connection == "connected" && health.installed[ollamaModelName(name)]
		var known *bool
		if health.connection == "connected" {
			known = &available
		}
		status.Models = append(status.Models, consoleModelAvailability{Name: name, Available: known})
		if !available && health.connection == "connected" {
			code := "model_missing"
			status.ErrorCode = &code
		}
	}
	return status
}
func (s *Server) handleConsoleIntegrations(c *gin.Context) {
	// HA and tags run in parallel; each shared upstream refresh is bounded by
	// its own ten-second deadline. Keep the caller context so an earlier equal
	// deadline cannot discard Catalog's stale observation before it publishes.
	ctx := c.Request.Context()
	haDone := make(chan smarthome.CatalogMeta, 1)
	go func() {
		snapshot, _ := s.consoleCatalog(ctx)
		meta := snapshot.Meta
		if meta.Connection == "" {
			code := "ha_unavailable"
			meta = smarthome.CatalogMeta{Freshness: "unknown", Connection: "unavailable", ErrorCode: &code}
		}
		haDone <- meta
	}()
	health, _ := s.ollamaStatus.Get(ctx, s.infer)
	var ha smarthome.CatalogMeta
	select {
	case ha = <-haDone:
	case <-ctx.Done():
		code := "ha_unavailable"
		ha = smarthome.CatalogMeta{Freshness: "unknown", Connection: "unavailable", ErrorCode: &code}
	}
	s.consoleReadJSON(c, gin.H{"ha": ha, "ollama": s.publicOllamaStatus(health)})
}
