package gateway

import (
	"context"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"strings"
	"time"
)

func (w *wsConn) handleControlChat(parent context.Context, content string, session *core.Session) bool {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	if w.options.AuthorizeControl != nil {
		ctx = smarthome.WithControlAuthorization(ctx, w.options.AuthorizeControl)
	}
	actor := smarthome.ControlActor{UserID: strings.TrimPrefix(w.owner, "console:"), SessionID: w.sessionID}
	result, err := w.options.ControlChat.Handle(ctx, actor, w.requestID, content)
	if err != nil {
		if w.validateSession() == nil {
			w.writeJSON(serverMessage{Type: "error", Code: "control_unavailable", Message: "无法核对设备请求，请明确设备名称或稍后重试。", SessionID: w.sessionID, RequestID: w.requestID})
		}
		return true
	}
	if result == nil {
		return false
	}
	return w.publishControlResult(result, session)
}
func (w *wsConn) handleControlReplay(parent context.Context, content string, session *core.Session) bool {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	if w.options.AuthorizeControl != nil {
		ctx = smarthome.WithControlAuthorization(ctx, w.options.AuthorizeControl)
	}
	result, err := w.options.ControlChat.Replay(ctx, smarthome.ControlActor{UserID: strings.TrimPrefix(w.owner, "console:"), SessionID: w.sessionID}, w.requestID, content)
	if err != nil {
		if w.validateSession() == nil {
			w.writeJSON(serverMessage{Type: "error", Code: smarthome.ControlErrorCode(err), Message: "无法恢复此前设备请求，请从历史核对。", SessionID: w.sessionID, RequestID: w.requestID})
		}
		return true
	}
	if result == nil {
		return false
	}
	// Existing history is immutable; resend its current card without consuming a round.
	publish := func(live context.Context) error {
		return w.writeJSONContext(live, serverMessage{Type: result.Type, Content: result.Content, SessionID: w.sessionID, RequestID: w.requestID, Proposal: result.Proposal, DeviceResult: result.DeviceResult})
	}
	if w.options.WithSession != nil {
		_ = w.options.WithSession(w.ctx, publish)
	} else if w.validateSession() == nil {
		_ = publish(w.ctx)
	}
	return true
}
func (w *wsConn) publishControlResult(result *core.ControlChatResult, session *core.Session) bool {
	var err error
	commit := func(live context.Context) error {
		filtered, _ := w.filter.Apply(result.Content, map[string]string{"channel": "console", "platform": "console"})
		msg := core.Message{Role: "assistant", Content: filtered, Timestamp: time.Now()}
		if result.Proposal != nil {
			msg.Attachments = []core.MessageAttachment{{Kind: "control_proposal", ProposalID: result.Proposal.ID}}
		}
		if !w.sessionMgr.AddMessage(session, msg) {
			return smarthome.ErrControlConflict
		}
		if err := w.sessionStore.SaveSession(session); err != nil {
			return err
		}
		return w.writeJSONContext(live, serverMessage{Type: result.Type, Content: filtered, SessionID: w.sessionID, RequestID: w.requestID, Proposal: result.Proposal, DeviceResult: result.DeviceResult})
	}
	if w.options.WithSession != nil {
		err = w.options.WithSession(w.ctx, commit)
	} else {
		err = commit(w.ctx)
	}
	if err != nil && w.validateSession() == nil {
		w.writeJSON(serverMessage{Type: "error", Message: "设备提议或查询结果未能保存，请从历史核对。"})
	}
	return true
}
