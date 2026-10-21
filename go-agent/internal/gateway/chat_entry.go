package gateway

import (
	"context"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/core"
)

type ChatEntry string

const (
	ChatEntryAuto ChatEntry = "auto"
	ChatEntryChat ChatEntry = "chat"
)

func parseChatEntry(value string) (ChatEntry, error) {
	switch value {
	case "", "auto":
		return ChatEntryAuto, nil
	case "chat":
		return ChatEntryChat, nil
	}
	return "", core.ErrRequestEntryInvalid
}

// Called inside the existing turn lease, before control replay or any provider.
// Persist a snapshot before publishing the binding into live session memory.
func (w *wsConn) bindChatEntry(ctx context.Context, session *core.Session, content string) bool {
	commit := func(live context.Context) error {
		if err := live.Err(); err != nil {
			return err
		}
		snapshot := core.CloneSession(session)
		if err := snapshot.BindRequestEntry(w.requestID, content, string(w.entry)); err != nil {
			return err
		}
		done := measureChat(live, "persist")
		defer done()
		if err := w.sessionStore.SaveSession(snapshot); err != nil {
			return err
		}
		return session.BindRequestEntry(w.requestID, content, string(w.entry))
	}
	var err error
	if w.options.WithSession != nil {
		err = w.options.WithSession(ctx, commit)
	} else if err = w.validateSession(); err == nil {
		err = commit(ctx)
	}
	if err == nil {
		return true
	}
	if ctx.Err() != nil {
		w.publishChatTimeout(ctx)
		return false
	}
	code, message := "chat_publish_failed", "请求未能保存，请核对会话后重试。"
	if errors.Is(err, core.ErrRequestEntryConflict) {
		code, message = "request_entry_conflict", "此请求的内容或入口与原请求不一致，请核对会话。"
	}
	if errors.Is(err, core.ErrRequestEntryLimit) {
		code, message = "request_entry_limit", "当前会话已达到请求上限，请新建会话。"
	}
	_ = w.publishChatFrame(ctx, serverMessage{Type: "error", Code: code, Outcome: "failed", Message: message, SessionID: w.sessionID, RequestID: w.requestID})
	return false
}
