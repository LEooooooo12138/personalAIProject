package wecom

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
