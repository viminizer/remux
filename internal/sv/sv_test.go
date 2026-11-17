package sv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolate moves Root and Home into a temp dir with the given defaults.
func isolate(t *testing.T, claude, codex string) string {
	t.Helper()
	dir := t.TempDir()
	Root = func() string { return filepath.Join(dir, "sv") }
	Home = func() string { return dir }
	os.MkdirAll(filepath.Join(dir, ".claude"), 0o700)
	os.MkdirAll(filepath.Join(dir, ".codex"), 0o700)
	os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(`{"model":"`+claude+`"}`), 0o600)
	os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte("model = \""+codex+"\"\n[profiles.x]\nmodel = \"gpt-6-astra\"\n"), 0o600)
	t.Setenv("HOME", dir) // config.Load for the limits
	return dir
}

func TestCheckModel(t *testing.T) {
	isolate(t, "opus[1m]", "gpt-6.1-sol")
	for _, c := range []struct {
		agent, model string
		ok           bool
	}{
		{"claude", "", true},
		{"claude", "sonnet", true},
		{"claude", "opus", true},
		{"claude", "claude-haiku-5-5", true},
		{"claude", "mystery", false},
		{"codex", "gpt-6.1-terra", true},
		{"codex", "gpt-6.1-sol", true},
		{"codex", "gpt-6-astra", false},
		{"gemini", "", false},
	} {
		if err := CheckModel(c.agent, c.model); (err == nil) != c.ok {
			t.Errorf("CheckModel(%s, %q) = %v, want ok=%v", c.agent, c.model, err, c.ok)
		}
	}
	// A weaker default lowers the ceiling.
	isolate(t, "sonnet", "gpt-6-luna")
	if CheckModel("claude", "opus") == nil || CheckModel("codex", "gpt-6.1-sol") == nil {
		t.Error("a model above the default was allowed")
	}
	if got := strings.Join(Allowed("claude"), ","); got != "haiku,sonnet" {
		t.Errorf("Allowed = %s", got)
	}
}

func TestCreateNamesAndChat(t *testing.T) {
	isolate(t, "opus", "gpt-6.1-sol")
	a, err := Create("/code/My Repo", "claude", "Refactor the payments module\nmore detail")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "sv-my-repo-refactor-payments-module" || a.Goal != "Refactor the payments module" {
		t.Fatalf("got %q %q", a.ID, a.Goal)
	}
	b, _ := Create("/code/My Repo", "claude", "Refactor the payments module")
	if b.ID != a.ID+"-2" {
		t.Fatalf("second id %q", b.ID)
	}
	AddMessage(a.ID, "kevin", "one")
	AddMessage(a.ID, "supervisor", "answer")
	AddMessage(a.ID, "kevin", "two")
	AddMessage(a.ID, "kevin", "three")
	if u := Unanswered(a.ID); len(u) != 2 || u[0].Text != "two" {
		t.Fatalf("unanswered %+v", u)
	}
	if len(List()) != 2 {
		t.Fatal("list")
	}
	if ValidID("sv-../x") || ReadFile(a.ID, "../status.json") != "" {
		t.Fatal("path escape")
	}
}

func TestTakeSlot(t *testing.T) {
	isolate(t, "opus", "gpt-6.1-sol")
	mk := func(repo string) string {
		x, _ := Create(repo, "claude", "task "+repo)
		Update(x.ID, func(t *Task) error { t.Phase = Queued; return nil })
		return x.ID
	}
	a, b, c := mk("/r/one"), mk("/r/one"), mk("/r/two")
	if ok, _ := takeSlot(a); !ok {
		t.Fatal("first slot")
	}
	if ok, why := takeSlot(b); ok || !strings.Contains(why, "this repo") {
		t.Fatalf("second in same repo: %v %q", ok, why)
	}
	if ok, _ := takeSlot(c); !ok {
		t.Fatal("other repo")
	}
	if x, _ := Load(a); x.Phase != Running {
		t.Fatal(x.Phase)
	}
}

func TestReadEvent(t *testing.T) {
	final, session, _ := readEvent([]byte(`{"type":"result","session_id":"abc","result":"all done"}`))
	if final != "all done" || session != "abc" {
		t.Fatalf("claude result: %q %q", final, session)
	}
	_, session, _ = readEvent([]byte(`{"type":"thread.started","thread_id":"t-1"}`))
	if session != "t-1" {
		t.Fatal("codex thread")
	}
	_, _, show := readEvent([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test"}}]}}`))
	if !strings.Contains(show, "Bash") {
		t.Fatal(show)
	}
}

func TestInbox(t *testing.T) {
	isolate(t, "opus", "gpt-6.1-sol")
	x, _ := Create("/r/a", "claude", "inbox test")
	Send(x.ID, 0, "Kevin", "hello")
	if got := FormatInbox(Take(x.ID, 0)); !strings.Contains(got, "hello") {
		t.Fatal(got)
	}
	if Take(x.ID, 0) != nil {
		t.Fatal("taken twice")
	}
	if Send(x.ID, 3, "s", "no such worker") == nil {
		t.Fatal("sent to a missing worker")
	}
}

func TestCheckDir(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(repo, ".claude", "worktrees", "a")
	run("worktree", "add", "-q", "-b", "a", wt)
	repo, _ = filepath.EvalSymlinks(repo)
	wt = filepath.Join(repo, ".claude", "worktrees", "a")
	ctx := context.Background()
	if err := checkDir(ctx, repo, repo, false); err == nil {
		t.Error("a writer was allowed in the main checkout")
	}
	if err := checkDir(ctx, repo, repo, true); err != nil {
		t.Errorf("read-only in the repo: %v", err)
	}
	if err := checkDir(ctx, repo, wt, false); err != nil {
		t.Errorf("writer in its worktree: %v", err)
	}
	if err := checkDir(ctx, repo, "/tmp", true); err == nil {
		t.Error("a dir outside the repo was allowed")
	}
}
