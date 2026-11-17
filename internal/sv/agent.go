package sv

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/viminizer/remux/internal/harness"
)

// Run is one headless agent run: a supervisor turn or a worker.
type Run struct {
	Agent    string // claude or codex
	Model    string // empty: the CLI default
	Effort   string // medium or high
	Dir      string
	Prompt   string
	Session  string // resume this session; empty starts a new one
	ReadOnly bool
	Stream   string   // JSONL output is appended here
	Hook     string   // a command Claude runs after each tool call, for the mailbox
	Env      []string // added to the environment
	Timeout  time.Duration
}

// Result is how a run ended.
type Result struct {
	Code    int
	Final   string    // the agent's last message
	Session string    // the session to resume
	Limit   time.Time // set when it ended on a usage limit
	Missing bool      // the session to resume no longer exists
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// args builds the command line. The prompt goes in on stdin.
//
// Claude gets the same base as the harness loops (see harness.Loop.command):
// every permission, the repo's settings but not Kevin's user plugins and
// hooks, and his deny rules carried over. Read-only adds a tool deny list;
// Bash can still write, so the role's instructions say read-only too.
// Codex in read-only runs in its own read-only sandbox instead.
func (r *Run) args(last string) []string {
	if r.Agent == "codex" {
		a := []string{"codex", "exec"}
		if r.Session != "" {
			a = append(a, "resume", r.Session)
		}
		a = append(a, "--json", "--skip-git-repo-check", "-c", `model_reasoning_effort="`+r.Effort+`"`, "-o", last)
		if r.ReadOnly {
			a = append(a, "-c", `sandbox_mode="read-only"`, "-c", `approval_policy="never"`)
		} else {
			a = append(a, "--dangerously-bypass-approvals-and-sandbox")
		}
		if r.Model != "" {
			a = append(a, "-m", r.Model)
		}
		return append(a, "-")
	}
	a := []string{"claude", "-p", "--dangerously-skip-permissions", "--effort", r.Effort,
		"--setting-sources", "project,local", "--output-format", "stream-json", "--verbose"}
	if r.Session != "" {
		a = append(a, "--resume", r.Session)
	} else {
		r.Session = newUUID()
		a = append(a, "--session-id", r.Session)
	}
	settings := map[string]any{}
	if deny := harness.UserDeny(); deny != "" {
		json.Unmarshal([]byte(deny), &settings)
	}
	if r.Hook != "" {
		settings["hooks"] = map[string]any{"PostToolUse": []any{map[string]any{"matcher": "*",
			"hooks": []any{map[string]any{"type": "command", "command": r.Hook}}}}}
	}
	if len(settings) > 0 {
		a = append(a, "--settings", marshal(settings))
	}
	if r.ReadOnly {
		a = append(a, "--disallowedTools", "Edit,Write,NotebookEdit")
	}
	if r.Model != "" {
		a = append(a, "--model", r.Model)
	}
	return a
}

// Exec runs the agent to the end. Its JSON stream is appended to r.Stream,
// and a short readable line per step goes to stdout - the tmux pane, which
// is where Kevin watches it.
func (r *Run) Exec(ctx context.Context) Result {
	if r.Timeout == 0 {
		r.Timeout = harness.RunTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	last, err := os.CreateTemp("", "remux-sv-last-*.txt")
	if err != nil {
		return Result{Code: -1, Final: err.Error()}
	}
	last.Close()
	defer os.Remove(last.Name())
	scratch, err := os.MkdirTemp("", "remux-scratch-*")
	if err != nil {
		return Result{Code: -1, Final: err.Error()}
	}
	defer os.RemoveAll(scratch)

	resumed := r.Session != ""
	args := r.args(last.Name())
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// Its own process group, killed whole on timeout, as in the harness.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	cmd.Dir = r.Dir
	cmd.Env = append(append(os.Environ(), "REMUX_SCRATCH="+scratch), r.Env...)
	cmd.Stdin = strings.NewReader(r.Prompt + "\n\nPut scratch files in $REMUX_SCRATCH, not /tmp. It is deleted when this run ends.\n")
	stream := io.Discard
	if r.Stream != "" {
		if f, err := os.OpenFile(r.Stream, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			defer f.Close()
			stream = f
		}
	}
	var errb bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &errb)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Result{Code: -1, Final: err.Error()}
	}
	res := Result{Session: r.Session}
	if err := cmd.Start(); err != nil {
		return Result{Code: -1, Final: err.Error()}
	}
	var tail []string
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		stream.Write(append(bytes.Clone(line), '\n'))
		final, session, show := readEvent(line)
		if final != "" {
			res.Final = final
		}
		if session != "" {
			res.Session = session
		}
		if show != "" {
			fmt.Println(show)
			tail = append(tail, show)
			if len(tail) > 40 {
				tail = tail[1:]
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		log.Printf("agent: %v", err)
		res.Code = -1
		if ee, ok := err.(*exec.ExitError); ok {
			res.Code = ee.ExitCode()
		}
	}
	if b, _ := os.ReadFile(last.Name()); len(bytes.TrimSpace(b)) > 0 {
		res.Final = strings.TrimSpace(string(b))
	}
	if res.Code != 0 {
		end := strings.Join(tail, "\n") + "\n" + errb.String() + "\n" + res.Final
		if until, ok := harness.UsageLimit(end, time.Now()); ok {
			res.Limit = until
		}
		res.Missing = resumed && strings.Contains(strings.ToLower(end), "no conversation found")
	}
	return res
}

// readEvent pulls what matters out of one stream line: the final message,
// the session id, and a short line for the pane.
//
// Claude's stream-json: {"type":"system","session_id":...}, assistant
// messages with text and tool_use parts, and {"type":"result","result":...}.
// Codex's --json: {"type":"thread.started","thread_id":...} and
// item.completed events whose item is a message, a command or a change.
func readEvent(line []byte) (final, session, show string) {
	var e struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		ThreadID  string `json:"thread_id"`
		Result    string `json:"result"`
		Message   struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
		Item struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Command string `json:"command"`
		} `json:"item"`
	}
	if json.Unmarshal(line, &e) != nil {
		return "", "", ""
	}
	session = e.SessionID
	if e.ThreadID != "" {
		session = e.ThreadID
	}
	switch e.Type {
	case "result":
		return e.Result, session, ""
	case "assistant":
		var parts []string
		for _, c := range e.Message.Content {
			switch c.Type {
			case "text":
				parts = append(parts, c.Text)
			case "tool_use":
				parts = append(parts, "→ "+c.Name+" "+short(string(c.Input), 140))
			}
		}
		return "", session, strings.Join(parts, "\n")
	case "item.completed":
		switch {
		case e.Item.Command != "":
			return "", session, "→ " + short(e.Item.Command, 140)
		case e.Item.Text != "":
			return "", session, e.Item.Text
		}
	}
	return "", session, ""
}

func short(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
