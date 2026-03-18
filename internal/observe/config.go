package observe

import (
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Kernel    KernelDataConfig `yaml:"kernel"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	WebSocket WebSocketConfig `yaml:"websocket"`
}

type ServerConfig struct {
	ListenAddr   string        `yaml:"listen_addr"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type KernelDataConfig struct {
	EventLogDir string `yaml:"event_log_dir"`
	StateDBPath string `yaml:"state_db_path"`
	RegistryDBPath string `yaml:"registry_db_path"`
	TransparencyLogPath string `yaml:"transparency_log_path"`
}

type RateLimitConfig struct {
	RequestsPerMinPerIP int `yaml:"requests_per_min_per_ip"`
}

type WebSocketConfig struct {
	MaxConnections int           `yaml:"max_connections"`
	WriteTimeout   time.Duration `yaml:"write_timeout"`
	PingInterval   time.Duration `yaml:"ping_interval"`
	MaxDuration    time.Duration `yaml:"max_duration"`
}

func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			ListenAddr:   ":8090",
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Kernel: KernelDataConfig{
			EventLogDir:         "./data/eventlog",
			StateDBPath:         "./data/state/state.db",
			RegistryDBPath:      "./data/state/registry.db",
			TransparencyLogPath: "./data/transparency.jsonl",
		},
		RateLimit: RateLimitConfig{
			RequestsPerMinPerIP: 300,
		},
		WebSocket: WebSocketConfig{
			MaxConnections: 100,
			WriteTimeout:   10 * time.Second,
			PingInterval:   30 * time.Second,
			MaxDuration:    24 * time.Hour,
		},
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return cfg, err
			}
		} else {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return cfg, err
			}
		}
	}

	// Environment variable overrides
	if v := os.Getenv("TABULUM_OBSERVE_LISTEN_ADDR"); v != "" {
		cfg.Server.ListenAddr = v
	}
	if v := os.Getenv("TABULUM_OBSERVE_EVENTLOG_DIR"); v != "" {
		cfg.Kernel.EventLogDir = v
	}
	if v := os.Getenv("TABULUM_OBSERVE_STATE_DB"); v != "" {
		cfg.Kernel.StateDBPath = v
	}
	if v := os.Getenv("TABULUM_OBSERVE_REGISTRY_DB"); v != "" {
		cfg.Kernel.RegistryDBPath = v
	}
	if v := os.Getenv("TABULUM_OBSERVE_RATE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.RateLimit.RequestsPerMinPerIP = n
		}
	}

	// Fill zero values with defaults
	defaults := DefaultConfig()
	if cfg.Server.ListenAddr == "" {
		cfg.Server.ListenAddr = defaults.Server.ListenAddr
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = defaults.Server.ReadTimeout
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = defaults.Server.WriteTimeout
	}
	if cfg.RateLimit.RequestsPerMinPerIP == 0 {
		cfg.RateLimit.RequestsPerMinPerIP = defaults.RateLimit.RequestsPerMinPerIP
	}
	if cfg.WebSocket.MaxConnections == 0 {
		cfg.WebSocket.MaxConnections = defaults.WebSocket.MaxConnections
	}
	if cfg.WebSocket.WriteTimeout == 0 {
		cfg.WebSocket.WriteTimeout = defaults.WebSocket.WriteTimeout
	}
	if cfg.WebSocket.PingInterval == 0 {
		cfg.WebSocket.PingInterval = defaults.WebSocket.PingInterval
	}
	if cfg.WebSocket.MaxDuration == 0 {
		cfg.WebSocket.MaxDuration = defaults.WebSocket.MaxDuration
	}

	return cfg, nil
}
