package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
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
	CheckOrigin:     sameOrigin,
}

type wsConn struct {
	conn       *websocket.Conn
	logger     *zap.Logger
	infer      inference.Client
	vaultR     vault.Reader
	router     *core.ModelRouter
	filter     *filter.Chain
	sessionMgr *core.SessionManager
	embedStore *vault.EmbeddingStore

	chainExecutor *chain.ChainExecutor
	chainRouter   *chain.ChainRouter

	sessionStore *core.SessionStore
	sedimenter   *memory.Sedimenter

	sessionID string
	session   *core.Session
	owner     string
	ctx       context.Context
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

func handleWebSocket(logger *zap.Logger, infer inference.Client, vr vault.Reader, router *core.ModelRouter, fc *filter.Chain, sessionMgr *core.SessionManager,
	embedStore *vault.EmbeddingStore, chainExecutor *chain.ChainExecutor, chainRouter *chain.ChainRouter, sessionStore *core.SessionStore, sedimenter *memory.Sedimenter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := requestPrincipal(r)
		if p.Owner == "" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Warn("ws upgrade failed", zap.Error(err))
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		// net/http does not close hijacked connections during Shutdown.
		// Tie each socket to the server's BaseContext and close idle reads too.
		go func() {
			<-ctx.Done()
			conn.Close()
		}()
		wsc := &wsConn{
			conn:          conn,
			owner:         p.Owner,
			ctx:           ctx,
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
			sedimenter:    sedimenter,
		}
		conn.SetReadLimit(1024 * 1024)
		wsc.loop()
	}
}

func (w *wsConn) loop() {
	defer func() {
		w.conn.Close()
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
			if w.sessionStore == nil {
				w.writeJSON(serverMessage{Type: "error", Message: "session store unavailable"})
				return
			}
			create := msg.SessionID == ""
			if msg.SessionID != "" {
				w.sessionID = msg.SessionID
			} else {
				w.sessionID = newSessionID()
			}
			if w.sessionID == "" {
				w.writeJSON(serverMessage{Type: "error", Message: "session unavailable"})
				return
			}
			w.session, err = w.sessionStore.BrowserSession("webchat", w.sessionID, w.owner, create)
			if err != nil {
				w.sessionID = ""
				w.writeJSON(serverMessage{Type: "error", Message: "session not found or unavailable"})
				continue
			}
			w.logger.Info("webchat session started", zap.String("session_id", w.sessionID))
			w.writeJSON(serverMessage{Type: "session", SessionID: w.sessionID})
		}

		w.handleChat(msg.Content)
	}
}

func (w *wsConn) handleChat(content string) {
	session, err := w.sessionStore.BrowserSession("webchat", w.sessionID, w.owner, false)
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
		w.sessionID = newSessionID()
		newSession, err := w.sessionStore.BrowserSession("webchat", w.sessionID, w.owner, true)
		if err != nil {
			w.writeJSON(serverMessage{Type: "error", Message: "session error"})
			return
		}
		session = newSession
		w.session = newSession
		w.writeJSON(serverMessage{Type: "session", SessionID: w.sessionID})
		w.sessionMgr.AddMessage(session, core.Message{
			Role: "user", Content: content, Timestamp: time.Now(),
		})

	}

	if w.chainExecutor != nil && w.chainRouter != nil {
		clone := core.CloneSession(session)
		if len(clone.Messages) > 0 {
			index := len(clone.Messages) - 1
			msg := clone.Messages[index]
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := w.sessionStore.IndexMessage(ctx, session, index, msg); err != nil {
					w.logger.Warn("session indexing failed", zap.Error(err))
				}
			}()
		}
		w.handleChatWithChain(content, session)
		return
	}

	if w.chainExecutor == nil || w.chainRouter == nil {
		w.writeJSON(serverMessage{Type: "error", Message: "agent not available"})
		return
	}
}

func (w *wsConn) handleChatWithChain(content string, session *core.Session) {
	history := buildHistoryAsMap(session)
	metadata := map[string]string{"channel": "webchat"}
	state := chain.NewChainState(content, "agent", metadata)
	state.Data["conversation_history"] = history

	// Buffer complete model output before releasing any text across the privacy boundary.
	ctx, cancel := context.WithTimeout(w.ctx, 300*time.Second)
	defer cancel()

	result, err := w.chainExecutor.Run(ctx, "chat", state)
	if err == nil && result != nil {
		err = result.Error
	}
	if err != nil || result == nil {
		w.logger.Warn("chat failed", zap.Error(err))
		w.writeJSON(serverMessage{Type: "error", Message: "chat generation failed"})
		return
	}
	responseText := result.FinalAnswer
	if responseText == "" {
		w.writeJSON(serverMessage{Type: "error", Message: "empty model response"})
		return
	}

	metadata["platform"] = "webchat"
	filtered, records := w.filter.Apply(responseText, metadata)
	for _, record := range records {
		w.logger.Info("output filtered", zap.String("channel", "webchat"), zap.String("filter", record.Filter))
	}

	w.sessionMgr.AddMessage(session, core.Message{
		Role: "assistant", Content: filtered, Timestamp: time.Now(),
	})

	if w.sessionStore != nil {
		if err := w.sessionStore.SaveSession(session); err != nil {
			w.logger.Warn("session save failed", zap.Error(err))
			w.writeJSON(serverMessage{Type: "error", Message: "conversation could not be saved"})
			return
		}
	}

	w.writeJSON(serverMessage{Type: "response", Content: filtered})
}

func buildHistoryAsMap(s *core.Session) []map[string]string {
	clone := core.CloneSession(s)
	if len(clone.Messages) > 0 && clone.Messages[len(clone.Messages)-1].Role == "user" {
		clone.Messages = clone.Messages[:len(clone.Messages)-1]
	}
	history := make([]map[string]string, len(clone.Messages))
	for i, m := range clone.Messages {
		history[i] = map[string]string{"role": m.Role, "content": m.Content}
	}
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	return history
}

func (w *wsConn) writeJSON(v interface{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := w.conn.WriteJSON(v); err != nil {
		w.logger.Warn("ws write error", zap.Error(err))
	}
}

func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
