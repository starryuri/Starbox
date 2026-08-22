package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Task is one top-level job definition.
type Task struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`   // metrics | exec | http
	EverySeconds int               `json:"every_seconds"`
	Command      string            `json:"command"`
	Args         []string          `json:"args"`
	Env          map[string]string `json:"env"`
	URL          string            `json:"url"`
	Path         string            `json:"path"`
	Limit        int               `json:"limit"`
	TimeoutSec   int               `json:"timeout_seconds"`
}

// Config is the whole config.json file.
type Config struct {
	HTTPAddr string `json:"http_addr"`
	Tasks    []Task `json:"tasks"`
}

// Load reads and parses a JSON config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}
