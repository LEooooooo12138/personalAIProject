package wecom

import (
	"fmt"

	"github.com/yuanleyao/ai-agent/internal/channel"
	"go.uber.org/zap"
)

type Config struct {
	Enabled        bool     `yaml:"enabled"`
	ListenAddr     string   `yaml:"listen_addr"`
	CorpID         string   `yaml:"corp_id"`
	CorpSecret     string   `yaml:"corp_secret"`
	AgentID        string   `yaml:"agent_id"`
	Token          string   `yaml:"token"`
	EncodingAESKey string   `yaml:"encoding_aes_key"`
	AllowedUsers   []string `yaml:"allowed_users"`
	AutoApprove    bool     `yaml:"auto_approve"`
}

func init() {
	channel.RegisterFactory(&Factory{})
}

type Factory struct{}

func (f *Factory) ID() string { return "wecom" }

func (f *Factory) Create(cfg map[string]interface{}, logger *zap.Logger) (channel.Channel, error) {
	wcCfg := Config{}
	if v, ok := cfg["listen_addr"].(string); ok   { wcCfg.ListenAddr = v }
	if v, ok := cfg["corp_id"].(string); ok         { wcCfg.CorpID = v }
	if v, ok := cfg["corp_secret"].(string); ok     { wcCfg.CorpSecret = v }
	if v, ok := cfg["agent_id"].(string); ok        { wcCfg.AgentID = v }
	if v, ok := cfg["token"].(string); ok           { wcCfg.Token = v }
	if v, ok := cfg["encoding_aes_key"].(string); ok { wcCfg.EncodingAESKey = v }
	if v, ok := cfg["auto_approve"].(bool); ok      { wcCfg.AutoApprove = v }
	if v, ok := cfg["allowed_users"].([]interface{}); ok {
		for _, u := range v {
			if s, ok := u.(string); ok {
				wcCfg.AllowedUsers = append(wcCfg.AllowedUsers, s)
			}
		}
	}
	if wcCfg.CorpID == "" {
		return nil, fmt.Errorf("wecom: corp_id is required")
	}
	return NewAdapter(wcCfg, logger)
}
