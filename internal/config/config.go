// Package config holds runtime settings and the on-disk state directory.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Config is what remux needs to run. Everything has a working default, so a
// missing config file is normal rather than an error.
type Config struct {
	// AllowLogin is the single tailnet identity allowed through. Empty means
	// "trust the login that enrolled this node", which is filled in on first
	// run so the common case needs no configuration at all.
	AllowLogin string `json:"allowLogin"`

	Hostname string `json:"hostname"` // tsnet node name
	Port     int    `json:"port"`     // --local port
	Lines    int    `json:"lines"`    // default capture depth
	PollMS   int    `json:"pollMs"`   // subscribed-pane poll interval
	TreeMS   int    `json:"treeMs"`   // tree poll interval

	// NotifyWaiting fires a push when an agent starts waiting for an answer.
	NotifyWaiting bool `json:"notifyWaiting"`
	// NotifyDone fires a push when a long task finishes (busy -> idle).
	// Off by default: it is the chattier of the two.
	NotifyDone bool `json:"notifyDone"`
}

func Default() *Config {
	return &Config{
		Hostname:      "remux",
		Port:          7399,
		Lines:         400,
		PollMS:        400,
		TreeMS:        2000,
		NotifyWaiting: true,
		NotifyDone:    false,
	}
}

func (c *Config) Poll() time.Duration { return time.Duration(c.PollMS) * time.Millisecond }
func (c *Config) TreePoll() time.Duration {
	return time.Duration(c.TreeMS) * time.Millisecond
}

// Dir is ~/.config/remux, where node keys, VAPID keys, subscriptions and the
// audit log live.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "remux"), nil
}

// EnsureDir creates the config directory with owner-only permissions. It holds
// the tsnet node key and the VAPID private key, both of which are real
// credentials.
func EnsureDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config file, falling back to defaults when it is absent.
func Load() (*Config, error) {
	c := Default()
	p, err := path()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return Default(), err
	}
	return c, nil
}

// Save writes the config file.
func Save(c *Config) error {
	if _, err := EnsureDir(); err != nil {
		return err
	}
	p, err := path()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o600)
}
