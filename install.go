package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/viminizer/remux/internal/config"
)

// remux runs as a LaunchAgent, not a system daemon.
//
// This is not a style choice. A daemon runs as root, and the tmux server it
// would need to talk to belongs to Kevin's own login session - root cannot see
// it at all. A LaunchAgent runs as him, in his session, which is the only way
// this works.
const launchLabel = "com.viminizer.remux"

// installedBinary is where install copies the binary to.
//
// The plist must not point at wherever the download happened to be run from.
// macOS protects ~/Desktop, ~/Documents and ~/Downloads with TCC, and a
// LaunchAgent has no consent to read them: pointing launchd at a binary under
// ~/Desktop produced a process that hung inside dyld before main() ever ran,
// with an empty log and no error anywhere. Copying to ~/.local/bin also means
// deleting the download later does not break the installed service.
func installedBinary() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin", "remux"), nil
}

// copyBinary installs src at dst, replacing whatever is there.
//
// The file is written under a temporary name and renamed, so a running remux
// keeps its own open file and an interrupted copy cannot leave a truncated
// binary that launchd would then try to start.
func copyBinary(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist"), nil
}

func plistBody(binary, logDir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>

  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
  </array>

  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>

  <!-- Without a throttle, a start that fails fast - a tailnet with HTTPS
       certificates switched off, say - would have launchd respawning remux in
       a tight loop instead of leaving one readable error in the log. -->
  <key>ThrottleInterval</key>
  <integer>10</integer>

  <!-- tmux lives in /usr/local/bin or /opt/homebrew/bin; launchd's default
       PATH has neither. -->
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>

  <key>StandardOutPath</key>
  <string>%s/remux.log</string>
  <key>StandardErrorPath</key>
  <string>%s/remux.err.log</string>

  <key>ProcessType</key>
  <string>Background</string>
</dict>
</plist>
`, launchLabel, binary, logDir, logDir)
}

func cmdInstall() error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return err
	}

	dir, err := config.EnsureDir()
	if err != nil {
		return err
	}

	target, err := installedBinary()
	if err != nil {
		return err
	}
	if target != binary {
		if err := copyBinary(binary, target); err != nil {
			return fmt.Errorf("copy binary to %s: %w", target, err)
		}
	}

	path, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Unload any previous copy first, so install is safe to run twice.
	_ = launchctl("bootout", guiTarget()+"/"+launchLabel)

	if err := os.WriteFile(path, []byte(plistBody(target, dir)), 0o644); err != nil {
		return err
	}

	if err := launchctl("bootstrap", guiTarget(), path); err != nil {
		// Leave no half-installed state behind: a plist that exists but was
		// never loaded is worse than no install, because `status` would lie.
		os.Remove(path)
		return fmt.Errorf("could not load the launch agent: %w", err)
	}

	fmt.Printf("\n  installed  %s\n", path)
	fmt.Printf("  binary     %s\n", target)
	fmt.Printf("  logs       %s/remux.log\n\n", dir)
	fmt.Println("  It is running now and will start again at login.")
	fmt.Println("  First run only: open the login URL printed in the log to enrol the node.")
	fmt.Printf("      tail -f %s/remux.log\n\n", dir)
	return nil
}

func cmdUninstall() error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	// Unload first; removing a loaded plist leaves the process running with
	// nothing on disk describing it.
	_ = launchctl("bootout", guiTarget()+"/"+launchLabel)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if target, err := installedBinary(); err == nil {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	fmt.Println("\n  uninstalled. Node state and keys are still in ~/.config/remux -")
	fmt.Println("  delete that directory too if you want a clean slate.")
	return nil
}

func cmdRestart() error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("not installed - run: remux install")
	}

	// restart is how a new version is picked up, so refresh the installed
	// copy from whichever binary is running this command.
	if self, err := os.Executable(); err == nil {
		if self, err = filepath.EvalSymlinks(self); err == nil {
			if target, err := installedBinary(); err == nil && target != self {
				if err := copyBinary(self, target); err != nil {
					return fmt.Errorf("update %s: %w", target, err)
				}
				fmt.Printf("  updated    %s\n", target)
			}
		}
	}

	_ = launchctl("bootout", guiTarget()+"/"+launchLabel)
	if err := launchctl("bootstrap", guiTarget(), path); err != nil {
		return err
	}
	fmt.Println("\n  restarted.")
	return nil
}

func cmdStatus() error {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Default()
	}

	fmt.Printf("\n  remux %s\n", version)
	preflight(cfg, false).Print()

	fmt.Println()
	path, _ := plistPath()
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("  ✓ launch agent          %s\n", path)
		if out, err := exec.Command("launchctl", "print", guiTarget()+"/"+launchLabel).Output(); err == nil {
			fmt.Printf("  ✓ loaded                %s\n", pidFrom(string(out)))
		} else {
			fmt.Println("  ✗ loaded                plist exists but launchd does not have it - run: remux restart")
		}
	} else {
		fmt.Println("  · launch agent          not installed - run: remux install")
	}

	dir, _ := config.Dir()
	if _, err := os.Stat(filepath.Join(dir, "tsnet")); err == nil {
		fmt.Printf("  ✓ tailnet node state    %s/tsnet\n", dir)
	} else {
		fmt.Println("  · tailnet node state    not enrolled yet - run remux once and open the login URL")
	}

	if cfg.AllowLogin != "" {
		fmt.Printf("  ✓ allowed identity      %s\n", cfg.AllowLogin)
	} else {
		fmt.Println("  · allowed identity      not pinned yet - the first tailnet user to connect")
	}
	fmt.Println()
	return nil
}

// guiTarget is the launchd domain for the current user's GUI session, which is
// where a LaunchAgent belongs.
func guiTarget() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func launchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("launchctl %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

func pidFrom(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "pid = ") {
			return t
		}
	}
	return "loaded"
}

// printQR puts the URL on screen as a scannable code, because nobody knows
// their tailnet name well enough to type it into a phone keyboard.
func printQR(url string) {
	if url == "" {
		return
	}
	q, err := qrcode.New(url, qrcode.Low)
	if err != nil {
		return
	}
	fmt.Print(q.ToSmallString(false))
}
