package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	APIKey string `json:"api_key"`

	// Model overrides the transcription model. Empty means the default,
	// whisper-large-v3 (most accurate). whisper-large-v3-turbo answers a
	// little sooner at a small cost in accuracy.
	Model string `json:"model,omitempty"`

	// Language is an optional ISO-639-1 hint such as "en". Naming the language
	// lets the model skip detecting it. Empty means auto-detect.
	Language string `json:"language,omitempty"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "whisprgo", "config.json")
}

func Load() (*Config, error) {
	data, err := os.ReadFile(configPath())
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Save(cfg *Config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
