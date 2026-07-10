package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

)

// 鈹€鈹€ Session Store 鈹€鈹€
//
// Wraps SessionManager with three additional capabilities:
//   1. Disk persistence  鈫?sessions survive restarts
//   2. Vector embeddings 鈫?semantic search over conversation history
//   3. History search    鈫?find past conversations by meaning
//
// Sessions are stored as JSON in agent-vault/_sessions/.
// Embeddings are cached in agent-vault/_sessions/embeddings.json
// using the same cache-validation pattern as EmbeddingStore.

// SessionStore adds persistence and vector search to session management.
type SessionStore struct {
	mgr       *SessionManager
	sessionsDir string
	cachePath   string
	infer       Embedder
	logger      *zap.Logger

	// Vector index
	mu        sync.RWMutex
	messages  []sessionMessage // embedded messages
	indexed   bool
}

// Embedder is the interface for generating embeddings (bge-m3 via Ollama).
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// sessionMessage is a single embedded message from session history.
type sessionMessage struct {
	SessionID string    `json:"session_id"`
	MsgIndex  int       `json:"msg_index"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	Embedding []float32 `json:"embedding"`
}

// sessionFile is the on-disk representation of a session.
type sessionFile struct {
	ID           string         `json:"id"`
	ChannelID    string         `json:"channel_id"`
	UserID       string         `json:"user_id"`
	StartedAt    time.Time      `json:"started_at"`
	LastActiveAt time.Time      `json:"last_active_at"`
	RoundCount   int            `json:"round_count"`
	Messages     []Message `json:"messages"`
}

// SessionHit is a search result from session history.
type SessionHit struct {
	SessionID string
	MsgIndex  int
	Role      string
	Content   string
	Score     float64
	Timestamp time.Time
}

// NewSessionStore creates a session store with persistence and vector search.
// sessionsDir is typically {agentVault}/_sessions/.
func NewSessionStore(mgr *SessionManager, sessionsDir string, infer Embedder, logger *zap.Logger) *SessionStore {
	os.MkdirAll(sessionsDir, 0755)
	return &SessionStore{
		mgr:         mgr,
		sessionsDir: sessionsDir,
		cachePath:   filepath.Join(sessionsDir, "embeddings.json"),
		infer:       infer,
		logger:      logger,
		messages:    make([]sessionMessage, 0),
	}
}

// Initialize loads persisted sessions from disk and rebuilds the vector index.
// Must be called once at startup.
func (ss *SessionStore) Initialize(ctx context.Context) error {
	// Load sessions from disk.
	entries, err := os.ReadDir(ss.sessionsDir)
	if err != nil {
		return fmt.Errorf("read sessions dir: %w", err)
	}

	loaded := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if e.Name() == "embeddings.json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(ss.sessionsDir, e.Name()))
		if err != nil {
			ss.logger.Warn("failed to read session file", zap.String("file", e.Name()), zap.Error(err))
			continue
		}
		var sf sessionFile
		if err := json.Unmarshal(data, &sf); err != nil {
			ss.logger.Warn("failed to parse session file", zap.String("file", e.Name()), zap.Error(err))
			continue
		}

		// Re-create session in SessionManager.
		s, err := ss.mgr.GetOrCreate(sf.ChannelID, sf.UserID)
		if err != nil {
			ss.logger.Warn("failed to restore session", zap.String("id", sf.ID), zap.Error(err))
			continue
		}
		for _, msg := range sf.Messages {
			ss.mgr.AddMessage(s, msg)
		}
		loaded++
	}
	ss.logger.Info("sessions loaded from disk", zap.Int("count", loaded))

	// Try loading embedding cache.
	if ss.loadEmbeddingCache() {
		ss.logger.Info("session embedding cache loaded", zap.Int("messages", len(ss.messages)))
		ss.mu.Lock()
		ss.indexed = true
		ss.mu.Unlock()
		return nil
	}

	// Build embeddings for loaded sessions.
	return ss.buildEmbeddings(ctx)
}

// SaveSession persists a single session to disk.
func (ss *SessionStore) SaveSession(s *Session) error {
	clone := CloneSession(s)
	sf := sessionFile{
		ID:           clone.ID,
		ChannelID:    clone.ChannelID,
		UserID:       clone.UserID,
		StartedAt:    clone.StartedAt,
		LastActiveAt: clone.LastActiveAt,
		RoundCount:   clone.RoundCount,
		Messages:     clone.Messages,
	}

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	// Sanitize filename: replace ":" with "_"
	filename := strings.ReplaceAll(sf.ID, ":", "_") + ".json"
	path := filepath.Join(ss.sessionsDir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}
	return nil
}

// IndexMessage generates an embedding for a user message and adds it to the vector index.
// Should be called after each user message is added to the session.
func (ss *SessionStore) IndexMessage(ctx context.Context, s *Session, msgIndex int, msg Message) error {
	if msg.Role != "user" {
		return nil // only index user queries
	}
	if len(msg.Content) < 3 {
		return nil // skip very short messages
	}

	vec, err := ss.infer.Embed(ctx, msg.Content)
	if err != nil {
		return fmt.Errorf("embed message: %w", err)
	}

	sm := sessionMessage{
		SessionID: s.ID,
		MsgIndex:  msgIndex,
		Role:      msg.Role,
		Content:   msg.Content,
		Timestamp: msg.Timestamp,
		Embedding: vec,
	}

	ss.mu.Lock()
	ss.messages = append(ss.messages, sm)
	ss.mu.Unlock()

	// Persist embedding cache asynchronously.
	go func() {
		if err := ss.saveEmbeddingCache(); err != nil {
			ss.logger.Warn("failed to save session embedding cache", zap.Error(err))
		}
	}()

	return nil
}

// SearchSessions performs semantic search over past conversation messages.
func (ss *SessionStore) SearchSessions(ctx context.Context, query string, k int) ([]SessionHit, error) {
	if k <= 0 {
		k = 5
	}

	queryVec, err := ss.infer.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	type scored struct {
		idx   int
		score float32
	}
	var scores []scored
	for i, sm := range ss.messages {
		s := cosineSimilarity(queryVec, sm.Embedding)
		if s > 0.3 {
			scores = append(scores, scored{idx: i, score: s})
		}
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	var hits []SessionHit
	for i, sc := range scores {
		if i >= k {
			break
		}
		sm := ss.messages[sc.idx]
		hits = append(hits, SessionHit{
			SessionID: sm.SessionID,
			MsgIndex:  sm.MsgIndex,
			Role:      sm.Role,
			Content:   sm.Content,
			Score:     float64(sc.score),
			Timestamp: sm.Timestamp,
		})
	}
	return hits, nil
}

// 鈹€鈹€ Embedding cache 鈹€鈹€

type sessionEmbeddingCache struct {
	Version  int              `json:"version"`
	BuiltAt  int64            `json:"built_at"`
	Messages []sessionMessage `json:"messages"`
}

const sessionEmbeddingVersion = 1

func (ss *SessionStore) loadEmbeddingCache() bool {
	data, err := os.ReadFile(ss.cachePath)
	if err != nil {
		return false
	}
	var cache sessionEmbeddingCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return false
	}
	if cache.Version != sessionEmbeddingVersion {
		return false
	}
	if len(cache.Messages) == 0 {
		return false
	}

	ss.mu.Lock()
	ss.messages = cache.Messages
	ss.mu.Unlock()
	return true
}

func (ss *SessionStore) saveEmbeddingCache() error {
	ss.mu.RLock()
	defer ss.mu.RUnlock()

	cache := sessionEmbeddingCache{
		Version:  sessionEmbeddingVersion,
		BuiltAt:  time.Now().Unix(),
		Messages: ss.messages,
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ss.cachePath, data, 0644)
}

// buildEmbeddings generates embeddings for all loaded session messages.
func (ss *SessionStore) buildEmbeddings(ctx context.Context) error {
	ss.logger.Info("building session embeddings...")

	// Collect all user messages from active sessions.
	ss.mgr.Mu().RLock()
	var allMsgs []sessionMessage
	for _, s := range ss.mgr.Sessions() {
		clone := CloneSession(s)
		for i, msg := range clone.Messages {
			if msg.Role != "user" || len(msg.Content) < 3 {
				continue
			}
			allMsgs = append(allMsgs, sessionMessage{
				SessionID: clone.ID,
				MsgIndex:  i,
				Role:      msg.Role,
				Content:   msg.Content,
				Timestamp: msg.Timestamp,
			})
		}
	}
	ss.mgr.Mu().RUnlock()

	for i := range allMsgs {
		vec, err := ss.infer.Embed(ctx, allMsgs[i].Content)
		if err != nil {
			ss.logger.Warn("embed session message failed", zap.Int("idx", i), zap.Error(err))
			continue
		}
		allMsgs[i].Embedding = vec
	}

	ss.mu.Lock()
	ss.messages = allMsgs
	ss.indexed = true
	ss.mu.Unlock()

	if err := ss.saveEmbeddingCache(); err != nil {
		ss.logger.Warn("failed to save session embedding cache", zap.Error(err))
	}

	ss.logger.Info("session embeddings built", zap.Int("count", len(allMsgs)))
	return nil
}


// ── Session List / History API ──

// SessionInfo is a lightweight summary of a session for listing.
type SessionInfo struct {
	ID           string    `json:"id"`
	ChannelID    string    `json:"channel_id"`
	UserID       string    `json:"user_id"`
	StartedAt    time.Time `json:"started_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	RoundCount   int       `json:"round_count"`
	MessageCount int       `json:"message_count"`
	Preview      string    `json:"preview"`
}

// ListSessions returns summaries of all persisted sessions.
func (ss *SessionStore) ListSessions(channelFilter string) []SessionInfo {
	entries, err := os.ReadDir(ss.sessionsDir)
	if err != nil {
		return nil
	}

	var infos []SessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if e.Name() == "embeddings.json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(ss.sessionsDir, e.Name()))
		if err != nil {
			continue
		}
		var sf sessionFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}

		if channelFilter != "" && sf.ChannelID != channelFilter {
			continue
		}

		preview := ""
		for _, m := range sf.Messages {
			if m.Role == "user" && m.Content != "" {
				preview = m.Content
				break
			}
		}
		if len([]rune(preview)) > 50 {
			preview = string([]rune(preview)[:50]) + "..."
		}

		infos = append(infos, SessionInfo{
			ID:           sf.ID,
			ChannelID:    sf.ChannelID,
			UserID:       sf.UserID,
			StartedAt:    sf.StartedAt,
			LastActiveAt: sf.LastActiveAt,
			RoundCount:   sf.RoundCount,
			MessageCount: len(sf.Messages),
			Preview:      preview,
		})
	}

	// Sort by most recent first.
	for i := 0; i < len(infos); i++ {
		for j := i + 1; j < len(infos); j++ {
			if infos[j].LastActiveAt.After(infos[i].LastActiveAt) {
				infos[i], infos[j] = infos[j], infos[i]
			}
		}
	}

	return infos
}

// GetMessages returns all messages for a given session (by channel + userID).
// Checks in-memory sessions first, then falls back to disk.
func (ss *SessionStore) GetMessages(channelID, userID string) []Message {
	// Check in-memory active session first.
	key := channelID + ":" + userID
	ss.mgr.Mu().RLock()
	if s, ok := ss.mgr.Sessions()[key]; ok {
		clone := CloneSession(s)
		ss.mgr.Mu().RUnlock()
		return clone.Messages
	}
	ss.mgr.Mu().RUnlock()

	// Fall back to disk.
	filename := strings.ReplaceAll(key, ":", "_") + ".json"
	data, err := os.ReadFile(filepath.Join(ss.sessionsDir, filename))
	if err != nil {
		return nil
	}
	var sf sessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil
	}
	return sf.Messages
}

// ── Cosine similarity ──

func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(magA) * math.Sqrt(magB)))
}
