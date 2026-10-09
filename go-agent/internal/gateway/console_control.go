package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/console"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

func (s *Server) setupConsoleControlRoutes(g *gin.RouterGroup) {
	g.POST("/control/proposals", s.handleControlPropose)
	g.GET("/control/proposals/:id", s.handleControlGet)
	g.POST("/control/proposals/:id/confirm", s.handleControlConfirm)
	g.POST("/control/proposals/:id/cancel", s.handleControlCancel)
	g.POST("/control/proposals/:id/reconcile", s.handleControlReconcile)
}

func (s *Server) controlContext(c *gin.Context, write bool) (context.Context, func(), bool) {
	if s.control == nil {
		consoleError(c, 503, "control_unavailable", "设备对话服务未配置")
		return nil, nil, false
	}
	p := requestConsolePrincipal(c.Request)
	if write && p.Role != "admin" {
		consoleError(c, 403, "forbidden", "当前账号只能查询设备")
		return nil, nil, false
	}
	token := consoleToken(c.Request)
	ctx, stop, err := s.consoleStore.BindSession(c.Request.Context(), token)
	if err != nil {
		consoleStoreError(c, err)
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	authorize := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := s.consoleStore.Resolve(token)
		if err != nil {
			return err
		}
		if current.UserID != p.UserID || current.MustChangePassword || current.Role != "admin" {
			return smarthome.ErrControlForbidden
		}
		return nil
	}
	return smarthome.WithControlAuthorization(ctx, authorize), func() { cancel(); stop() }, true
}

// This endpoint deliberately has a narrower 400 contract than older Console forms.
func decodeControlJSON(c *gin.Context, dst any) bool {
	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	if err != nil || !json.Valid(data) {
		consoleError(c, 400, "invalid_request", "请求格式无效")
		return false
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		consoleError(c, 400, "invalid_request", "需要JSON对象")
		return false
	}
	// Reject duplicate keys too; do not let two action/ID values mean different things.
	dec := json.NewDecoder(bytes.NewReader(data))
	_, err = dec.Token()
	seen := map[string]bool{}
	for err == nil && dec.More() {
		var tok json.Token
		tok, err = dec.Token()
		if err != nil {
			break
		}
		k, ok := tok.(string)
		if !ok || seen[k] {
			err = errors.New("duplicate key")
			break
		}
		seen[k] = true
		var raw json.RawMessage
		err = dec.Decode(&raw)
	}
	if err != nil {
		consoleError(c, 400, "invalid_request", "请求字段重复或无效")
		return false
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(dst) != nil {
		consoleError(c, 400, "invalid_request", "请求包含无效字段")
		return false
	}
	return true
}

func (s *Server) controlError(c *gin.Context, err error) {
	if _, e := s.consoleStore.Resolve(consoleToken(c.Request)); e != nil {
		consoleStoreError(c, e)
		return
	}
	status, code := 502, "control_failed"
	switch {
	case errors.Is(err, smarthome.ErrControlNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, smarthome.ErrControlForbidden), errors.Is(err, console.ErrForbidden):
		status, code = 403, "forbidden"
	case errors.Is(err, smarthome.ErrControlInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(err, smarthome.ErrControlExpired):
		status, code = 410, "expired"
	case errors.Is(err, smarthome.ErrControlConflict):
		status, code = 409, "conflict"
	case errors.Is(err, smarthome.ErrControlUnsupported):
		status, code = 422, "unsupported"
	case errors.Is(err, smarthome.ErrControlUnavailable), errors.Is(err, smarthome.ErrControlCapacity):
		status, code = 503, "control_unavailable"
	}
	consoleError(c, status, code, "设备请求未完成，请核对状态后操作")
}

func (s *Server) ownedControlActor(c *gin.Context, ctx context.Context) (smarthome.ControlActor, *smarthome.ControlProposal, bool) {
	actor := smarthome.ControlActor{UserID: requestConsolePrincipal(c.Request).UserID}
	proposal, err := s.control.Get(ctx, actor, c.Param("id"))
	if err != nil {
		s.controlError(c, err)
		return actor, nil, false
	}
	actor.SessionID = proposal.SessionID
	if s.sessionStore == nil {
		consoleError(c, 503, "unavailable", "会话服务不可用")
		return actor, nil, false
	}
	if _, err = s.sessionStore.OwnedMessages("console", actor.SessionID, "console:"+actor.UserID); err != nil {
		consoleError(c, 404, "not_found", "未找到提议")
		return actor, nil, false
	}
	return actor, proposal, true
}
func (s *Server) handleControlGet(c *gin.Context) {
	ctx, stop, ok := s.controlContext(c, false)
	if !ok {
		return
	}
	defer stop()
	_, p, ok := s.ownedControlActor(c, ctx)
	if ok {
		s.consoleSmartHomeJSON(c, ctx, p)
	}
}
func (s *Server) handleControlPropose(c *gin.Context) {
	ctx, stop, ok := s.controlContext(c, true)
	if !ok {
		return
	}
	defer stop()
	var b struct {
		SessionID string `json:"session_id"`
		RequestID string `json:"request_id"`
		EntityID  string `json:"entity_id"`
		Action    string `json:"action"`
	}
	if !decodeControlJSON(c, &b) {
		return
	}
	actor := smarthome.ControlActor{UserID: requestConsolePrincipal(c.Request).UserID, SessionID: b.SessionID}
	if s.sessionStore == nil {
		consoleError(c, 503, "unavailable", "会话服务不可用")
		return
	}
	if _, err := s.sessionStore.OwnedMessages("console", actor.SessionID, "console:"+actor.UserID); err != nil {
		consoleError(c, 404, "not_found", "未找到会话")
		return
	}
	outcome, err := s.control.Propose(ctx, actor, b.RequestID, smarthome.ControlIntent{Kind: "on_off", EntityID: b.EntityID, Action: b.Action})
	if err != nil {
		s.controlError(c, err)
		return
	}
	s.consoleSmartHomeJSON(c, ctx, outcome)
}
func (s *Server) handleControlConfirm(c *gin.Context)   { s.controlMutation(c, "confirm") }
func (s *Server) handleControlCancel(c *gin.Context)    { s.controlMutation(c, "cancel") }
func (s *Server) handleControlReconcile(c *gin.Context) { s.controlMutation(c, "reconcile") }
func (s *Server) controlMutation(c *gin.Context, operation string) {
	ctx, stop, ok := s.controlContext(c, true)
	if !ok {
		return
	}
	defer stop()
	var body struct{}
	if !decodeControlJSON(c, &body) {
		return
	}
	actor, _, ok := s.ownedControlActor(c, ctx)
	if !ok {
		return
	}
	var p *smarthome.ControlProposal
	var err error
	switch operation {
	case "confirm":
		p, err = s.control.Confirm(ctx, actor, c.Param("id"))
	case "cancel":
		p, err = s.control.Cancel(ctx, actor, c.Param("id"))
	case "reconcile":
		p, err = s.control.Reconcile(ctx, actor, c.Param("id"))
	}
	if err != nil {
		s.controlError(c, err)
		return
	}
	s.consoleSmartHomeJSON(c, ctx, p)
}
