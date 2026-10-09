package gateway

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/yuanleyao/ai-agent/internal/console"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

func (s *Server) consoleChatAvailable() bool {
	return s.sessionMgr != nil && s.sessionStore != nil && s.chainExecutor != nil && s.chainRouter != nil && s.filterChain != nil
}

func (s *Server) handleConsoleChat(c *gin.Context) {
	if !s.consoleOriginAllowed(c) {
		return
	}
	if !s.consoleChatAvailable() {
		consoleError(c, 503, "unavailable", "Chat is unavailable")
		return
	}
	token := consoleToken(c.Request)
	ctx, stop, err := s.consoleStore.BindSession(c.Request.Context(), token)
	if err != nil {
		consoleStoreError(c, err)
		return
	}
	defer stop()
	// Binding precedes upgrade. Revocation during the handshake cancels this
	// context, so even a just-upgraded socket is closed and cannot publish.
	if err := ctx.Err(); err != nil {
		consoleStoreError(c, console.ErrUnauthenticated)
		return
	}
	upgrade := websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == s.cfg.Console.PublicOrigin }}
	conn, err := upgrade.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()
	options := webChatOptions{ChannelID: "console", VaultName: "agent", IndexMessages: false,
		ControlChat: s.controlChat,
		AuthorizeControl: func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			p, err := s.consoleStore.Resolve(token)
			if err != nil {
				return err
			}
			if p.Role != "admin" || p.MustChangePassword {
				return smarthome.ErrControlForbidden
			}
			return nil
		},
		ValidateSession: func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			p, err := s.consoleStore.Resolve(token)
			if err == nil && p.MustChangePassword {
				return console.ErrForbidden
			}
			return err
		},
		WithSession: func(ctx context.Context, fn func(context.Context) error) error {
			return s.consoleStore.WithSession(ctx, token, fn)
		},
	}
	wsc := &wsConn{conn: conn, owner: "console:" + requestConsolePrincipal(c.Request).UserID, ctx: ctx, options: options, logger: s.logger, infer: s.infer, vaultR: s.vaultR, router: s.router, filter: s.filterChain, sessionMgr: s.sessionMgr, embedStore: s.embedStore, chainExecutor: s.chainExecutor, chainRouter: s.chainRouter, sessionStore: s.sessionStore, sedimenter: s.sedimenter}
	conn.SetReadLimit(1024 * 1024)
	wsc.loop()
}

func (s *Server) consoleCapabilities(role string, restricted bool) []string {
	capabilities := []string{}
	if restricted {
		return capabilities
	}
	capabilities = append(capabilities, "areas:read")
	if s.controlChat != nil {
		capabilities = append(capabilities, "devices:query")
	}
	if s.control != nil && role == "admin" && len(s.cfg.SmartHome.Control.Targets) > 0 {
		capabilities = append(capabilities, "devices:control")
	}
	if s.vaultR != nil {
		capabilities = append(capabilities, "knowledge:read")
		if role == "admin" {
			capabilities = append(capabilities, "knowledge:manage")
		}
	}
	if s.smartHome != nil {
		capabilities = append(capabilities, "suggestions:read")
		if role == "admin" {
			capabilities = append(capabilities, "suggestions:manage", "collection:read")
		}
	}
	if role == "admin" {
		capabilities = append(capabilities, "members:manage")
	}
	if s.consoleChatAvailable() {
		capabilities = append(capabilities, "chat:use")
	}
	if s.sessionStore != nil {
		capabilities = append(capabilities, "sessions:read")
	}
	return capabilities
}
