package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ── Public interface ──

// VectorStore provides semantic search over vault content.
// Phase 1.6: minimal Qdrant wrapper; full indexing pipeline in Phase 2.
type VectorStore interface {
	Index(ctx context.Context, id string, vector []float32, payload map[string]string) error
	Search(ctx context.Context, vector []float32, limit int) ([]ScoredPoint, error)
}

// ScoredPoint is a search result from the vector store.
type ScoredPoint struct {
	ID      string
	Score   float32
	Payload map[string]string
}

// ── Qdrant implementation ──

// QdrantStore is a minimal REST client for Qdrant vector database.
type QdrantStore struct {
	endpoint   string
	collection string
	hc         *http.Client
}

// NewQdrantStore creates a vector store backed by Qdrant.
func NewQdrantStore(endpoint, collection string) *QdrantStore {
	return &QdrantStore{
		endpoint:   endpoint,
		collection: collection,
		hc:         &http.Client{Timeout: 10 * time.Second},
	}
}

// Index writes a point to the Qdrant collection.
func (q *QdrantStore) Index(ctx context.Context, id string, vector []float32, payload map[string]string) error {
	body := map[string]interface{}{
		"points": []map[string]interface{}{
			{
				"id":      id,
				"vector":  vector,
				"payload": payload,
			},
		},
	}
	return q.put(ctx, fmt.Sprintf("/collections/%s/points", q.collection), body)
}

// Search performs a vector similarity search.
func (q *QdrantStore) Search(ctx context.Context, vector []float32, limit int) ([]ScoredPoint, error) {
	body := map[string]interface{}{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
	}

	resp, err := q.post(ctx, fmt.Sprintf("/collections/%s/points/search", q.collection), body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result []struct {
			ID      interface{}       `json:"id"`
			Score   float32           `json:"score"`
			Payload map[string]string `json:"payload"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("qdrant: search decode: %w", err)
	}

	points := make([]ScoredPoint, len(result.Result))
	for i, r := range result.Result {
		points[i] = ScoredPoint{
			ID:      fmt.Sprintf("%v", r.ID),
			Score:   r.Score,
			Payload: r.Payload,
		}
	}
	return points, nil
}

// ── helpers ──

func (q *QdrantStore) put(ctx context.Context, path string, body interface{}) error {
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, q.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("qdrant: %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.hc.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	return nil
}

func (q *QdrantStore) post(ctx context.Context, path string, body interface{}) ([]byte, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("qdrant: %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant: %s: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qdrant: %s: read: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qdrant: %s: status %d: %s", path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}
