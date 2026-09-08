package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/viminizer/remux/internal/config"
)

// Preflight catches the boring failures before they get mistaken for bugs:
// tmux missing from PATH, an unwritable config dir, a port already in use.
// It runs on every start, not just on install.
type Preflight struct {
	checks []check
}

type check struct {
	name string
	ok   bool
	note string
	// fatal marks a check that must pass for remux to serve at all.
	fatal bool
}

func preflight(cfg *config.Config, local bool) *Preflight {
	p := &Preflight{}

	// tmux on PATH
	if path, err := exec.LookPath("tmux"); err != nil {
		p.add("tmux on PATH", false, "not found - install it with: brew install tmux", true)
	} else {
		ver := "unknown version"
		if out, err := exec.Command(path, "-V").Output(); err == nil {
			ver = strings.TrimSpace(string(out))
		}
		p.add("tmux on PATH", true, fmt.Sprintf("%s (%s)", path, ver), true)
	}

	// tmux server running - not fatal, the UI has an empty state for it
	if err := exec.Command("tmux", "list-sessions").Run(); err != nil {
		p.add("tmux server", false, "no server running yet - remux will show an empty workspace", false)
	} else {
		p.add("tmux server", true, "running", false)
	}

	// config dir writable
	dir, err := config.EnsureDir()
	if err != nil {
		p.add("config dir writable", false, err.Error(), true)
	} else if err := writable(dir); err != nil {
		p.add("config dir writable", false, dir+": "+err.Error(), true)
	} else {
		p.add("config dir writable", true, dir, true)
	}

	// port free - only meaningful in --local; on the tailnet the node has
	// its own address and :443 does not collide with anything on the Mac.
	if local {
		addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
		if ln, err := net.Listen("tcp", addr); err != nil {
			p.add(fmt.Sprintf("port %d free", cfg.Port), false,
				"in use - another remux may already be running", true)
		} else {
			ln.Close()
			p.add(fmt.Sprintf("port %d free", cfg.Port), true, addr, true)
		}
	}

	return p
}

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func (p *Preflight) add(name string, ok bool, note string, fatal bool) {
	p.checks = append(p.checks, check{name: name, ok: ok, note: note, fatal: fatal})
}

// OK reports whether every fatal check passed.
func (p *Preflight) OK() bool {
	for _, c := range p.checks {
		if c.fatal && !c.ok {
			return false
		}
	}
	return true
}

func (p *Preflight) Print() {
	fmt.Println()
	for _, c := range p.checks {
		mark := "✓"
		if !c.ok {
			mark = "✗"
			if !c.fatal {
				mark = "·"
			}
		}
		fmt.Printf("  %s %-22s %s\n", mark, c.name, c.note)
	}
}

// configPath is where the launchd agent and status command look for state.
func configPath(name string) string {
	dir, err := config.Dir()
	if err != nil {
		return name
	}
	return filepath.Join(dir, name)
}
