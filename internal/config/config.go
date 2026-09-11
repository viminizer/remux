// Package config holds runtime settings and the on-disk state directory.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
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

	// Repos is the GitHub watchlist, as "owner/name", in the order Kevin
	// added them. Empty is a normal state: the GitHub screen opens on its
	// empty Repos tab with the Add button.
	Repos []string `json:"repos"`
	// GitHubMS is how often the shared GitHub poller runs. A minute is
	// slow enough to be invisible against the 5000/hour limit and fast
	// enough that a red CI run reaches the phone while he still cares.
	GitHubMS int `json:"githubMs"`
	// NotifyCI fires a push when one of your pull requests turns red or
	// somebody asks you for a review.
	NotifyCI bool `json:"notifyCi"`
	// IgnoreChecks names checks that do not count as a failure, matched
	// case-insensitively as substrings. Vercel is the default because a
	// failed preview deploy is not a thing a pull request waits on a
	// person for, and one of them turns the whole rollup red.
	IgnoreChecks []string `json:"ignoreChecks"`
	// Muted are inbox rows dismissed from the phone, keyed "owner/name#42"
	// and valued with the item's own updatedAt at the moment it was muted.
	// Anything newer means the item actually moved, so the mute lapses and
	// the row comes back rather than disappearing for good.
	Muted map[string]time.Time `json:"muted,omitempty"`
	// NamePanes lets a cheap model read each agent pane and name what it is
	// working on. It is the only thing in remux that costs money, so it gets
	// a switch - the state glyph beside the name is free and stays on either
	// way. Gated, it is a few cents a day; the gates are in internal/titler.
	NamePanes bool `json:"namePanes"`
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
		GitHubMS:      60000,
		NotifyCI:      true,
		IgnoreChecks:  []string{"Vercel"},
		NamePanes:     true,
	}
}

func (c *Config) Poll() time.Duration { return time.Duration(c.PollMS) * time.Millisecond }
func (c *Config) TreePoll() time.Duration {
	return time.Duration(c.TreeMS) * time.Millisecond
}

// GitHubPoll is clamped: a value under 15 seconds would spend the hourly API
// budget on a screen nobody is looking at, and a zero means an old config file
// written before the GitHub screen existed.
func (c *Config) GitHubPoll() time.Duration {
	const floor = 15 * time.Second
	d := time.Duration(c.GitHubMS) * time.Millisecond
	if d < floor {
		return time.Minute
	}
	return d
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

// saveMu serialises the read-modify-write in Update.
var saveMu sync.Mutex

// Update applies fn to what is on disk and writes it back.
//
// This is what every runtime settings change goes through, and the reason is
// that command line flags overwrite the in-memory Config: --port, --lines,
// --poll and --hostname all do. Saving the in-memory copy would bake whichever
// flags this process happened to start with into the file, so a development
// server run once on another port would move the installed service to it.
// Reading the file first means only the fields fn touches ever change.
func Update(fn func(*Config)) (*Config, error) {
	saveMu.Lock()
	defer saveMu.Unlock()

	c, err := Load()
	if err != nil {
		return nil, err
	}
	fn(c)
	if err := save(c); err != nil {
		return nil, err
	}
	return c, nil
}

// Save writes the config file. Prefer Update for anything the running process
// changes; this is for the install path, which owns the whole file.
func Save(c *Config) error {
	saveMu.Lock()
	defer saveMu.Unlock()
	return save(c)
}

func save(c *Config) error {
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
