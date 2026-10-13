package harness

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomSpawnsEnforceFiveAndExplicitTask(t *testing.T) {
	l, root, _ := idleHarness(t)
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	items := map[string]Item{}
	for i := 1; i <= 7; i++ {
		it := openItem(i, "ready")
		it.Body = "Independent" + string(rune('A'+i)) + ".java"
		items[string(rune('0'+i))] = it
	}
	b, _ := json.Marshal(items)
	os.WriteFile(filepath.Join(root, "items-map"), b, 0o600)
	gh := `#!/usr/bin/python3
import json,sys
from pathlib import Path
p=Path(ROOT)
if sys.argv[1:3]==['issue','view']: print(json.dumps(json.loads((p/'items-map').read_text())[sys.argv[3]]))
else: print('[]')
`
	gh = strings.ReplaceAll(gh, "ROOT", strconvQuote(root))
	os.WriteFile(filepath.Join(root, "gh"), []byte(gh), 0o755)
	// A stand-in tmux server records argv and session metadata, never executes
	// an agent or writes to real tmux. It mirrors optional custom loop fields.
	script := `#!/usr/bin/python3
import json,sys,re
from pathlib import Path
p=Path(ROOT); f=p/'server'; state=json.loads(f.read_text()) if f.exists() else {'sessions':{},'starts':[]}; a=sys.argv[1:]
if a[0]=='list-sessions':
 for name,opts in state['sessions'].items():
  fields=[name,'%1',opts.get('@loop_role',''),'codex',str(p),'o/r','idle','1','0','',opts.get('@loop_scope','backend'),'','add','','','',opts.get('@loop_model',''),opts.get('@loop_custom',''),opts.get('@loop_budget_key',''),opts.get('@loop_reserve','')]
  print('|~|'.join(fields))
elif a[0]=='new-session':
 name=a[a.index('-s')+1]; state['sessions'][name]={};state['starts'].append(a)
elif a[0]=='set-option':
 name=a[a.index('-t')+1].strip('=:');state['sessions'][name][a[-2]]=a[-1]
elif a[0]=='display-message':
 name=a[a.index('-t')+1].strip('=:');print(re.sub(r'#\{([^}]+)\}',lambda m:state['sessions'][name].get(m[1],''),a[-1]))
f.write_text(json.dumps(state))
`
	script = strings.ReplaceAll(script, "ROOT", strconvQuote(root))
	os.WriteFile(filepath.Join(root, "tmux"), []byte(script), 0o755)
	for name, content := range map[string]string{
		"codex": "#!/bin/sh\nexit 0\n",
		"bunx": `#!/bin/sh
echo '[{"provider":"Codex","account":{"id":"active","is_active":true},"metrics":[{"label":"Weekly","remaining_percent":100}]}]'
`,
	} {
		os.WriteFile(filepath.Join(root, name), []byte(content), 0o755)
	}
	var directives []string
	for i := 1; i <= 7; i++ {
		directives = append(directives, "SPAWN build backend "+string(rune('0'+i))+" normal")
	}
	if err := l.spawnWorkers(context.Background(), directives[0]); err != nil {
		t.Fatal(err)
	}
	if err := l.spawnWorkers(context.Background(), directives[0]); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(root, "server"))
	var first struct {
		Starts [][]string `json:"starts"`
	}
	json.Unmarshal(b, &first)
	if len(first.Starts) != 1 {
		t.Fatal("duplicate task spawned before cap was reached")
	}
	if err := l.spawnWorkers(context.Background(), strings.Join(directives, "\n")); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(root, "server"))
	var server struct {
		Starts [][]string `json:"starts"`
	}
	json.Unmarshal(b, &server)
	if len(server.Starts) != 5 {
		t.Fatalf("started %d custom builders, want five", len(server.Starts))
	}
	for _, a := range server.Starts {
		joined := strings.Join(a, " ")
		for _, want := range []string{"--task", "--scope backend", "--agent codex", "--reserve 10", "--budget-key"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("task/budget argument %q missing", want)
			}
		}
	}
	// Repeated directives cannot create an extra worker for the same task.
	if err := l.spawnWorkers(context.Background(), directives[0]); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(root, "server"))
	json.Unmarshal(b, &server)
	if len(server.Starts) != 5 {
		t.Fatal("repeated spawn duplicated a task")
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestCustomWorkersDoNotFallBackToUnassignedQueue(t *testing.T) {
	l, root, assignment := idleHarness(t)
	l.Task = 12
	os.WriteFile(filepath.Join(root, "gh"), []byte("#!/bin/sh\ncase \"$1 $2 $3\" in\n'issue view 13'|'pr view 13') cat '"+filepath.Join(root, "other")+"' ;;\n'issue view 12'|'pr view 12') cat '"+filepath.Join(root, "item")+"' ;;\n*) cat '"+filepath.Join(root, "items")+"' ;;\nesac\n"), 0o755)
	other := openItem(13, "ready", "needs-review")
	b, _ := json.Marshal(other)
	os.WriteFile(filepath.Join(root, "other"), b, 0o600)
	b, _ = json.Marshal([]Item{other})
	os.WriteFile(filepath.Join(root, "items"), b, 0o600)
	os.WriteFile(assignment, []byte("issue:12"), 0o600)
	b, _ = json.Marshal(openItem(12, "ready", "blocked:external"))
	os.WriteFile(filepath.Join(root, "item"), b, 0o600)
	did, err := l.buildOnce(context.Background())
	if err != nil || did {
		t.Fatalf("custom builder fell back to unrelated queue: %v %v", did, err)
	}
	os.WriteFile(assignment, []byte("pr:12"), 0o600)
	b, _ = json.Marshal(openItem(12, "needs-review", "blocked"))
	os.WriteFile(filepath.Join(root, "item"), b, 0o600)
	did, err = l.reviewOnce(context.Background())
	if err != nil || did {
		t.Fatalf("custom reviewer fell back to unrelated PR: %v %v", did, err)
	}
}
