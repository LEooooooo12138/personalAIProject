package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

type HAIntentParser interface {
	Parse(context.Context, string, chain.HACandidates) (chain.HAIntent, error)
}
type ControlChat struct {
	service *smarthome.ControlService
	parser  HAIntentParser
}
type ControlChatResult struct {
	Type         string
	Content      string
	Proposal     *smarthome.ControlProposal
	DeviceResult *smarthome.DeviceQueryResult
}

func NewControlChat(service *smarthome.ControlService, parser HAIntentParser) *ControlChat {
	return &ControlChat{service, parser}
}
func (c *ControlChat) Handle(ctx context.Context, actor smarthome.ControlActor, requestID, query string) (*ControlChatResult, error) {
	qs, cs, candidateErr := c.service.Candidates(ctx, query)
	if candidateErr != nil {
		qs = nil
		cs = nil
	}
	candidates := chain.HACandidates{}
	for _, t := range qs {
		candidates.Query = append(candidates.Query, chain.HATarget{EntityID: t.EntityID, Name: t.Name, AreaName: t.AreaName, Domain: t.Domain})
	}
	for _, t := range cs {
		candidates.Control = append(candidates.Control, chain.HATarget{EntityID: t.EntityID, Name: t.Name, AreaName: t.AreaName, Domain: t.Domain, Aliases: t.Aliases})
	}
	// Verified load names take precedence over registry controller names for shared IDs.
	for i, q := range candidates.Query {
		for _, target := range candidates.Control {
			if q.EntityID == target.EntityID {
				candidates.Query[i] = target
			}
		}
	}
	intent, err := c.parser.Parse(ctx, query, candidates)
	if err != nil {
		return nil, err
	}
	switch intent.Kind {
	case "chat":
		return nil, nil
	case "clarify":
		return &ControlChatResult{Type: "response", Content: intent.Question}, nil
	case "query":
		if candidateErr != nil {
			return nil, candidateErr
		}
		result, err := c.service.Query(ctx, intent.EntityID)
		if err != nil {
			return nil, err
		}
		return &ControlChatResult{Type: "device_result", Content: fmt.Sprintf("%s：%s（本次读取的HA状态）", result.Name, result.State), DeviceResult: result}, nil
	case "on_off":
		if candidateErr != nil {
			return nil, candidateErr
		}
		outcome, err := c.service.Propose(ctx, actor, requestID, smarthome.ControlIntent{InputHash: controlInputHash(query), Kind: "on_off", EntityID: intent.EntityID, Action: intent.Action})
		if err != nil {
			return nil, err
		}
		if outcome.Kind == "already_satisfied" {
			return &ControlChatResult{Type: "device_result", Content: "设备已经处于目标状态，没有发送控制指令。", DeviceResult: outcome.DeviceResult}, nil
		}
		return &ControlChatResult{Type: "control_proposal", Content: "请核对设备与动作，然后单独确认。尚未执行设备控制。", Proposal: outcome.Proposal}, nil
	default:
		return nil, smarthome.ErrControlInvalid
	}
}
func (c *ControlChat) Replay(ctx context.Context, actor smarthome.ControlActor, requestID, query string) (*ControlChatResult, error) {
	outcome, err := c.service.Replay(ctx, actor, requestID, controlInputHash(query))
	if err != nil || outcome == nil {
		return nil, err
	}
	if outcome.Proposal != nil {
		return &ControlChatResult{Type: "control_proposal", Content: "已恢复此前请求，请以卡片当前状态为准。", Proposal: outcome.Proposal}, nil
	}
	return &ControlChatResult{Type: "device_result", Content: "此前请求已满足目标状态，没有发送控制指令；观察时间见卡片。", DeviceResult: outcome.DeviceResult}, nil
}

func controlInputHash(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}
