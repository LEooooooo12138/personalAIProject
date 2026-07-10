package wecom

import (
	"context"
	"encoding/xml"
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
			zap.String("query", r.URL.RawQuery),
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
	h.server = &http.Server{Addr: addr, Handler: mux}
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

	h.logger.Info("callback verification request",
		zap.String("signature", signature),
		zap.String("timestamp", timestamp),
		zap.String("nonce", nonce),
	)

	if h.crypto.VerifySignature(signature, timestamp, nonce, echostr) {
		decrypted, err := h.crypto.Decrypt(echostr)
		if err != nil {
			h.logger.Error("decrypt echostr failed", zap.Error(err))
			http.Error(w, "decrypt failed", http.StatusBadRequest)
			return
		}
		h.logger.Info("verification successful", zap.String("echostr", string(decrypted)))
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
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("read body failed", zap.Error(err))
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	h.logger.Info("callback message received",
		zap.Int("body_len", len(body)),
		zap.String("body_preview", string(body[:200])),
	)

	var encryptMsg struct {
		Encrypt string `xml:"Encrypt"`
	}
	if err := xml.Unmarshal(body, &encryptMsg); err != nil {
		h.logger.Error("parse xml failed", zap.Error(err), zap.ByteString("body", body))
		http.Error(w, "parse xml failed", http.StatusBadRequest)
		return
	}

	signature := r.URL.Query().Get("msg_signature")
	timestamp := r.URL.Query().Get("timestamp")
	nonce := r.URL.Query().Get("nonce")

	h.logger.Info("callback message details",
		zap.String("signature", signature),
		zap.String("timestamp", timestamp),
		zap.String("nonce", nonce),
		zap.String("encrypt_len", encryptMsg.Encrypt),
	)

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

	h.logger.Info("message decrypted", zap.String("decrypted", string(decrypted)))

	var msg WeComMessage
	if err := xml.Unmarshal(decrypted, &msg); err != nil {
		h.logger.Error("parse message failed", zap.Error(err), zap.ByteString("decrypted", decrypted))
		http.Error(w, "parse message failed", http.StatusBadRequest)
		return
	}

	h.logger.Info("decoded wecom message",
		zap.String("from_user", msg.FromUserName),
		zap.String("msg_type", msg.MsgType),
		zap.String("content", msg.Content),
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

	msgCh <- chMsg
	w.Write([]byte("success"))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

