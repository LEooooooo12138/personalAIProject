package core

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig
	Inference   InferenceConfig
	Vaults      VaultsConfig
	Session     SessionConfig
	Memory      MemoryConfig
	Personality PersonalityConfig
	Logging     LoggingConfig
	Channels    ChannelsConfig
}

type ServerConfig struct {
	Port        int    `mapstructure:"port"`
	InternalKey string `mapstructure:"internal_key"`
}

type InferenceConfig struct {
	Endpoint string        `mapstructure:"endpoint"`
	Models   ModelsConfig  `mapstructure:"models"`
	Timeout  time.Duration `mapstructure:"timeout"`
}

type ModelsConfig struct {
	Local     string `mapstructure:"local"`
	Vision    string `mapstructure:"vision"`
	Embedding string `mapstructure:"embedding"`
}

type VaultsConfig struct {
	Personal string `mapstructure:"personal"`
	Agent    string `mapstructure:"agent"`
}

type SessionConfig struct {
	MaxRounds    int           `mapstructure:"max_rounds"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`
	ScanInterval time.Duration `mapstructure:"scan_interval"`
	MaxSessions  int           `mapstructure:"max_sessions"`
}

type MemoryConfig struct {
	DedupThreshold float64 `mapstructure:"dedup_threshold"`
	MinMessages    int     `mapstructure:"min_messages"`
}

type PersonalityConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	Name         string `mapstructure:"name"`
	Tone         string `mapstructure:"tone"`
	SystemPrompt string `mapstructure:"system_prompt"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

type ChannelsConfig struct {
	Wecom WecomChannelConfig `mapstructure:"wecom"`
}

type WecomChannelConfig struct {
	Enabled        bool     `mapstructure:"enabled"`
	ListenAddr     string   `mapstructure:"listen_addr"`
	CorpID         string   `mapstructure:"corp_id"`
	CorpSecret     string   `mapstructure:"corp_secret"`
	AgentID        string   `mapstructure:"agent_id"`
	Token          string   `mapstructure:"token"`
	EncodingAESKey string   `mapstructure:"encoding_aes_key"`
	AllowedUsers   []string `mapstructure:"allowed_users"`
	AutoApprove    bool     `mapstructure:"auto_approve"`
}

func LoadConfig(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	for _, key := range v.AllKeys() {
		val := v.GetString(key)
		if strings.HasPrefix(val, "${") && strings.HasSuffix(val, "}") {
			envName := val[2 : len(val)-1]
			if envVal, ok := os.LookupEnv(envName); ok {
				v.Set(key, envVal)
			}
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Inference.Endpoint == "" {
		return nil, fmt.Errorf("config: inference.endpoint is required")
	}
	if cfg.Vaults.Personal == "" {
		return nil, fmt.Errorf("config: vaults.personal is required")
	}
	if cfg.Vaults.Agent == "" {
		return nil, fmt.Errorf("config: vaults.agent is required")
	}
	if cfg.Inference.Timeout == 0 {
		cfg.Inference.Timeout = 60 * time.Second
	}
	if cfg.Session.MaxRounds == 0 {
		cfg.Session.MaxRounds = 20
	}
	if cfg.Session.IdleTimeout == 0 {
		cfg.Session.IdleTimeout = 30 * time.Minute
	}
	if cfg.Session.ScanInterval == 0 {
		cfg.Session.ScanInterval = 1 * time.Minute
	}
	if cfg.Session.MaxSessions == 0 {
		cfg.Session.MaxSessions = 100
	}
	if cfg.Memory.DedupThreshold == 0 {
		cfg.Memory.DedupThreshold = 0.45
	}
	if cfg.Memory.MinMessages == 0 {
		cfg.Memory.MinMessages = 3
	}

	return &cfg, nil
}
