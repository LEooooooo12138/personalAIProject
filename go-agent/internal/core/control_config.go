package core

import (
	"fmt"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"time"
)

type HAControlTarget struct {
	EntityID             string   `mapstructure:"entity_id"`
	Name                 string   `mapstructure:"name"`
	AreaName             string   `mapstructure:"area_name"`
	Domain               string   `mapstructure:"domain"`
	Aliases              []string `mapstructure:"aliases"`
	AllowedActions       []string `mapstructure:"allowed_actions"`
	LoadLocationVerified bool     `mapstructure:"load_location_verified"`
}
type HAControlConfig struct {
	Enabled          bool              `mapstructure:"enabled"`
	Targets          []HAControlTarget `mapstructure:"targets"`
	ProposalTTL      time.Duration     `mapstructure:"proposal_ttl"`
	ReadbackTimeout  time.Duration     `mapstructure:"readback_timeout"`
	ReadbackInterval time.Duration     `mapstructure:"readback_interval"`
	Retention        time.Duration     `mapstructure:"retention"`
	MaxRecords       int               `mapstructure:"max_records"`
}

func (c HAControlConfig) ServiceConfig() smarthome.ControlConfig {
	targets := make([]smarthome.ControlTarget, 0, len(c.Targets))
	for _, t := range c.Targets {
		targets = append(targets, smarthome.ControlTarget{EntityID: t.EntityID, Name: t.Name, AreaName: t.AreaName, Domain: t.Domain, Aliases: t.Aliases, AllowedActions: t.AllowedActions, LoadLocationVerified: t.LoadLocationVerified})
	}
	return smarthome.ControlConfig{Targets: targets, ProposalTTL: c.ProposalTTL, ReadbackTimeout: c.ReadbackTimeout, ReadbackInterval: c.ReadbackInterval, Retention: c.Retention, MaxRecords: c.MaxRecords}
}
func (c *Config) ValidateHAControl() error {
	if !c.SmartHome.Control.Enabled {
		return nil
	}
	if !c.Console.Enabled || !c.SmartHome.Enabled || c.SmartHome.BaseURL == "" {
		return fmt.Errorf("config: smarthome.control requires Console and Home Assistant")
	}
	return nil
}
