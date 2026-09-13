package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// tempHome points the config directory at a throwaway home. Without it a test
// would write over the real ~/.config/remux, which holds the pinned tailnet
// identity and the watchlist.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("config dir is %s, outside the temp home %s - refusing to write", dir, home)
	}
	return dir
}

// TestUpdateLeavesFlagsAlone is the pin path: --port and --lines live in the
// in-memory Config, and persisting the identity must not carry them to disk.
func TestUpdateLeavesFlagsAlone(t *testing.T) {
	tempHome(t)
	if err := Save(Default()); err != nil { // what the install path wrote
		t.Fatal(err)
	}

	// This process was started with --port 9999 --lines 1000.
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Port, cfg.Lines = 9999, 1000

	// What the tailnet pin callback does with it.
	if _, err := Update(func(c *Config) { c.AllowLogin = "kevin@example.com" }); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.AllowLogin != "kevin@example.com" {
		t.Errorf("allowLogin is %q, want the pinned identity", got.AllowLogin)
	}
	if got.Port != 7399 || got.Lines != 400 {
		t.Errorf("flags reached the file: port %d lines %d, want 7399 and 400", got.Port, got.Lines)
	}
}

// TestSaveIsAtomic pins both halves of an atomic replace: the new file is a
// different file renamed into place rather than the old one truncated, and a
// concurrent reader never catches a half-written config.json.
func TestSaveIsAtomic(t *testing.T) {
	dir := tempHome(t)
	p := filepath.Join(dir, "config.json")

	c := Default()
	c.AllowLogin = "kevin@example.com"
	for i := 0; i < 4000; i++ {
		c.Repos = append(c.Repos, fmt.Sprintf("viminizer/repo-%04d", i))
	}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}

	// An in-place write keeps the same file; a rename replaces it.
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("config.json was written in place - a crash mid-write would truncate it")
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var reads, bad int
	var first error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			got, err := Load()
			mu.Lock()
			reads++
			if err != nil || got.AllowLogin != c.AllowLogin || len(got.Repos) != len(c.Repos) {
				bad++
				if first == nil {
					first = fmt.Errorf("err=%v allowLogin=%q repos=%d", err, got.AllowLogin, len(got.Repos))
				}
			}
			mu.Unlock()
		}
	}()

	for i := 0; i < 30; i++ {
		if err := Save(c); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	if bad > 0 {
		t.Fatalf("%d of %d reads saw a half-written config.json, first: %v", bad, reads, first)
	}
	if reads == 0 {
		t.Fatal("the reader never ran")
	}

	// The replacement has to keep owner-only mode: this directory holds the
	// tsnet node key and the VAPID private key.
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("config.json is mode %v, want 0600", st.Mode().Perm())
	}

	// And it has to clean up after itself rather than leaving temp files.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("left a temp file behind: %s", e.Name())
		}
	}
}
