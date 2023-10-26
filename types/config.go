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
	BlockedPathsStr string   `envconfig:"PROXY_BLOCKED_PATHS"`
	BlockedPaths    []string `yaml:"blockedPaths"`
}

type FrontendConfig struct {
	Enabled  bool   `yaml:"enabled" envconfig:"FRONTEND_ENABLED"`
	Debug    bool   `yaml:"debug" envconfig:"FRONTEND_DEBUG"`
	Pprof    bool   `yaml:"pprof" envconfig:"FRONTEND_PPROF"`
	Minify   bool   `yaml:"minify" envconfig:"FRONTEND_MINIFY"`
	SiteName string `yaml:"siteName" envconfig:"FRONTEND_SITE_NAME"`
}
