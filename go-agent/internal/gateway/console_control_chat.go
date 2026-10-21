package gateway

import (
	"context"
	"errors"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"go.uber.org/zap"
	"strings"
	"time"
)

func (w *wsConn) handleControlChat(parent context.Context, content string, session *core.Session) bool {
	mode := core.CloneSession(session).AssistantMode
	budget := 60 * time.Second
	if w.options.AssistantConfig != nil && (mode == "cloud" || mode == "hybrid") {
		budget = 5 * time.Minute
	}
	// Budget includes a bounded local attempt, cloud fallback and a separate
	// local device-resolution step. Login/connection cancellation still wins.
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	if w.options.AuthorizeControl != nil {
		ctx = smarthome.WithControlAuthorization(ctx, w.options.AuthorizeControl)
	}
	actor := smarthome.ControlActor{UserID: strings.TrimPrefix(w.owner, "console:"), SessionID: w.sessionID}
	var result *core.ControlChatResult
	var err error
	if err := w.publishChatStage(ctx, ChatStageClassifying); err != nil {
		w.publishChatTimeout(ctx)
		return true
	}
	ctx = observeChatCalls(ctx, "classify")
	classifyDone := measureChat(ctx, "classify")
	defer classifyDone()
	if w.options.AssistantConfig != nil && (mode == "cloud" || mode == "hybrid") {
		settingsDone := measureChat(ctx, "settings")
		settings, key, configErr := w.options.AssistantConfig.Snapshot()
		settingsDone()
		if configErr != nil {
			err = errAssistantSettingsUnavailable
		} else {
			result, err = w.options.ControlChat.HandleConversation(inference.WithAssistantRoute(ctx, mode, key, settings.MiMoEndpoint), actor, w.requestID, content)
		}
	} else {
		result, err = w.options.ControlChat.Handle(ctx, actor, w.requestID, content)
	}
	classifyDone()
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		w.logger.Warn("console device request failed", zap.String("error_class", controlChatErrorClass(err)))
		if w.validateSession() == nil {
			w.writeJSON(serverMessage{Type: "error", Code: chatErrorCode(err, "control_unavailable"), Outcome: controlFailureOutcome(err), Message: "无法核对设备请求，请明确设备名称或稍后重试。", SessionID: w.sessionID, RequestID: w.requestID})
		}
		return true
	}
	if result == nil {
		return false
	}
	return w.publishControlResult(ctx, result, session)
}
func (w *wsConn) handleControlReplay(parent context.Context, content string, session *core.Session) bool {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	if w.options.AuthorizeControl != nil {
		ctx = smarthome.WithControlAuthorization(ctx, w.options.AuthorizeControl)
	}
	event := "request:" + w.requestID
	index, saved, err := w.sessionStore.OwnedEvent(w.options.ChannelID, w.sessionID, w.owner, event)
	if err != nil {
		if w.validateSession() == nil {
			w.writeJSON(serverMessage{Type: "error", Code: "chat_publish_failed", Outcome: "unknown", Message: "设备提议或查询结果未能保存，请从历史核对。"})
		}
		return true
	}

	if err == nil && saved != nil {
		history := core.CloneSession(session).Messages
		if index < 1 || index >= len(history) || history[index-1].Role != "user" || history[index-1].Content != content {
			err = smarthome.ErrControlConflict
		} else {
			result := &core.ControlChatResult{Type: "response", Content: saved.Content, DeviceResult: saved.DeviceResult}
			if saved.DeviceResult != nil {
				result.Type = "device_result"
			}
			if len(saved.Attachments) > 0 {
				var replay *core.ControlChatResult
				replay, err = w.options.ControlChat.Replay(ctx, smarthome.ControlActor{UserID: strings.TrimPrefix(w.owner, "console:"), SessionID: w.sessionID}, w.requestID, content)
				if replay != nil {
					result.Type = replay.Type
					result.Proposal = replay.Proposal
				}
			}
			if err == nil {
				w.sendSavedControl(ctx, result, *saved)
				return true
			}
		}
	}
	if err != nil {
		if w.validateSession() == nil {
			w.writeJSON(serverMessage{Type: "error", Code: smarthome.ControlErrorCode(err), Outcome: "unknown", Message: "无法恢复此前设备请求，请从历史核对。", SessionID: w.sessionID, RequestID: w.requestID})
		}
		return true
	}
	result, err := w.options.ControlChat.Replay(ctx, smarthome.ControlActor{UserID: strings.TrimPrefix(w.owner, "console:"), SessionID: w.sessionID}, w.requestID, content)
	if err != nil {
		if w.validateSession() == nil {
			w.writeJSON(serverMessage{Type: "error", Code: smarthome.ControlErrorCode(err), Outcome: "unknown", Message: "无法恢复此前设备请求，请从历史核对。", SessionID: w.sessionID, RequestID: w.requestID})
		}
		return true
	}
	if result == nil {
		return false
	}
	if result.Proposal != nil {
		legacy, loadErr := w.sessionStore.OwnedLegacyProposalMessage(w.options.ChannelID, w.sessionID, w.owner, result.Proposal.ID)
		if loadErr != nil {
			if w.validateSession() == nil {
				w.writeJSON(serverMessage{Type: "error", Code: "chat_publish_failed", Outcome: "unknown", Message: "设备提议或查询结果未能保存，请从历史核对。"})
			}
			return true
		}
		if legacy != nil {
			w.sendSavedControl(ctx, result, *legacy)
			return true
		}
	}
	// A prior business event may have survived an interrupted message save.
	// Rebuild only its presentation; Replay never sends a control write.
	return w.publishControlResult(ctx, result, session)
}

func (w *wsConn) sendSavedControl(ctx context.Context, result *core.ControlChatResult, msg core.Message) {
	publish := func(live context.Context) error {
		return w.writeJSONContext(live, serverMessage{Type: result.Type, Content: msg.Content, SessionID: w.sessionID, RequestID: w.requestID, Proposal: result.Proposal, DeviceResult: msg.DeviceResult, EventID: msg.EventID, PersonalityRevision: msg.PersonalityRevision, PresentationVersion: msg.PresentationVersion})
	}
	if w.options.WithSession != nil {
		_ = w.options.WithSession(ctx, publish)
	} else if w.validateSession() == nil {
		_ = publish(ctx)
	}
}
func (w *wsConn) publishControlResult(ctx context.Context, result *core.ControlChatResult, session *core.Session) bool {
	// handleChatContext already owns this session's turn. Generate outside the
	// auth-store commit lock so logout remains responsive during local inference.
	msg := presentDevice(ctx, w.options.Presenter, w.options.AssistantConfig, result.Stage, result.Content, "request:"+w.requestID)
	if result.Proposal != nil {
		msg.Attachments = []core.MessageAttachment{{Kind: "control_proposal", ProposalID: result.Proposal.ID}}
	}
	msg.DeviceResult = result.DeviceResult
	var saved *core.Message
	commit := func(live context.Context) error {
		if err := live.Err(); err != nil {
			return err
		}
		var err error
		persistDone := measureChat(ctx, "persist")
		defer persistDone()
		_, saved, err = w.sessionStore.SaveOwnedEvent(w.options.ChannelID, w.sessionID, w.owner, msg)
		return err
	}
	var err error
	if w.options.WithSession != nil {
		err = w.options.WithSession(ctx, commit)
	} else if err = w.validateSession(); err == nil {
		err = commit(ctx)
	}
	if err == nil {
		w.sendSavedControl(ctx, result, *saved)
	} else {
		w.logger.Warn("console device result publication failed", zap.String("error_class", controlChatErrorClass(err)))
		if w.validateSession() == nil {
			w.writeJSON(serverMessage{Type: "error", Code: "chat_publish_failed", Outcome: "unknown", Message: "设备提议或查询结果未能保存，请从历史核对。"})
		}
	}
	return true
}

// Classify only known sentinels. Raw errors can contain model input or credentials.
func controlChatErrorClass(err error) string {
	if code := inference.ErrorCode(err); code != "" {
		return code
	}
	for _, known := range []struct {
		err   error
		class string
	}{
		{chain.ErrHAIntentInvalid, "intent_invalid"},
		{chain.ErrHAIntentUnavailable, "intent_unavailable"},
		{context.Canceled, "cancelled"},
		{context.DeadlineExceeded, "deadline_exceeded"},
		{smarthome.ErrControlInvalid, "control_invalid"},
		{smarthome.ErrControlForbidden, "control_forbidden"},
		{smarthome.ErrControlNotFound, "control_not_found"},
		{smarthome.ErrControlConflict, "control_conflict"},
		{smarthome.ErrControlExpired, "control_expired"},
		{smarthome.ErrControlUnsupported, "control_unsupported"},
		{smarthome.ErrControlUnavailable, "control_unavailable"},
		{smarthome.ErrControlCapacity, "control_capacity"},
	} {
		if errors.Is(err, known.err) {
			return known.class
		}
	}
	return "unknown"
}
