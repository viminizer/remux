package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/viminizer/remux/internal/config"
)

// The cleanup loop removes what agents leave on the laptop after their work
// is done: folders and scratch files, dev servers and containers. Anything
// named after a closed issue or PR goes without asking. Anything that names
// no issue goes to the agent, which only decides; the loop does the removing,
// and only for the items it offered.

// CleanEvery is the gap between two sweeps.
var CleanEvery = 30 * time.Minute

// LeftoverAge is how long a folder or file must be untouched before it goes.
var LeftoverAge = 24 * time.Hour

// TrashKeep is how long removed folders wait in the holding folder.
var TrashKeep = 7 * 24 * time.Hour

// sweepHome and sweepTmp are where leftovers are looked for. Tests move them.
var sweepHome, _ = os.UserHomeDir()
var sweepTmp = "/tmp"

type leftover struct {
	Kind  string `json:"kind"` // path, process or container
	ID    string `json:"id"`   // the path, the pid or the container name
	Issue int    `json:"issue,omitempty"`
	Info  string `json:"info,omitempty"` // command line, or image and status
	old   bool   // a path untouched for LeftoverAge
}

func (x leftover) key() string { return x.Kind + " " + x.ID }

// leadingNumber matches an issue number at the start of a name. Five digits
// at most, so a date such as 20261014 is not read as one.
var leadingNumber = regexp.MustCompile(`^(\d{1,5})(?:[-_.]|$)`)

// issueNumber reads the issue or PR a name belongs to: educenter-2059-pg,
// .educenter-2018, issue-245, 1968-backend, educenter-pr1945.json. Zero when
// the name carries none.
func issueNumber(base, name string) int {
	s := strings.TrimLeft(strings.ToLower(base), ".")
	s = strings.TrimLeft(strings.TrimPrefix(s, name), "-_.")
	for _, p := range []string{"issue-", "pr-", "pr"} {
		if t := strings.TrimPrefix(s, p); t != s {
			s = t
			break
		}
	}
	m := leadingNumber.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// leftoverPaths lists the folders and files agents keep for a repo called
// name: ~/.local/share/<name>/*, ~/.<name>-*, /tmp/<name>*, and folders under
// the repo's .claude/worktrees that git no longer knows. A folder that names
// no issue but holds folders that do (observations/, .educenter-evidence/)
// is looked into instead of being offered whole.
func leftoverPaths(repo, name string, registered map[string]bool, now time.Time) []leftover {
	var out []leftover
	add := func(dir string, keep func(string) bool, open bool) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !keep(e.Name()) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			n := issueNumber(e.Name(), name)
			if n == 0 && open && e.IsDir() {
				if inner := numbered(p, name, now); len(inner) > 0 {
					out = append(out, inner...)
					continue
				}
			}
			out = append(out, pathLeftover(p, n, now))
		}
	}
	all := func(string) bool { return true }
	add(filepath.Join(sweepHome, ".local", "share", name), all, true)
	add(sweepHome, func(s string) bool { return strings.HasPrefix(s, "."+name+"-") }, true)
	add(sweepTmp, func(s string) bool { return strings.HasPrefix(s, name) }, true)
	add(filepath.Join(repo, ".claude", "worktrees"), func(s string) bool {
		return !registered[filepath.Join(repo, ".claude", "worktrees", s)]
	}, false)
	return out
}

// numbered lists the entries of dir that name an issue.
func numbered(dir, name string, now time.Time) []leftover {
	entries, _ := os.ReadDir(dir)
	var out []leftover
	for _, e := range entries {
		if n := issueNumber(e.Name(), name); n > 0 {
			out = append(out, pathLeftover(filepath.Join(dir, e.Name()), n, now))
		}
	}
	return out
}

func pathLeftover(p string, n int, now time.Time) leftover {
	x := leftover{Kind: "path", ID: p, Issue: n}
	if fi, err := os.Lstat(p); err == nil {
		x.old = now.Sub(fi.ModTime()) >= LeftoverAge
		if fi.IsDir() {
			x.Info = "folder"
		}
	}
	return x
}

// leftoverProcesses are the processes started from one of the given folders,
// such as a jar under ~/.educenter-2018 or vite in a worktree. ps lines are
// "pid command". The loops and their agents are never among them.
func leftoverProcesses(ps string, dirs []leftover, self int) []leftover {
	var out []leftover
	for _, line := range strings.Split(ps, "\n") {
		pidStr, cmd, ok := strings.Cut(strings.TrimSpace(line), " ")
		pid, err := strconv.Atoi(pidStr)
		if !ok || err != nil || pid == self {
			continue
		}
		if agentProcess(cmd) {
			continue
		}
		for _, d := range dirs {
			if mentions(cmd, d.ID) {
				if len(cmd) > 200 {
					cmd = cmd[:200]
				}
				out = append(out, leftover{Kind: "process", ID: pidStr, Issue: d.Issue, Info: cmd})
				break
			}
		}
	}
	return out
}

// agentProcess is a loop or an agent run, by any word of its command.
func agentProcess(cmd string) bool {
	for _, w := range strings.Fields(cmd) {
		if b := filepath.Base(w); b == "remux" || b == "codex" || b == "claude" {
			return true
		}
	}
	return false
}

// mentions reports whether cmd names path p itself or something inside it.
func mentions(cmd, p string) bool {
	for i := strings.Index(cmd, p); i >= 0; {
		end := i + len(p)
		if end == len(cmd) || cmd[end] == '/' || cmd[end] == ' ' || cmd[end] == '"' || cmd[end] == '\'' {
			return true
		}
		j := strings.Index(cmd[end:], p)
		if j < 0 {
			break
		}
		i = end + j
	}
	return false
}

// leftoverContainers are the docker containers named after the repo. Lines
// are "name\tstatus\timage".
func leftoverContainers(docker, name string) []leftover {
	var out []leftover
	for _, line := range strings.Split(docker, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 || !strings.HasPrefix(f[0], name+"-") {
			continue
		}
		out = append(out, leftover{Kind: "container", ID: f[0], Issue: issueNumber(f[0], name), Info: f[2] + ", " + f[1]})
	}
	return out
}

// judgment is what happens to one leftover, and why.
type judgment struct {
	leftover
	Remove bool   `json:"remove"`
	Why    string `json:"why"`
}

// judge decides every leftover that names an issue. The rest are returned
// for the agent.
func judge(items []leftover, closed, busy map[int]bool) (decided []judgment, ask []leftover) {
	for _, x := range items {
		switch {
		case x.Issue == 0:
			ask = append(ask, x)
		case busy[x.Issue]:
			decided = append(decided, judgment{x, false, "a loop is working on #" + strconv.Itoa(x.Issue)})
		case !closed[x.Issue]:
			decided = append(decided, judgment{x, false, "#" + strconv.Itoa(x.Issue) + " is open"})
		case x.Kind == "path" && !x.old:
			decided = append(decided, judgment{x, false, "changed in the last day"})
		default:
			decided = append(decided, judgment{x, true, "#" + strconv.Itoa(x.Issue) + " is closed"})
		}
	}
	return decided, ask
}

const cleanupRules = `You decide which leftovers on this laptop can be removed. The loop removes them, not you.

- Do not remove, stop, move or change anything yourself. Read only: ls, du, cat, ps,
  docker ps/inspect, git log, gh issue view, and the project's docs.
- Each item below names no issue, so read the project's docs (AGENTS.md, CLAUDE.md,
  docs/, .remux/) to learn what it is. Something the docs say to keep, share or never
  stop (a shared server, its database, a demo) stays.
- Remove only what is clearly finished scratch: comment drafts, JSON dumps, logs,
  old investigation folders, containers that belong to nothing. When unsure, keep it.
- A process or a running container that something may still use stays unless you are sure.

Answer with one line per item, nothing else:
REMOVE <kind> <id> - <short reason>
KEEP <kind> <id> - <short reason>
`

var decisionLine = regexp.MustCompile(`^(REMOVE|KEEP) (path|process|container) (.+?) - (.+)$`)

// askAgent sends the leftovers that name no issue to the agent. Only answers
// about offered items count. Unanswered items stay.
func (l *Loop) askAgent(ctx context.Context, ask []leftover) ([]judgment, error) {
	data, _ := json.MarshalIndent(ask, "", "  ")
	prompt := cleanupRules + "\nRepository: " + l.gh.Slug + " at " + l.Repo + "\n\n# Leftovers\n\n```json\n" + string(data) + "\n```\n"
	if s := strings.TrimSpace(l.get(ctx, "@loop_instr")); s != "" {
		prompt += "\n# Session instructions\n\n" + s + "\n"
	}
	code, out := l.agent(ctx, l.Repo, prompt)
	if l.limited() {
		return nil, nil
	}
	if code != 0 {
		return nil, fmt.Errorf("cleanup agent exited %d: %s", code, tail(out, 10))
	}
	return parseDecisions(out, ask), nil
}

func parseDecisions(out string, ask []leftover) []judgment {
	offered := map[string]leftover{}
	for _, x := range ask {
		offered[x.key()] = x
	}
	var vs []judgment
	for _, line := range strings.Split(out, "\n") {
		m := decisionLine.FindStringSubmatch(strings.TrimSpace(strings.Trim(line, "`")))
		if m == nil {
			continue
		}
		if x, ok := offered[m[2]+" "+m[3]]; ok {
			vs = append(vs, judgment{x, m[1] == "REMOVE", "agent: " + m[4]})
			delete(offered, x.key())
		}
	}
	return vs
}

// sweepState remembers the agent's answers, so an item is asked about once.
type sweepState struct {
	Decisions map[string]judgment `json:"decisions"`
}

func sweepFile(session string) string {
	dir, _ := config.Dir()
	return filepath.Join(dir, "cleanup-"+strings.TrimPrefix(session, "loop-")+".json")
}

// sweepOnce runs one sweep every CleanEvery. In a dry run nothing is
// removed: the report says what would be.
func (l *Loop) sweepOnce(ctx context.Context) (bool, error) {
	if time.Since(l.lastCleanup) < CleanEvery {
		return false, nil
	}
	l.lastCleanup = time.Now()
	dry := l.get(ctx, "@loop_dry") != ""
	// Not "triaging": build loops skip their triage while any loop shows that.
	l.state(ctx, "working", "Looking for leftovers")
	if !dry {
		if err := l.cleanupMerged(ctx); err != nil {
			log.Printf("merged PR cleanup: %v", err)
		}
		l.git(ctx, l.Repo, "worktree", "prune")
	}

	name := strings.ToLower(filepath.Base(l.Repo))
	registered := map[string]bool{}
	if out, err := l.git(ctx, l.Repo, "worktree", "list", "--porcelain"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if p, ok := strings.CutPrefix(line, "worktree "); ok {
				registered[p] = true
			}
		}
	}
	now := time.Now()
	paths := leftoverPaths(l.Repo, name, registered, now)
	// A dev server is found by the folder it runs from, which may be a live
	// worktree whose issue has since closed.
	owners := append([]leftover{}, paths...)
	for p := range registered {
		if n := issueNumber(filepath.Base(p), name); n > 0 && strings.Contains(p, "/.claude/worktrees/") {
			owners = append(owners, leftover{Kind: "path", ID: p, Issue: n})
		}
	}
	var items []leftover
	if ps, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=").Output(); err == nil {
		items = append(items, leftoverProcesses(string(ps), owners, os.Getpid())...)
	}
	dctx, cancel := context.WithTimeout(ctx, time.Minute)
	if out, err := exec.CommandContext(dctx, "docker", "ps", "-a", "--format", "{{.Names}}\t{{.Status}}\t{{.Image}}").Output(); err == nil {
		items = append(items, leftoverContainers(string(out), name)...)
	} else {
		log.Printf("cleanup: docker: %v", err)
	}
	cancel()
	items = append(items, paths...)

	closed, err := l.closedNumbers(ctx)
	if err != nil {
		return true, err
	}
	busy := map[int]bool{}
	if loops, err := l.Tmux.Loops(ctx); err == nil {
		for _, o := range loops {
			if o.Slug == l.gh.Slug && o.Issue > 0 {
				busy[o.Issue] = true
			}
		}
	}
	decided, ask := judge(items, closed, busy)

	var st sweepState
	if b, err := os.ReadFile(sweepFile(l.Session)); err == nil {
		json.Unmarshal(b, &st)
	}
	if st.Decisions == nil {
		st.Decisions = map[string]judgment{}
	}
	var fresh []leftover
	for _, x := range ask {
		if v, ok := st.Decisions[x.key()]; ok {
			v.leftover = x
			decided = append(decided, v)
		} else {
			fresh = append(fresh, x)
		}
	}
	if len(fresh) > 0 {
		answers, err := l.askAgent(ctx, fresh)
		if err != nil {
			log.Printf("cleanup: %v", err)
		}
		for _, v := range answers {
			st.Decisions[v.key()] = v
			decided = append(decided, v)
		}
		// Unanswered items are kept and asked about again next sweep.
		answered := map[string]bool{}
		for _, v := range answers {
			answered[v.key()] = true
		}
		for _, x := range fresh {
			if !answered[x.key()] {
				decided = append(decided, judgment{x, false, "no answer yet"})
			}
		}
	}

	removed, freed := 0, int64(0)
	sort.SliceStable(decided, func(i, j int) bool { return removeOrder(decided[i]) < removeOrder(decided[j]) })
	for i := range decided {
		v := &decided[i]
		if !v.Remove || dry {
			continue
		}
		size := int64(0)
		if v.Kind == "path" {
			size = diskSize(v.ID)
		}
		if err := l.removeLeftover(ctx, v.leftover); err != nil {
			v.Remove, v.Why = false, "could not remove: "+err.Error()
			continue
		}
		delete(st.Decisions, v.key())
		removed++
		freed += size
		logRemoval(*v)
	}
	if !dry {
		purgeTrash(now)
	}
	if b, err := json.MarshalIndent(st, "", "  "); err == nil {
		os.WriteFile(sweepFile(l.Session), b, 0o600)
	}
	l.sweepSummary = l.writeReport(decided, dry, removed, freed)
	return true, nil
}

// Processes go first so a server lets go of its folder before it moves.
func removeOrder(v judgment) int {
	return map[string]int{"process": 0, "container": 1, "path": 2}[v.Kind]
}

// closedNumbers are the closed issues and PRs; a merged PR is closed too.
func (l *Loop) closedNumbers(ctx context.Context) (map[int]bool, error) {
	closed := map[int]bool{}
	for _, kind := range []string{"issue", "pr"} {
		var items []Item
		if err := l.gh.JSON(ctx, &items, kind, "list", "--state", "closed", "--limit", "10000", "--json", "number"); err != nil {
			return nil, err
		}
		for _, it := range items {
			closed[it.Number] = true
		}
	}
	return closed, nil
}

func (l *Loop) removeLeftover(ctx context.Context, x leftover) error {
	switch x.Kind {
	case "process":
		pid, _ := strconv.Atoi(x.ID)
		return syscall.Kill(pid, syscall.SIGTERM)
	case "container":
		dctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return exec.CommandContext(dctx, "docker", "rm", "-f", "-v", x.ID).Run()
	}
	return toTrash(x.ID, time.Now())
}

func trashDir() string { return filepath.Join(sweepHome, ".remux", "cleanup-trash") }

// toTrash moves p into today's holding folder, named after its full path.
func toTrash(p string, now time.Time) error {
	dir := filepath.Join(trashDir(), now.Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dest := filepath.Join(dir, strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", "%"))
	if _, err := os.Lstat(dest); err == nil {
		dest += now.Format(".150405")
	}
	return os.Rename(p, dest)
}

// purgeTrash erases holding folders older than TrashKeep. Agents leave
// folders without read or write permission, so those are opened first.
func purgeTrash(now time.Time) {
	entries, _ := os.ReadDir(trashDir())
	for _, e := range entries {
		day, err := time.ParseInLocation("2006-01-02", e.Name(), now.Location())
		if err != nil || now.Sub(day) < TrashKeep {
			continue
		}
		p := filepath.Join(trashDir(), e.Name())
		filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
			if d != nil && d.IsDir() {
				os.Chmod(q, 0o700)
			}
			return nil
		})
		if err := os.RemoveAll(p); err != nil {
			log.Printf("cleanup: purge %s: %v", p, err)
		}
	}
}

func diskSize(p string) int64 {
	var n int64
	filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func logRemoval(v judgment) {
	dir, err := config.EnsureDir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "cleanup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s removed %s %s (%s)\n", time.Now().Format(time.RFC3339), v.Kind, v.ID, v.Why)
}

// writeReport writes the whole sweep to cleanup-<loop>.txt next to the
// config and returns the one line the loop card shows.
func (l *Loop) writeReport(vs []judgment, dry bool, removed int, freed int64) string {
	var gone, kept []judgment
	for _, v := range vs {
		if v.Remove {
			gone = append(gone, v)
		} else {
			kept = append(kept, v)
		}
	}
	var b strings.Builder
	head := "Removed"
	if dry {
		head = "Dry run: would remove"
	}
	fmt.Fprintf(&b, "Cleanup %s, %s\n\n%s (%d):\n", l.Repo, time.Now().Format("2006-01-02 15:04"), head, len(gone))
	for _, v := range gone {
		fmt.Fprintf(&b, "  %-9s %s  (%s)\n", v.Kind, v.ID, v.Why)
	}
	fmt.Fprintf(&b, "\nKept (%d):\n", len(kept))
	for _, v := range kept {
		fmt.Fprintf(&b, "  %-9s %s  (%s)\n", v.Kind, v.ID, v.Why)
	}
	file := strings.TrimSuffix(sweepFile(l.Session), ".json") + ".txt"
	if err := os.WriteFile(file, []byte(b.String()), 0o600); err != nil {
		log.Printf("cleanup report: %v", err)
	}
	if dry {
		return fmt.Sprintf("Dry run: would remove %d, keep %d. See %s", len(gone), len(kept), filepath.Base(file))
	}
	return fmt.Sprintf("Removed %d (%.1f GB), kept %d", removed, float64(freed)/1e9, len(kept))
}
