package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
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

// accessPromptedPath marks that the Accessibility prompt has been shown for
// the current wait. It lives next to the config so it survives the process
// restarts that waiting for the grant requires.
func accessPromptedPath() string {
	return filepath.Join(filepath.Dir(configPath()), "access-prompted")
}

// accessPromptAge is how long a marker suppresses the prompt. After that the
// user has walked away from the dialog; show it again next time they look.
const accessPromptAge = time.Hour

// MarkAccessPrompted records that the Accessibility prompt has been shown.
// It returns true if the caller should show the prompt now: the first time,
// or when the previous prompt is old enough to have been forgotten.
func MarkAccessPrompted() bool {
	p := accessPromptedPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return true
	}
	if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) < accessPromptAge {
		return false
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		return true
	}
	return true
}

// ClearAccessPrompted removes the marker so the next time access is missing
// (an upgrade, a revoked grant) the prompt is shown again.
func ClearAccessPrompted() {
	os.Remove(accessPromptedPath())
}
