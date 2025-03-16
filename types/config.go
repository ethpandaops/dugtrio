package types

import "time"

// Config is a struct to hold the configuration data
type Config struct {
	Debug     bool              `yaml:"debug" envconfig:"DUGTRIO_DEBUG"`
	Logging   *LoggingConfig    `yaml:"logging"`
	Endpoints []*EndpointConfig `yaml:"endpoints"`
	Server    *ServerConfig     `yaml:"server"`
	Pool      *PoolConfig       `yaml:"pool"`
	Proxy     *ProxyConfig      `yaml:"proxy"`
	Frontend  *FrontendConfig   `yaml:"frontend"`
	Metrics   *MetricsConfig    `yaml:"metrics"`
}

type LoggingConfig struct {
	OutputLevel  string `yaml:"outputLevel" envconfig:"LOGGING_OUTPUT_LEVEL"`
	OutputStderr bool   `yaml:"outputStderr" envconfig:"LOGGING_OUTPUT_STDERR"`

	FilePath  string `yaml:"filePath" envconfig:"LOGGING_FILE_PATH"`
	FileLevel string `yaml:"fileLevel" envconfig:"LOGGING_FILE_LEVEL"`
}

type EndpointConfig struct {
	Url      string            `yaml:"url"`
	Name     string            `yaml:"name"`
	Priority int               `yaml:"priority"`
	Weight   int               `yaml:"weight"`
	Headers  map[string]string `yaml:"headers"`
}

type ServerConfig struct {
	Port string `yaml:"port" envconfig:"SERVER_PORT"`
	Host string `yaml:"host" envconfig:"SERVER_HOST"`

	ReadTimeout  time.Duration `yaml:"readTimeout" envconfig:"SERVER_READ_TIMEOUT"`
	WriteTimeout time.Duration `yaml:"writeTimeout" envconfig:"SERVER_WRITE_TIMEOUT"`
	IdleTimeout  time.Duration `yaml:"idleTimeout" envconfig:"SERVER_IDLE_TIMEOUT"`
}

type PoolConfig struct {
	FollowDistance  uint64 `yaml:"followDistance" envconfig:"POOL_FOLLOW_DISTANCE"`
	MaxHeadDistance uint64 `yaml:"maxHeadDistance" envconfig:"POOL_MAX_HEAD_DISTANCE"`
	SchedulerMode   string `yaml:"schedulerMode" envconfig:"POOL_SCHEDULER_MODE"`
}

type ProxyConfig struct {
	ProxyCount      uint64        `yaml:"proxyCount" envconfig:"PROXY_PROXY_COUNT"`
	CallTimeout     time.Duration `yaml:"callTimeout" envconfig:"PROXY_CALL_TIMEOUT"`
	SessionTimeout  time.Duration `yaml:"sessionTimeout" envconfig:"PROXY_SESSION_TIMEOUT"`
	StickyEndpoint  bool          `yaml:"stickyEndpoint" envconfig:"PROXY_STICKY_ENDPOINT"`
	CallRateLimit   uint64        `yaml:"callRateLimit" envconfig:"PROXY_CALL_RATE_LIMIT"`
	CallRateBurst   uint64        `yaml:"callRateBurst" envconfig:"PROXY_CALL_RATE_BURST"`
	BlockedPathsStr string        `envconfig:"PROXY_BLOCKED_PATHS"`
	BlockedPaths    []string      `yaml:"blockedPaths"`
	Auth            *AuthConfig   `yaml:"auth"`

	// RebalanceInterval is how often to check for session imbalances (0 = disabled)
	RebalanceInterval time.Duration `yaml:"rebalanceInterval"`
	// RebalanceThreshold is the percentage difference from ideal distribution that triggers rebalancing (0-1)
	RebalanceThreshold float64 `yaml:"rebalanceThreshold"`
	// RebalanceMaxSweep is the maximum number of sessions to rebalance per run (0 = unlimited)
	RebalanceMaxSweep int `yaml:"rebalanceMaxSweep"`
}

type FrontendConfig struct {
	Enabled  bool   `yaml:"enabled" envconfig:"FRONTEND_ENABLED"`
	Debug    bool   `yaml:"debug" envconfig:"FRONTEND_DEBUG"`
	Pprof    bool   `yaml:"pprof" envconfig:"FRONTEND_PPROF"`
	Minify   bool   `yaml:"minify" envconfig:"FRONTEND_MINIFY"`
	SiteName string `yaml:"siteName" envconfig:"FRONTEND_SITE_NAME"`
}

type AuthConfig struct {
	Required bool   `yaml:"required" envconfig:"PROXY_AUTH_REQUIRED"`
	Password string `yaml:"password" envconfig:"PROXY_AUTH_PASSWORD"`
}

type MetricsConfig struct {
	Enabled bool `yaml:"enabled" envconfig:"METRICS_ENABLED"`
}
