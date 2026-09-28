package wecom

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/yuanleyao/ai-agent/internal/channel"
	"go.uber.org/zap"
)

type CallbackHandler struct {
	crypto  *WeComCallbackCrypto
	contact *ContactFilter
	server  *http.Server
	logger  *zap.Logger
}

func NewCallbackHandler(crypto *WeComCallbackCrypto, contact *ContactFilter, logger *zap.Logger) *CallbackHandler {
	return &CallbackHandler{
		crypto:  crypto,
		contact: contact,
		logger:  logger,
	}
}

func (h *CallbackHandler) Start(ctx context.Context, addr string, msgCh chan<- channel.Message) {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		h.logger.Debug("callback request received",
			zap.String("method", r.Method),
			zap.String("remote", r.RemoteAddr),
			zap.String("path", r.URL.Path),
		)
		switch r.Method {
		case http.MethodGet:
			h.handleVerification(w, r)
		case http.MethodPost:
			h.handleMessage(w, r, msgCh)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	h.server = &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.server.Shutdown(shutdownCtx)
	}()
	if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		h.logger.Error("callback server error", zap.Error(err))
	}
}

func (h *CallbackHandler) Stop() error {
	if h.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return h.server.Shutdown(shutdownCtx)
	}
	return nil
}

func (h *CallbackHandler) handleVerification(w http.ResponseWriter, r *http.Request) {
	echostr := r.URL.Query().Get("echostr")
	signature := r.URL.Query().Get("msg_signature")
	timestamp := r.URL.Query().Get("timestamp")
	nonce := r.URL.Query().Get("nonce")

	h.logger.Info("callback verification request")

	if h.crypto.VerifySignature(signature, timestamp, nonce, echostr) {
		decrypted, err := h.crypto.Decrypt(echostr)
		if err != nil {
			h.logger.Error("decrypt echostr failed", zap.Error(err))
			http.Error(w, "decrypt failed", http.StatusBadRequest)
			return
		}
		h.logger.Info("verification successful")
		w.Write(decrypted)
	} else {
		h.logger.Error("verification signature invalid")
		http.Error(w, "invalid signature", http.StatusForbidden)
	}
}

type WeComMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgId        string   `xml:"MsgId"`
	AgentID      string   `xml:"AgentID"`
}

func (h *CallbackHandler) handleMessage(w http.ResponseWriter, r *http.Request, msgCh chan<- channel.Message) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		h.logger.Error("read body failed", zap.Error(err))
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	h.logger.Info("callback message received",
		zap.Int("body_len", len(body)),
	)

	var encryptMsg struct {
		Encrypt string `xml:"Encrypt"`
	}
	if err := xml.Unmarshal(body, &encryptMsg); err != nil {
		h.logger.Error("parse xml failed", zap.Error(err))
		http.Error(w, "parse xml failed", http.StatusBadRequest)
		return
	}
	if encryptMsg.Encrypt == "" {
		http.Error(w, "missing encrypted message", http.StatusBadRequest)
		return
	}

	signature := r.URL.Query().Get("msg_signature")
	timestamp := r.URL.Query().Get("timestamp")
	nonce := r.URL.Query().Get("nonce")

	if !h.crypto.VerifySignature(signature, timestamp, nonce, encryptMsg.Encrypt) {
		h.logger.Error("signature verification failed")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	decrypted, err := h.crypto.Decrypt(encryptMsg.Encrypt)
	if err != nil {
		h.logger.Error("decrypt message failed", zap.Error(err))
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}

	var msg WeComMessage
	if err := xml.Unmarshal(decrypted, &msg); err != nil {
		h.logger.Error("parse message failed", zap.Error(err))
		http.Error(w, "parse message failed", http.StatusBadRequest)
		return
	}

	h.logger.Info("decoded wecom message",
		zap.String("from_user", msg.FromUserName),
		zap.String("msg_type", msg.MsgType),
		zap.String("msg_id", msg.MsgId),
	)

	// Check whitelist
	if !h.contact.IsAllowed(msg.FromUserName) {
		h.logger.Warn("unauthorized user rejected",
			zap.String("user_id", msg.FromUserName),
		)
		w.Write([]byte("success"))
		return
	}

	h.logger.Info("user authorized, forwarding to agent",
		zap.String("user_id", msg.FromUserName),
	)

	// Convert to internal message format
	chMsg := channel.Message{
		ID:        msg.MsgId,
		ChannelID: "wecom",
		UserID:    msg.FromUserName,
		Content:   msg.Content,
		Metadata: map[string]string{
			"platform": "wecom",
		},
		Timestamp: time.Unix(msg.CreateTime, 0),
	}

	select {
	case msgCh <- chMsg:
		w.Write([]byte("success"))
	case <-r.Context().Done():
		return
	}
}
