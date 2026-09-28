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
	Retrieval   RetrievalConfig
	Session     SessionConfig
	Memory      MemoryConfig
	Personality PersonalityConfig
	Logging     LoggingConfig
	Channels    ChannelsConfig
	SmartHome   SmartHomeConfig
}

type ServerConfig struct {
	Port            int           `mapstructure:"port"`
	InternalKey     string        `mapstructure:"internal_key"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	ChainTimeout    time.Duration `mapstructure:"chain_timeout"`
	SearchTimeout   time.Duration `mapstructure:"search_timeout"`
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

type RetrievalConfig struct {
	RRFK          int           `mapstructure:"rrf_k"`
	TopK          int           `mapstructure:"top_k"`
	MaxChunkChars int           `mapstructure:"max_chunk_chars"`
	SearchTimeout time.Duration `mapstructure:"search_timeout"`
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

// SmartHomeConfig holds Home Assistant connection and automation settings.
type SmartHomeConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	BaseURL         string `mapstructure:"base_url"`
	Token           string `mapstructure:"token"`
	PollIntervalSec int    `mapstructure:"poll_interval_sec"`
	AnalysisHour    int    `mapstructure:"analysis_hour"`
	// AgentVaultPath is where rule documents and reports are written.
	AgentVaultPath string `mapstructure:"agent_vault_path"`
}

type WecomChannelConfig struct {
	Enabled bool                   `mapstructure:"enabled"`
	Config  map[string]interface{} `mapstructure:"config"`
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
			// Missing secrets must never become usable literal ${NAME} credentials.
			v.Set(key, os.Getenv(envName))
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
	if cfg.Retrieval.RRFK == 0 {
		cfg.Retrieval.RRFK = 60
	}
	if cfg.Retrieval.TopK == 0 {
		cfg.Retrieval.TopK = 5
	}
	if cfg.Retrieval.MaxChunkChars == 0 {
		cfg.Retrieval.MaxChunkChars = 800
	}
	if cfg.Retrieval.SearchTimeout == 0 {
		cfg.Retrieval.SearchTimeout = 30 * time.Second
	}
	if cfg.Server.ShutdownTimeout == 0 {
		cfg.Server.ShutdownTimeout = 10 * time.Second
	}
	if cfg.Server.ChainTimeout == 0 {
		cfg.Server.ChainTimeout = 300 * time.Second
	}
	if cfg.Server.SearchTimeout == 0 {
		cfg.Server.SearchTimeout = 30 * time.Second
	}
	if cfg.Memory.MinMessages == 0 {
		cfg.Memory.MinMessages = 3
	}
	if cfg.SmartHome.PollIntervalSec == 0 {
		cfg.SmartHome.PollIntervalSec = 3600
	}
	if cfg.SmartHome.AnalysisHour == 0 {
		cfg.SmartHome.AnalysisHour = 3
	}
	if cfg.SmartHome.AgentVaultPath == "" && cfg.Vaults.Agent != "" {
		cfg.SmartHome.AgentVaultPath = cfg.Vaults.Agent + "/smart-home"
	}

	return &cfg, nil
}
