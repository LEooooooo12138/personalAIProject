package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/persona"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

type assistantMessage struct {
	Index   int          `json:"index"`
	Message core.Message `json:"message"`
}
type controlPresentationResponse struct {
	*smarthome.ControlProposal
	AssistantMessage            *assistantMessage `json:"assistant_message,omitempty"`
	AssistantMessageUnavailable bool              `json:"assistant_message_unavailable,omitempty"`
}

// Only the local selector receives personality markdown. Facts stay server-owned.
func presentDevice(ctx context.Context, presenter *persona.Presenter, config *assistantconfig.Store, stage, fact, event string) core.Message {
	msg := core.Message{Role: "assistant", Content: fact, Timestamp: time.Now(), AssistantMode: "local", EventID: event}
	if config == nil || presenter == nil {
		return msg
	}
	markdown, err := config.PersonalityContent()
	if err != nil {
		return msg
	}
	result := presenter.Present(ctx, markdown, persona.Event{Stage: stage, Fact: fact})
	msg.Content = result.Content
	msg.PersonalityRevision = result.PersonalityRevision
	msg.PresentationVersion = result.Version
	return msg
}

func proposalEvent(p *smarthome.ControlProposal) string {
	// Observation and error are part of the identity so a later reconcile is a
	// distinct fact, while a repeated retrieval remains the same event.
	data, _ := json.Marshal(struct {
		Status string
		After  *smarthome.DeviceQueryResult
		Error  string
	}{p.Status, p.After, p.ErrorCode})
	return fmt.Sprintf("proposal:%s:%x", p.ID, sha256.Sum256(data))
}
func proposalFact(p *smarthome.ControlProposal) string {
	switch p.Status {
	case "succeeded":
		return "已从 Home Assistant 读取到设备的目标状态；这不等同于现场确认。"
	case "failed":
		return "此次设备控制失败，请核对设备状态后再决定下一步。"
	case "unknown":
		return "此次设备控制结果尚不确定；不能确认成功，也不能确定指令未执行。请先核对状态。"
	case "cancelled":
		return "提议已取消，未通过此提议发送设备控制指令。"
	case "expired":
		return "提议已过期，不能再确认执行；请重新查询设备状态。"
	case "executing":
		return "设备控制正在处理，结果尚未确定，请勿重复发送。"
	default:
		return "请核对设备与动作，然后单独确认。尚未执行设备控制。"
	}
}

// Caller holds the conversation turn; inference never holds the auth-store lock.
func (s *Server) publishProposal(c *gin.Context, ctx context.Context, p *smarthome.ControlProposal, create bool) {
	response := controlPresentationResponse{ControlProposal: p}
	owner := "console:" + requestConsolePrincipal(c.Request).UserID
	event := proposalEvent(p)
	index, msg, err := s.sessionStore.OwnedEvent("console", p.SessionID, owner, event)
	if err == nil && msg == nil && create && p.Status != "pending" {
		candidate := presentDevice(ctx, s.presenter, s.assistantConfig, p.Status, proposalFact(p), event)
		candidate.Attachments = []core.MessageAttachment{{Kind: "control_proposal", ProposalID: p.ID}}
		candidate.DeviceResult = p.After
		err = s.consoleStore.WithSession(ctx, consoleToken(c.Request), func(live context.Context) error {
			if err := live.Err(); err != nil {
				return err
			}
			var saveErr error
			index, msg, saveErr = s.sessionStore.SaveOwnedEvent("console", p.SessionID, owner, candidate)
			return saveErr
		})
	}
	if err != nil {
		response.AssistantMessageUnavailable = true
	} else if msg != nil {
		response.AssistantMessage = &assistantMessage{Index: index, Message: *msg}
	}
	s.consoleSmartHomeJSON(c, ctx, response)
}

func (s *Server) acquireControlTurn(c *gin.Context, ctx context.Context, session string) (func(), bool) {
	if s.sessionMgr == nil {
		consoleError(c, 503, "unavailable", "会话服务不可用")
		return nil, false
	}
	release, err := s.sessionMgr.AcquireTurn(ctx, "console", session)
	if err != nil {
		s.controlError(c, err)
		return nil, false
	}
	principal, err := s.consoleStore.Resolve(consoleToken(c.Request))
	if err != nil || principal.UserID != requestConsolePrincipal(c.Request).UserID || principal.MustChangePassword {
		release()
		consoleError(c, 401, "unauthenticated", "会话已失效")
		return nil, false
	}
	if _, err := s.sessionStore.OwnedMessages("console", session, "console:"+principal.UserID); err != nil {
		release()
		consoleError(c, 404, "not_found", "未找到会话")
		return nil, false
	}
	return release, true
}
