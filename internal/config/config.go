package config

import (
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig       `yaml:"server"`
	RateLimits   RateLimitConfig    `yaml:"rate_limits"`
	State        StateConfig        `yaml:"state"`
	Messaging    MessagingConfig    `yaml:"messaging"`
	Webhook      WebhookConfig      `yaml:"webhook"`
	EventLog     EventLogConfig     `yaml:"event_log"`
	Verification VerificationConfig `yaml:"verification"`
}

type ServerConfig struct {
	ListenAddr   string        `yaml:"listen_addr"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type RateLimitConfig struct {
	MessageSendPerMin       int `yaml:"message_send_per_min"`
	StateReadPerMin         int `yaml:"state_read_per_min"`
	StateWritePerMin        int `yaml:"state_write_per_min"`
	RegistryReadPerMin      int `yaml:"registry_read_per_min"`
	OperatorRegPerHourPerIP int `yaml:"operator_reg_per_hour_per_ip"`
	WebhookOutboundPerMin   int `yaml:"webhook_outbound_per_min"`
}

type StateConfig struct {
	DataDir        string `yaml:"data_dir"`
	MaxCapacityBytes int64  `yaml:"max_capacity_bytes"`
	MaxKeyLength   int    `yaml:"max_key_length"`
	MaxValueLength int    `yaml:"max_value_length"`
}

type MessagingConfig struct {
	MaxContentLength int `yaml:"max_content_length"`
	MaxQueuePerAgent int `yaml:"max_queue_per_agent"`
}

type WebhookConfig struct {
	ConnectTimeout          time.Duration `yaml:"connect_timeout"`
	TotalTimeout            time.Duration `yaml:"total_timeout"`
	CircuitBreakerThreshold int           `yaml:"circuit_breaker_threshold"`
	CircuitBreakerDuration  time.Duration `yaml:"circuit_breaker_duration"`
	MaxRetries              int           `yaml:"max_retries"`
}

type EventLogConfig struct {
	DataDir     string `yaml:"data_dir"`
	MaxFileSize int64  `yaml:"max_file_size"`
}

type VerificationConfig struct {
	GateType        string        `yaml:"gate_type"`
	ChallengeExpiry time.Duration `yaml:"challenge_expiry"`
}

func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			ListenAddr:   ":8080",
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		RateLimits: RateLimitConfig{
			MessageSendPerMin:       60,
			StateReadPerMin:         300,
			StateWritePerMin:        60,
			RegistryReadPerMin:      30,
			OperatorRegPerHourPerIP: 5,
			WebhookOutboundPerMin:   60,
		},
		State: StateConfig{
			DataDir:        "./data/state",
			MaxCapacityBytes: 1073741824, // 1 GB
			MaxKeyLength:   1024,
			MaxValueLength: 1048576, // 1 MB
		},
		Messaging: MessagingConfig{
			MaxContentLength: 65536, // 64 KB
			MaxQueuePerAgent: 10000,
		},
		Webhook: WebhookConfig{
			ConnectTimeout:          5 * time.Second,
			TotalTimeout:            10 * time.Second,
			CircuitBreakerThreshold: 5,
			CircuitBreakerDuration:  5 * time.Minute,
			MaxRetries:              3,
		},
		EventLog: EventLogConfig{
			DataDir:     "./data/eventlog",
			MaxFileSize: 104857600, // 100 MB
		},
		Verification: VerificationConfig{
			GateType:        "stub",
			ChallengeExpiry: 5 * time.Minute,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := DefaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return cfg, err
			}
			// File doesn't exist — use defaults
		} else {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return cfg, err
			}
		}
	}

	// Environment variable overrides
	if v := os.Getenv("TABULUM_LISTEN_ADDR"); v != "" {
		cfg.Server.ListenAddr = v
	}
	if v := os.Getenv("TABULUM_STATE_DATA_DIR"); v != "" {
		cfg.State.DataDir = v
	}
	if v := os.Getenv("TABULUM_EVENTLOG_DATA_DIR"); v != "" {
		cfg.EventLog.DataDir = v
	}
	if v := os.Getenv("TABULUM_VERIFICATION_GATE_TYPE"); v != "" {
		cfg.Verification.GateType = v
	}
	if v := os.Getenv("TABULUM_MAX_CAPACITY_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.State.MaxCapacityBytes = n
		}
	}
	if v := os.Getenv("TABULUM_MESSAGE_SEND_PER_MIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.RateLimits.MessageSendPerMin = n
		}
	}

	// Ensure defaults are filled in for zero values
	if cfg.Server.ListenAddr == "" {
		cfg.Server.ListenAddr = ":8080"
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 30 * time.Second
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 30 * time.Second
	}
	if cfg.State.DataDir == "" {
		cfg.State.DataDir = "./data/state"
	}
	if cfg.EventLog.DataDir == "" {
		cfg.EventLog.DataDir = "./data/eventlog"
	}
	if cfg.Verification.GateType == "" {
		cfg.Verification.GateType = "stub"
	}
	if cfg.Verification.ChallengeExpiry == 0 {
		cfg.Verification.ChallengeExpiry = 5 * time.Minute
	}

	return cfg, nil
}
