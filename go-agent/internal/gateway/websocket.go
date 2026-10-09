package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"strings"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     sameOrigin,
}

type wsConn struct {
	requestID  string
	options    webChatOptions
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

// These options are server-owned and never decoded from client messages.
type webChatOptions struct {
	ChannelID, VaultName string
	ControlChat          *core.ControlChat
	AuthorizeControl     func(context.Context) error
	IndexMessages        bool
	ValidateSession      func(context.Context) error
	WithSession          func(context.Context, func(context.Context) error) error
}

type clientMessage struct {
	RequestID string `json:"request_id,omitempty"`
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
}

type serverMessage struct {
	RequestID    string                       `json:"request_id,omitempty"`
	Proposal     *smarthome.ControlProposal   `json:"proposal,omitempty"`
	DeviceResult *smarthome.DeviceQueryResult `json:"device_result,omitempty"`
	Code         string                       `json:"code,omitempty"`
	Type         string                       `json:"type"`
	Content      string                       `json:"content,omitempty"`
	Message      string                       `json:"message,omitempty"`
	SessionID    string                       `json:"session_id,omitempty"`
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
			options:       webChatOptions{ChannelID: "webchat", VaultName: "agent", IndexMessages: true},
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

		if len(msg.RequestID) > 128 || strings.ContainsAny(msg.RequestID, " /\\\t\r\n") {
			w.writeJSON(serverMessage{Type: "error", Code: "invalid_request", Message: "invalid request id"})
			continue
		}
		w.requestID = msg.RequestID
		if w.requestID == "" {
			w.requestID = newSessionID()
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
			w.session, err = w.sessionStore.BrowserSession(w.options.ChannelID, w.sessionID, w.owner, create)
			if err != nil {
				w.writeSessionError(w.sessionID, err)
				w.sessionID = ""
				continue
			}
			w.logger.Info("webchat session started", zap.String("session_id", w.sessionID))
			w.writeJSON(serverMessage{Type: "session", SessionID: w.sessionID})
		}

		w.handleChat(msg.Content)
	}
}

func (w *wsConn) handleChat(content string) {
	w.handleChatContext(w.ctx, content)
}

// Keep the connection lifetime separate from a caller's generation deadline.
// The normal entry uses w.ctx; the chain limit still starts after turn acquisition.
func (w *wsConn) handleChatContext(generationContext context.Context, content string) {
	release, err := w.sessionMgr.AcquireTurn(w.ctx, w.options.ChannelID, w.sessionID)
	if err != nil {
		return
	}
	defer release()
	if w.validateSession() != nil {
		return
	}
	session, err := w.sessionStore.BrowserSession(w.options.ChannelID, w.sessionID, w.owner, false)
	if err != nil {
		w.logger.Error("webchat session create failed", zap.Error(err))
		w.writeSessionError(w.sessionID, err)
		return
	}
	w.session = session
	if w.options.ControlChat != nil && w.handleControlReplay(generationContext, content, session) {
		return
	}

	if ok := w.sessionMgr.AddMessage(session, core.Message{
		Role: "user", Content: content, Timestamp: time.Now(),
	}); !ok {
		w.sessionMgr.EndSession(session)
		nextID := newSessionID()
		if nextID == "" {
			w.writeSessionError(w.sessionID, errors.New("session identity unavailable"))
			return
		}
		// Reserve the fresh identity before publishing it to any other connection.
		nextRelease, err := w.sessionMgr.AcquireTurn(w.ctx, w.options.ChannelID, nextID)
		if err != nil {
			return
		}
		defer nextRelease()
		if w.validateSession() != nil {
			return
		}
		newSession, err := w.sessionStore.BrowserSession(w.options.ChannelID, nextID, w.owner, true)
		if err != nil {
			w.writeSessionError(w.sessionID, err)
			return
		}
		w.sessionID = nextID
		session = newSession
		w.session = newSession
		w.writeJSON(serverMessage{Type: "session", SessionID: w.sessionID})
		w.sessionMgr.AddMessage(session, core.Message{
			Role: "user", Content: content, Timestamp: time.Now(),
		})

	}

	if w.chainExecutor != nil && w.chainRouter != nil {
		clone := core.CloneSession(session)
		if w.options.IndexMessages && len(clone.Messages) > 0 {
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
		if w.options.ControlChat != nil && w.handleControlChat(generationContext, content, session) {
			return
		}
		w.handleChatWithChain(generationContext, content, session)
		return
	}

	if w.chainExecutor == nil || w.chainRouter == nil {
		w.writeJSON(serverMessage{Type: "error", Message: "agent not available"})
		return
	}
}

func (w *wsConn) handleChatWithChain(generationContext context.Context, content string, session *core.Session) {
	history := buildHistoryAsMap(session)
	metadata := map[string]string{"channel": w.options.ChannelID}
	state := chain.NewChainState(content, w.options.VaultName, metadata)
	state.Data["conversation_history"] = history

	// Buffer complete model output before releasing any text across the privacy boundary.
	ctx, cancel := context.WithTimeout(generationContext, 300*time.Second)
	defer cancel()

	result, err := w.chainExecutor.Run(ctx, "chat", state)
	if ctx.Err() != nil {
		// A generation deadline ends this turn, not the socket or conversation.
		// Use the live login/connection context to publish the terminal error.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && w.ctx.Err() == nil {
			publish := func(ctx context.Context) error {
				return w.writeJSONContext(ctx, serverMessage{Type: "error", Message: "chat generation timed out"})
			}
			if w.options.WithSession != nil {
				_ = w.options.WithSession(w.ctx, publish)
			} else if w.validateSession() == nil {
				_ = publish(w.ctx)
			}
		}
		return
	}
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

	metadata["platform"] = w.options.ChannelID
	filtered, records := w.filter.Apply(responseText, metadata)
	for _, record := range records {
		w.logger.Info("output filtered", zap.String("channel", w.options.ChannelID), zap.String("filter", record.Filter))
	}
	commit := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !w.sessionMgr.AddMessage(session, core.Message{
			Role: "assistant", Content: filtered, Timestamp: time.Now(),
		}) {
			return errors.New("session ended before response")
		}

		if w.sessionStore != nil {
			if err := w.sessionStore.SaveSession(session); err != nil {
				w.logger.Warn("session save failed", zap.Error(err))
				return err
			}
		}

		return w.writeJSONContext(ctx, serverMessage{Type: "response", Content: filtered})
	}
	if w.options.WithSession != nil {
		err = w.options.WithSession(ctx, commit)
	} else if err = w.validateSession(); err == nil {
		err = commit(ctx)
	}
	if err != nil && w.ctx.Err() == nil {
		w.writeJSON(serverMessage{Type: "error", Message: "conversation could not be saved or published"})
	}
}

func (w *wsConn) validateSession() error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.options.ValidateSession != nil {
		return w.options.ValidateSession(w.ctx)
	}
	return nil
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
	_ = w.writeJSONContext(w.ctx, v)
}

func (w *wsConn) writeJSONContext(ctx context.Context, v interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	if expiry, ok := ctx.Deadline(); ok && expiry.Before(deadline) {
		deadline = expiry
	}
	if err := w.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if err := w.conn.WriteJSON(v); err != nil {
		w.logger.Warn("ws write error", zap.Error(err))
		return err
	}
	return nil
}

func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// Identity failures alone allow the client to discard its stale conversation ID.
func (w *wsConn) writeSessionError(id string, err error) {
	code := "session_unavailable"
	if errors.Is(err, core.ErrSessionNotFound) {
		code = "session_not_found"
		if w.sessionID == id {
			w.sessionID = ""
		}
	}
	w.writeJSON(serverMessage{Type: "error", Code: code, SessionID: id, Message: "session not found or unavailable"})
}
