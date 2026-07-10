package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
	"github.com/yuanleyao/ai-agent/internal/vault"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type wsConn struct {
	conn       *websocket.Conn
	logger     *zap.Logger
	infer      inference.Client
	vaultR     vault.Reader
	router     *core.Router
	filter     *filter.Chain
	sessionMgr *core.SessionManager
	embedStore *EmbeddingStore

	chainExecutor *chain.ChainExecutor
	chainRouter   *chain.ChainRouter

	sessionStore *core.SessionStore
	sedimenter  *memory.Sedimenter

	sessionID string
	session   *core.Session
	mu        sync.Mutex
}

type clientMessage struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
}

type serverMessage struct {
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type vaultSource struct {
	Title string
	Body  string
}

func handleWebSocket(logger *zap.Logger, infer inference.Client, vr vault.Reader, router *core.Router, fc *filter.Chain, sessionMgr *core.SessionManager,
	embedStore *EmbeddingStore, chainExecutor *chain.ChainExecutor, chainRouter *chain.ChainRouter, sessionStore *core.SessionStore, sedimenter *memory.Sedimenter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Warn("ws upgrade failed", zap.Error(err))
			return
		}
		wsc := &wsConn{
			conn:          conn,
			logger:        logger,
			infer:         infer,
			vaultR:        vr,
			router:        router,
			filter:        fc,
			sessionMgr:    sessionMgr,
			embedStore:    embedStore,
			chainExecutor: chainExecutor,
			chainRouter:   chainRouter,
			sessionStore:  sessionStore,
			sedimenter:   sedimenter,
		}
		wsc.loop()
	}
}

func (w *wsConn) loop() {
	defer func() {
		w.conn.Close()
		if w.session != nil {
			w.sessionMgr.EndSession(w.session)
			w.logger.Info("webchat session ended on disconnect",
				zap.String("session_id", w.session.ID),
			)
		}
	}()

	for {
		_, raw, err := w.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				w.logger.Warn("ws read error", zap.Error(err))
			}
			return
		}

		var msg clientMessage
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Content == "" {
			w.writeJSON(serverMessage{Type: "error", Message: "invalid message"})
			continue
		}

		if w.sessionID == "" {
			if msg.SessionID != "" {
				w.sessionID = msg.SessionID
			} else {
				w.sessionID = newSessionID()
			}
			w.logger.Info("webchat session started", zap.String("session_id", w.sessionID))
			w.writeJSON(serverMessage{Type: "session", SessionID: w.sessionID})
		}

		w.handleChat(msg.Content)
	}
}

func (w *wsConn) handleChat(content string) {
	session, err := w.sessionMgr.GetOrCreate("webchat", w.sessionID)
	if err != nil {
		w.logger.Error("webchat session create failed", zap.Error(err))
		w.writeJSON(serverMessage{Type: "error", Message: "session error"})
		return
	}
	w.session = session

	if ok := w.sessionMgr.AddMessage(session, core.Message{
		Role: "user", Content: content, Timestamp: time.Now(),
	}); !ok {
		w.sessionMgr.EndSession(session)
		newSession, err := w.sessionMgr.GetOrCreate("webchat", w.sessionID)
		if err != nil {
			w.writeJSON(serverMessage{Type: "error", Message: "session error"})
			return
		}
		session = newSession
		w.session = newSession
		w.sessionMgr.AddMessage(session, core.Message{
			Role: "user", Content: content, Timestamp: time.Now(),
		})

		// Index user message for semantic session search.
		if w.sessionStore != nil {
			msgIdx := len(session.Messages) - 1
			go w.sessionStore.IndexMessage(context.Background(), session, msgIdx, core.Message{Role: "user", Content: content, Timestamp: time.Now()})
		}
	}

	if w.chainExecutor != nil && w.chainRouter != nil {
		w.handleChatWithChain(content, session)
		return
	}

	w.handleChatLegacy(content, session)
}

func (w *wsConn) handleChatWithChain(content string, session *core.Session) {
	history := buildHistoryAsMap(session)
	metadata := map[string]string{"channel": "webchat"}
	state := chain.NewChainState(content, "personal", metadata)
	state.Data["conversation_history"] = history

	streamCh := make(chan chain.StreamToken, 50)
	state.Stream = streamCh
	state.StreamMode = true

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	go func() {
		result, err := w.chainExecutor.Run(ctx, "chat", state)
		if err != nil {
			w.logger.Error("chain async error", zap.Error(err))
		} else if result != nil && result.Error != nil {
			w.logger.Error("chain async step error", zap.Error(result.Error))
		}
		for _, t := range state.Trace {
			w.logger.Debug("chain step trace",
				zap.String("step", t.StepName),
				zap.Duration("duration", t.Duration),
				zap.String("status", t.Status),
			)
		}
		close(streamCh)
	}()

	var fullResponse strings.Builder
	for token := range streamCh {
		switch {
		case token.Sources != nil:
			var srcList []string
			for _, s := range token.Sources {
				srcList = append(srcList, s.Title)
			}
			w.writeJSON(serverMessage{Type: "context", Content: "\u68c0\u7d22\u5230\u76f8\u5173\u9875\u9762: " + strings.Join(srcList, ", ")})
		case token.Error != "":
			w.writeJSON(serverMessage{Type: "error", Message: token.Error})
		case token.Text != "":
			fullResponse.WriteString(token.Text)
			w.writeJSON(serverMessage{Type: "stream", Content: token.Text})
		}
	}

	responseText := fullResponse.String()
	if responseText == "" {
		responseText = state.FinalAnswer
	}
	if responseText == "" {
		sourceCount, _ := state.Get("source_count")
		mergedCount, _ := state.Get("merged_count")
		sysPrompt, _ := state.GetString("system_prompt")
		sysPreview := ""
		if len(sysPrompt) > 200 {
			sysPreview = sysPrompt[:200] + "..."
		} else {
			sysPreview = sysPrompt
		}
		debugRaw, _ := state.GetString("_llm_raw_response")
		errStr := ""
		if llmErr, ok := state.GetString("_llm_error"); ok && llmErr != "" {
			errStr += "\nllm error: " + llmErr
		}
		responseText = "\u62b1\u6b49\uff0c\u6211\u6682\u65f6\u65e0\u6cd5\u56de\u7b54\u8fd9\u4e2a\u95ee\u9898\u3002\n\n[debug] sources=" + fmt.Sprintf("%v", sourceCount) + " merged=" + fmt.Sprintf("%v", mergedCount) + " sys_prompt_len=" + fmt.Sprintf("%d", len(sysPrompt)) + " llm_raw_len=" + fmt.Sprintf("%d", len(debugRaw)) + errStr + "\nsys_prompt_preview: " + sysPreview
	}

	metadata["platform"] = "webchat"
	filtered, _ := w.filter.Apply(responseText, metadata)

	w.sessionMgr.AddMessage(session, core.Message{
		Role: "assistant", Content: filtered, Timestamp: time.Now(),
	})

	if w.sessionStore != nil {
		go func() {
			if err := w.sessionStore.SaveSession(w.session); err != nil {
				w.logger.Warn("session save failed", zap.Error(err))
			}
		}()
	}

	w.writeJSON(serverMessage{Type: "response", Content: filtered})
}

func (w *wsConn) handleChatLegacy(content string, session *core.Session) {
	sources := w.retrieveVaultContext(content)

	history := buildHistoryFromSession(session)
	reqBody := buildWSRequestWithContext(history, content, sources)

	metadata := map[string]string{"channel": "webchat"}
	decision := w.router.Decide(reqBody, "auto", metadata)
	reqBody = setModel(reqBody, decision.TargetModel)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	rawResp, err := w.infer.Chat(ctx, reqBody)
	if err != nil {
		w.logger.Error("ws chat failed", zap.Error(err))
		w.writeJSON(serverMessage{Type: "error", Message: fmt.Sprintf("inference error: %v", err)})
		return
	}

	responseText, err := extractWSResponse(rawResp)
	if err != nil {
		w.logger.Error("ws parse response failed", zap.Error(err))
		w.writeJSON(serverMessage{Type: "error", Message: "failed to parse response"})
		return
	}

	metadata["platform"] = "webchat"
	filtered, _ := w.filter.Apply(responseText, metadata)

	w.sessionMgr.AddMessage(session, core.Message{
		Role: "assistant", Content: filtered, Timestamp: time.Now(),
	})

	// Persist session to disk (survives restart).
	if w.sessionStore != nil {
		go func() {
			if err := w.sessionStore.SaveSession(w.session); err != nil {
				w.logger.Warn("session save failed", zap.Error(err))
			}
		}()
	}

	if len(sources) > 0 {
		var srcList []string
		for _, s := range sources {
			srcList = append(srcList, s.Title)
		}
		w.writeJSON(serverMessage{Type: "context", Content: "\u68c0\u7d22\u5230\u76f8\u5173\u9875\u9762: " + strings.Join(srcList, ", ")})
	}

	w.writeJSON(serverMessage{Type: "response", Content: filtered})
}

func buildHistoryAsMap(s *core.Session) []map[string]string {
	clone := core.CloneSession(s)
	history := make([]map[string]string, len(clone.Messages))
	for i, m := range clone.Messages {
		history[i] = map[string]string{"role": m.Role, "content": m.Content}
	}
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	return history
}

func (w *wsConn) retrieveVaultContext(query string) []vaultSource {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bm25Results, _ := w.vaultR.Search(ctx, "personal", query)

	embedResults, embedErr := w.embedStore.Search(ctx, query, 10)

	merged := rrfFuse(bm25Results, embedResults)

	var sources []vaultSource
	for i, m := range merged {
		if i >= 5 {
			break
		}
		var body string
		var sourceTitle string

		if m.chunkContent != "" {
			body = m.chunkContent
			if m.sectionTitle != "" {
				sourceTitle = m.title + " > " + m.sectionTitle
			} else {
				sourceTitle = m.title
			}
		} else {
			page, err := w.vaultR.ReadPage(ctx, "personal", m.path)
			if err != nil {
				w.logger.Warn("vault read page failed", zap.String("path", m.path), zap.Error(err))
				continue
			}
			if isInternalPage(page) {
				continue
			}
			body = findBestChunkByTokens(page, query)
			sourceTitle = page.Title
		}

		if len([]rune(body)) > 800 {
			body = string([]rune(body)[:800]) + "..."
		}
		sources = append(sources, vaultSource{Title: sourceTitle, Body: body})
	}

	if embedErr != nil {
		w.logger.Warn("embedding search error", zap.Error(embedErr))
	}
	w.logger.Debug("rag retrieval",
		zap.Int("bm25_results", len(bm25Results)),
		zap.Int("embed_results", len(embedResults)),
		zap.Int("merged", len(merged)),
		zap.Int("sources", len(sources)),
	)

	return sources
}

func findBestChunkByTokens(page *vault.Page, query string) string {
	chunks := vault.ChunkPage(page)
	if len(chunks) == 0 {
		return page.Body
	}
	queryRunes := []rune(strings.ToLower(query))
	bestScore := 0
	bestContent := page.Body
	for _, c := range chunks {
		contentRunes := []rune(strings.ToLower(c.Content))
		score := 0
		for i := 0; i < len(queryRunes)-1; i++ {
			window := string(queryRunes[i : i+2])
			if strings.Contains(string(contentRunes), window) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestContent = c.Content
		}
	}
	return bestContent
}

const rrfK = 60

type rrfEntry struct {
	path         string
	title        string
	score        float64
	chunkContent string
	sectionTitle string
}

func rrfFuse(bm25 []vault.SearchResult, embed []EmbeddingResult) []rrfEntry {
	scores := make(map[string]float64)
	titles := make(map[string]string)
	chunkContent := make(map[string]string)
	sectionTitle := make(map[string]string)

	for i, r := range bm25 {
		scores[r.Path] += 1.0 / float64(rrfK+i+1)
		titles[r.Path] = r.Title
	}
	for i, r := range embed {
		scores[r.PagePath] += 1.0 / float64(rrfK+i+1)
		if titles[r.PagePath] == "" {
			titles[r.PagePath] = r.Title
		}
		if r.ChunkContent != "" {
			chunkContent[r.PagePath] = r.ChunkContent
			sectionTitle[r.PagePath] = r.SectionTitle
		}
	}

	var entries []rrfEntry
	for path, score := range scores {
		entries = append(entries, rrfEntry{
			path:         path,
			title:        titles[path],
			score:        score,
			chunkContent: chunkContent[path],
			sectionTitle: sectionTitle[path],
		})
	}
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].score > entries[i].score {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	return entries
}

func isInternalPage(page *vault.Page) bool {
	for _, tag := range page.Tags {
		if tag == "visibility/internal" || tag == "internal" {
			return true
		}
	}
	if page.Category == "concept" || page.Category == "concepts" {
		for _, tag := range page.Tags {
			if tag == "rag" || tag == "user-facing" || tag == "knowledge-base" || tag == "game" {
				return false
			}
		}
		return true
	}
	return false
}

func (w *wsConn) writeJSON(v interface{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := w.conn.WriteJSON(v); err != nil {
		w.logger.Warn("ws write error", zap.Error(err))
	}
}

func buildHistoryFromSession(s *core.Session) []wsMessage {
	clone := core.CloneSession(s)
	history := make([]wsMessage, len(clone.Messages))
	for i, m := range clone.Messages {
		history[i] = wsMessage{Role: m.Role, Content: m.Content}
	}
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	return history
}

type wsMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func buildWSRequestWithContext(history []wsMessage, currentContent string, sources []vaultSource) []byte {
	messages := make([]map[string]string, 0, len(history)+2)

	if len(sources) > 0 {
		var ctx strings.Builder
		ctx.WriteString("\u4f60\u53ef\u53c2\u8003\u4ee5\u4e0b\u77e5\u8bc6\u5e93\u5185\u5bb9\u56de\u7b54\u95ee\u9898:\n\n")
		for _, s := range sources {
			ctx.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", s.Title, s.Body))
		}
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": ctx.String(),
		})
	}

	for _, m := range history {
		messages = append(messages, map[string]string{
			"role": m.Role, "content": m.Content,
		})
	}
	if len(messages) > 0 && messages[len(messages)-1]["role"] == "user" {
		messages[len(messages)-1]["content"] = currentContent
	}

	req := map[string]interface{}{
		"model":    "auto",
		"messages": messages,
	}
	data, _ := json.Marshal(req)
	return data
}

func extractWSResponse(raw json.RawMessage) (string, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return resp.Choices[0].Message.Content, nil
}

func setModel(body []byte, model string) []byte {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	m["model"] = model
	data, _ := json.Marshal(m)
	return data
}

func newSessionID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sessionToConv(s *core.Session) *memory.Conversation {
	messages := make([]memory.ConvMessage, len(s.Messages))
	for i, m := range s.Messages {
		messages[i] = memory.ConvMessage{Role: m.Role, Content: m.Content}
	}
	return &memory.Conversation{
		Messages:  messages,
		ChannelID: s.ChannelID,
		UserID:    s.UserID,
		StartedAt: s.StartedAt,
		EndedAt:   s.LastActiveAt,
	}
}