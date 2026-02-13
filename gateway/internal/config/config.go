package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Engines     []EngineConfig    `yaml:"engines"`
	Session     SessionConfig     `yaml:"session"`
	HealthCheck HealthCheckConfig `yaml:"health_check"`
}

type ServerConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	WebSocketPath  string `yaml:"websocket_path"`
	MaxConnections int    `yaml:"max_connections"`
}

type EngineConfig struct {
	ID   int    `yaml:"id"`
	Addr string `yaml:"addr"`
}

type SessionConfig struct {
	TimeoutSec     int `yaml:"timeout_sec"`
	MaxDurationSec int `yaml:"max_duration_sec"`
}

type HealthCheckConfig struct {
	IntervalSec      int `yaml:"interval_sec"`
	FailureThreshold int `yaml:"failure_threshold"`
}

func DefaultConfig() *Config {
	engines := make([]EngineConfig, 8)
	for i := 0; i < 8; i++ {
		engines[i] = EngineConfig{
			ID:   i,
			Addr: fmt.Sprintf("localhost:%d", 50050+i),
		}
	}

	return &Config{
		Server: ServerConfig{
			Host:           "0.0.0.0",
			Port:           8080,
			WebSocketPath:  "/ws/asr",
			MaxConnections: 1024,
		},
		Engines: engines,
		Session: SessionConfig{
			TimeoutSec:     300,
			MaxDurationSec: 3600,
		},
		HealthCheck: HealthCheckConfig{
			IntervalSec:      5,
			FailureThreshold: 3,
		},
	}
}

func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	return cfg, nil
}
