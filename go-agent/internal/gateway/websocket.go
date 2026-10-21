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

	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/filter"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/memory"
	"github.com/yuanleyao/ai-agent/internal/persona"
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
	entry       ChatEntry
	observation *chatObservation
	requestID   string
	options     webChatOptions
	conn        *websocket.Conn
	logger      *zap.Logger
	infer       inference.Client
	vaultR      vault.Reader
	router      *core.ModelRouter
	filter      *filter.Chain
	sessionMgr  *core.SessionManager
	embedStore  *vault.EmbeddingStore

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
	Presenter            *persona.Presenter
	AssistantConfig      *assistantconfig.Store
	ChannelID, VaultName string
	ControlChat          *core.ControlChat
	AuthorizeControl     func(context.Context) error
	IndexMessages        bool
	ValidateSession      func(context.Context) error
	WithSession          func(context.Context, func(context.Context) error) error
}

type clientMessage struct {
	Entry     ChatEntry `json:"entry,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	SessionID string    `json:"session_id"`
	Content   string    `json:"content"`
}

type serverMessage struct {
	OmittedHistoryPairs int                          `json:"omitted_history_pairs,omitempty"`
	Stage               ChatStage                    `json:"stage,omitempty"`
	EventID             string                       `json:"event_id,omitempty"`
	PersonalityRevision string                       `json:"personality_revision,omitempty"`
	PresentationVersion string                       `json:"presentation_version,omitempty"`
	Outcome             string                       `json:"outcome,omitempty"`
	RequestID           string                       `json:"request_id,omitempty"`
	Proposal            *smarthome.ControlProposal   `json:"proposal,omitempty"`
	DeviceResult        *smarthome.DeviceQueryResult `json:"device_result,omitempty"`
	Code                string                       `json:"code,omitempty"`
	Type                string                       `json:"type"`
	Content             string                       `json:"content,omitempty"`
	Message             string                       `json:"message,omitempty"`
	SessionID           string                       `json:"session_id,omitempty"`
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
	ctx, cancel := context.WithCancel(w.ctx)
	w.ctx = ctx
	// Keep reading while the single dispatcher waits for a session turn or model.
	// Otherwise a peer close cannot cancel queued work until it starts publishing.
	// Bound pipelined input; an overflowing peer is disconnected instead of
	// blocking the only reader or growing an unbounded per-connection backlog.
	incoming := make(chan []byte, 16)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer w.conn.Close()
		defer cancel()
		for {
			_, raw, err := w.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					w.logger.Warn("ws read error", zap.Error(err))
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case incoming <- raw:
			default:
				return
			}
		}
	}()
	defer func() {
		cancel()
		w.conn.Close()
		<-readDone
	}()

	for {
		var raw []byte
		select {
		case <-ctx.Done():
			return
		case raw = <-incoming:
		}
		if ctx.Err() != nil {
			return
		}
		var err error

		var msg clientMessage
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Content == "" {
			w.writeJSON(serverMessage{Type: "error", Code: "invalid_request", Outcome: "failed", Message: "invalid message"})
			continue
		}

		if len(msg.RequestID) > 128 || strings.ContainsAny(msg.RequestID, " /\\\t\r\n") {
			w.writeJSON(serverMessage{Type: "error", Code: "invalid_request", Outcome: "failed", Message: "invalid request id"})
			continue
		}

		entry, entryErr := parseChatEntry(string(msg.Entry))
		if entryErr != nil || (entry == ChatEntryChat && msg.RequestID == "") {
			code, message := "invalid_chat_entry", "不支持此聊天入口。"
			if entryErr == nil {
				code, message = "request_id_required", "普通聊天需要请求标识，请重新发起请求。"
			}
			_ = w.publishChatFrame(w.ctx, serverMessage{Type: "error", Code: code, Outcome: "failed", Message: message, SessionID: w.sessionID, RequestID: msg.RequestID})
			continue
		}
		w.entry = entry
		w.requestID = msg.RequestID
		if w.requestID == "" {
			w.requestID = newSessionID()
		}
		if w.sessionID == "" {
			if w.sessionStore == nil {
				w.writeJSON(serverMessage{Type: "error", Code: "session_unavailable", Outcome: "failed", Message: "session store unavailable"})
				return
			}
			create := msg.SessionID == ""
			if msg.SessionID != "" {
				w.sessionID = msg.SessionID
			} else {
				w.sessionID = newSessionID()
			}
			if w.sessionID == "" {
				w.writeJSON(serverMessage{Type: "error", Code: "session_unavailable", Outcome: "failed", Message: "session unavailable"})
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
// The whole-round budget includes queueing; the socket remains live for terminal errors.
func (w *wsConn) handleChatContext(generationContext context.Context, content string) {
	round, cancel := context.WithTimeout(generationContext, 300*time.Second)
	defer cancel()
	// Even a caller-supplied shorter budget must remain bound to login lifetime.
	stop := context.AfterFunc(w.ctx, cancel)
	defer stop()
	round, observation := startChatObservation(round, w.logger)
	w.observation = observation
	defer func() { observation.finish(round); w.observation = nil }()
	generationContext = round
	if w.requestID == "" {
		w.requestID = newSessionID()
	}
	queueDone := measureChat(round, "queue")
	defer queueDone()
	if err := w.publishChatStage(round, ChatStageQueued); err != nil {
		w.publishChatTimeout(round)
		return
	}
	release, err := w.sessionMgr.AcquireTurn(round, w.options.ChannelID, w.sessionID)
	queueDone()
	if err != nil {
		w.publishChatTimeout(round)
		return
	}
	defer release()
	if w.validateSession() != nil {
		return
	}
	if err := w.publishChatStage(round, ChatStagePreparing); err != nil {
		w.publishChatTimeout(round)
		return
	}
	session, err := w.sessionStore.BrowserSession(w.options.ChannelID, w.sessionID, w.owner, false)
	if err != nil {
		w.logger.Error("webchat session create failed", zap.Error(err))
		w.writeSessionError(w.sessionID, err)
		return
	}
	w.session = session
	settingsDone := measureChat(round, "settings")
	pinned := w.pinAssistantMode(session)
	settingsDone()
	if !pinned {
		return
	}
	if !w.bindChatEntry(round, session, content) {
		return
	}
	if w.entry != ChatEntryChat && w.options.ControlChat != nil && w.handleControlReplay(generationContext, content, session) {
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
		nextRelease, err := w.sessionMgr.AcquireTurn(round, w.options.ChannelID, nextID)
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
		if !w.pinAssistantMode(session) {
			return
		}
		if !w.bindChatEntry(round, session, content) {
			return
		}
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
		if w.entry != ChatEntryChat && w.options.ControlChat != nil && w.handleControlChat(generationContext, content, session) {
			return
		}
		w.handleChatWithChain(generationContext, content, session)
		return
	}

	if w.chainExecutor == nil || w.chainRouter == nil {
		w.writeJSON(serverMessage{Type: "error", Code: "inference_unavailable", Outcome: "failed", Message: "agent not available"})
		return
	}
}

func (w *wsConn) handleChatWithChain(generationContext context.Context, content string, session *core.Session) {
	history := buildHistoryAsMap(session)
	mode, key, route := "", "", "chat"
	endpoint, personality := "", ""
	if w.options.AssistantConfig != nil {
		settingsDone := measureChat(generationContext, "settings")
		settings, storedKey, err := w.options.AssistantConfig.Snapshot()
		settingsDone()
		if err != nil {
			w.writeJSON(serverMessage{Type: "error", Code: "assistant_settings_unavailable", Outcome: "failed", Message: "assistant settings unavailable"})
			return
		}
		personalityDone := measureChat(generationContext, "personality")
		personality, err = w.options.AssistantConfig.PersonalityContent()
		personalityDone()
		if err != nil {
			w.writeJSON(serverMessage{Type: "error", Code: "personality_unavailable", Outcome: "failed", Message: "personality file unavailable"})
			return
		}
		mode, key = core.CloneSession(session).AssistantMode, storedKey
		endpoint = settings.MiMoEndpoint
		if mode == "cloud" || mode == "hybrid" {
			route = "direct"
			history = buildCloudHistory(session)
		}
	}
	metadata := map[string]string{"channel": w.options.ChannelID}
	state := chain.NewChainState(content, w.options.VaultName, metadata)
	state.Data["conversation_history"] = history
	state.Data["assistant_personality"] = personality
	if mode == "cloud" || mode == "hybrid" {
		// Keep the established twenty-message limit. A production byte cap awaits
		// real provider measurements; synthetic assembly timings cannot set it.
		state.Data["chat_budget"] = chain.ChatBudget{MaxInputBytes: 0, MaxHistoryMessages: 20}
	}

	// Buffer complete model output before releasing any text across the privacy boundary.
	ctx := generationContext
	if mode != "" {
		ctx = inference.WithAssistantRoute(ctx, mode, key, endpoint)
	}

	if err := w.publishChatStage(ctx, ChatStageGenerating); err != nil {
		w.publishChatTimeout(ctx)
		return
	}
	generateDone := measureChat(ctx, "generate")
	result, err := w.chainExecutor.Run(observeChatCalls(ctx, "generate"), route, state)
	generateDone()
	if ctx.Err() != nil {
		w.publishChatTimeout(ctx)
		return
	}
	if err == nil && result != nil {
		err = result.Error
	}
	if err != nil || result == nil {
		w.logger.Warn("chat failed", zap.String("error_class", chatErrorCode(err, "chat_generation_failed")))
		w.writeJSON(serverMessage{Type: "error", Code: chatErrorCode(err, "chat_generation_failed"), Outcome: "failed", Message: "chat generation failed"})
		return
	}
	responseText := result.FinalAnswer
	if responseText == "" {
		w.writeJSON(serverMessage{Type: "error", Code: "inference_invalid_response", Outcome: "failed", Message: "empty model response"})
		return
	}

	metadata["platform"] = w.options.ChannelID
	// Only ordinary conversations can opt into cloud speech. Knowledge and
	// device content, including anything carried forward in local history, stay local.
	messageMode := mode
	if len(state.Sources) > 0 {
		messageMode = ""
	}
	if mode == "local" {
		for _, previous := range core.CloneSession(session).Messages {
			if previous.Role == "assistant" && core.MessageRequiresLocalSpeech(previous) {
				messageMode = ""
				break
			}
		}
	}
	filterDone := measureChat(ctx, "filter")
	filtered, records := w.filter.Apply(responseText, metadata)
	filterDone()
	if strings.TrimSpace(filtered) == "" {
		w.writeJSON(serverMessage{Type: "error", Code: "chat_filter_empty", Outcome: "failed", Message: "chat output unavailable"})
		return
	}
	if err := w.publishChatStage(ctx, ChatStageSaving); err != nil {
		w.publishChatTimeout(ctx)
		return
	}
	for _, record := range records {
		w.logger.Info("output filtered", zap.String("channel", w.options.ChannelID), zap.String("filter", record.Filter))
	}
	commit := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		persistDone := measureChat(ctx, "persist")
		defer persistDone()
		message := core.Message{Role: "assistant", Content: filtered, Timestamp: time.Now(), AssistantMode: messageMode}
		// The turn lease serializes ordinary, binding, and control-event writes.
		// Save a private snapshot first: live history readers must never observe
		// an assistant answer whose persistence failed. Preserve the live user
		// input and bindings on failure; do not roll back the whole session.
		snapshot := core.CloneSession(session)
		if !w.sessionMgr.AddMessage(snapshot, message) {
			return errors.New("session ended before response")
		}
		if w.sessionStore != nil {
			if err := w.sessionStore.SaveSession(snapshot); err != nil {
				w.logger.Warn("session save failed", zap.Error(err))
				return err
			}
		}
		if !w.sessionMgr.AddMessage(session, message) {
			return errors.New("session ended before response")
		}

		persistDone()
		omittedPairs, _ := state.Data["omitted_history_pairs"].(int)
		return w.writeJSONContext(ctx, serverMessage{Type: "response", Content: filtered, SessionID: w.sessionID, RequestID: w.requestID, OmittedHistoryPairs: omittedPairs})
	}
	if w.options.WithSession != nil {
		err = w.options.WithSession(ctx, commit)
	} else if err = w.validateSession(); err == nil {
		err = commit(ctx)
	}
	if err != nil && w.ctx.Err() == nil {
		w.writeJSON(serverMessage{Type: "error", Code: "chat_publish_failed", Outcome: "unknown", Message: "conversation could not be saved or published"})
	}
}

func (w *wsConn) pinAssistantMode(session *core.Session) bool {
	if w.options.AssistantConfig == nil {
		return true
	}
	settings, err := w.options.AssistantConfig.Get()
	if err != nil {
		w.writeJSON(serverMessage{Type: "error", Code: "assistant_settings_unavailable", Outcome: "failed", Message: "assistant settings unavailable"})
		return false
	}
	session.PinAssistantMode(string(settings.LLMMode))
	return true
}

func buildCloudHistory(session *core.Session) []map[string]string {
	messages := core.CloneSession(session).Messages
	history := []map[string]string{}
	for i := 1; i < len(messages); i++ {
		user, reply := messages[i-1], messages[i]
		if user.Role != "user" || reply.Role != "assistant" || len(reply.Attachments) != 0 || reply.DeviceResult != nil || reply.EventID != "" || (reply.AssistantMode != "cloud" && reply.AssistantMode != "hybrid") {
			continue
		}
		history = append(history, map[string]string{"role": "user", "content": user.Content}, map[string]string{"role": "assistant", "content": reply.Content})
	}
	// The chain budget applies the count cap after this privacy filter, so its
	// omission count describes only eligible pairs actually removed.
	return history
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
	if frame, ok := v.(serverMessage); ok {
		_ = w.publishChatFrame(w.ctx, frame)
		return
	}
	_ = w.writeJSONContext(w.ctx, v)
}

func (w *wsConn) writeJSONContext(ctx context.Context, v interface{}) error {
	if frame, ok := v.(serverMessage); ok && w.observation != nil && frame.Type != "chat_stage" && frame.Type != "session" {
		publishDone := measureChat(context.WithValue(ctx, chatObservationKey{}, w.observation), "publish")
		defer publishDone()
	}
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
	if frame, ok := v.(serverMessage); ok && w.observation != nil && frame.Type != "chat_stage" && frame.Type != "session" {
		w.observation.mu.Lock()
		if frame.Type == "error" {
			w.observation.terminal = frame.Code
		} else {
			w.observation.terminal = "success"
		}
		w.observation.mu.Unlock()
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
	w.writeJSON(serverMessage{Type: "error", Code: code, Outcome: "failed", SessionID: id, Message: "session not found or unavailable"})
}
