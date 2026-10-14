package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/tmux"
)

func usageFixture(t *testing.T, text string) []providerUsage {
	t.Helper()
	var p []providerUsage
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSpawnUsageActiveAccountAndMissingBudgets(t *testing.T) {
	usage := usageFixture(t, `[{"provider":"Codex","account":{"id":"other","is_active":false},"metrics":[{"label":"Weekly","remaining_percent":100}]},{"provider":"Codex","account":{"id":"current","is_active":true},"metrics":[{"label":"Weekly","remaining_percent":84}]},{"provider":"Claude","metrics":[{"label":"Weekly","remaining_percent":99}]}]`)
	c, ok := chooseSpawn(usage, nil, "review", "normal", nil)
	if !ok || c.Agent != "codex" || c.Remaining != 84 {
		t.Fatalf("wrong active budget: %+v %v", c, ok)
	}
	for _, data := range []string{
		`[]`,
		`[{"provider":"Copilot","metrics":[{"label":"Weekly","remaining_percent":100}]}]`,
		`[{"provider":"Codex","account":{"id":"x","is_active":true},"metrics":[{"label":"Weekly","remaining_percent":null}]}]`,
		`[{"provider":"Codex","account":{"id":"x","is_active":true},"metrics":[{"label":"Weekly","remaining_percent":101}]}]`,
		`[{"provider":"Codex","account":{"id":"x","is_active":true},"metrics":[{"label":"Weekly","remaining_percent":24}]}]`,
		`[{"provider":"Claude","metrics":[{"label":"Session","remaining_percent":100}]}]`,
		`[{"provider":"Claude","metrics":[{"label":"Session","remaining_percent":10},{"label":"Weekly","remaining_percent":99},{"label":"Fable","remaining_percent":100}]}]`,
	} {
		if _, ok := chooseSpawn(usageFixture(t, data), nil, "review", "normal", nil); ok {
			t.Fatalf("unsafe/missing budget accepted: %s", data)
		}
	}
	if _, ok := chooseSpawn(usage, nil, "review", "normal", map[string]bool{c.Key: true}); ok {
		t.Fatal("known native limit ignored")
	}
}

func TestSpawnCodexActualWeeklyFloorAndRoutesDeepWork(t *testing.T) {
	usage := usageFixture(t, `[{"provider":"Codex","account":{"id":"current","is_active":true},"metrics":[{"label":"Weekly","remaining_percent":84}]}]`)
	var loops []tmux.Loop
	for i := 0; i < 5; i++ {
		c, ok := chooseSpawn(usage, loops, "review", "normal", nil)
		if !ok {
			t.Fatalf("bounded task %d refused", i+1)
		}
		loops = append(loops, tmux.Loop{Custom: true, BudgetKey: c.Key, Reserve: c.Reserve})
	}
	if c, ok := chooseSpawn(usage, loops, "review", "deep", nil); !ok || c.Reserve != 0 {
		t.Fatal("Codex should use actual weekly quota without predicted task reservations")
	}
	for _, remaining := range []float64{29, 30, 31} {
		p := usageFixture(t, `[{"provider":"Codex","account":{"id":"current","is_active":true},"metrics":[{"label":"Weekly","remaining_percent":84}]}]`)
		p[0].Metrics[0].Remaining = &remaining
		for _, role := range []string{"build", "review"} {
			for _, class := range []string{"normal", "deep"} {
				_, ok := chooseSpawn(p, loops, role, class, nil)
				if ok != (remaining > 30) {
					t.Fatalf("Codex %s/%s at %.0f%%: eligible=%v", role, class, remaining, ok)
				}
			}
		}
	}
	claude := usageFixture(t, `[{"provider":"Claude","metrics":[{"label":"Session","remaining_percent":90},{"label":"Weekly","remaining_percent":80},{"label":"Opus","remaining_percent":70}]}]`)
	c, ok := chooseSpawn(claude, nil, "review", "deep", nil)
	if !ok || c.Agent != "claude" || c.Model != "opus" || c.Effort != "high" || c.Reserve != 20 || c.Remaining != 70 {
		t.Fatalf("deep route=%+v", c)
	}
	if _, ok := chooseSpawn(usage, []tmux.Loop{{Agent: "codex", State: "blocked", Note: "usage limit, back at 15:04"}}, "review", "normal", nil); ok {
		t.Fatal("manual worker native limit ignored")
	}
}

func TestSpawnLimitPersistsWithoutShorteningReset(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l := &Loop{BudgetKey: strings.Repeat("a", 64), limit: time.Now().Add(time.Hour)}
	l.rememberSpawnLimit()
	if !spawnHolds()[l.BudgetKey] {
		t.Fatal("native limit not held")
	}
	l.limit = time.Now().Add(time.Minute)
	l.rememberSpawnLimit()
	dir, _ := config.Dir()
	b, _ := os.ReadFile(filepath.Join(dir, "spawn-limit-"+l.BudgetKey+".json"))
	var until time.Time
	json.Unmarshal(b, &until)
	if time.Until(until) < 50*time.Minute {
		t.Fatal("shorter limit overwrote later reset")
	}
	os.WriteFile(filepath.Join(dir, "spawn-limit-"+l.BudgetKey+".json"), []byte(`"2000-01-01T00:00:00Z"`), 0o600)
	if spawnHolds()[l.BudgetKey] {
		t.Fatal("expired limit still blocks")
	}
}

func TestCustomCapacityAndOwnerOverlap(t *testing.T) {
	var loops []tmux.Loop
	for i := 0; i < 5; i++ {
		loops = append(loops, tmux.Loop{Slug: "o/r", Role: "build", Custom: true})
	}
	loops = append(loops, tmux.Loop{Slug: "o/r", Role: "build"}, tmux.Loop{Slug: "other/r", Role: "build", Custom: true}, tmux.Loop{Slug: "o/r", Role: "review", Custom: true})
	if customCount(loops, "o/r", "build") != 5 || customCount(loops, "o/r", "review") != 1 {
		t.Fatal("per-role/repo cap wrong")
	}
	if !codeOverlap("backend/NavigationRegistry.java:45", "NavigationRegistry.java:60") || codeOverlap("Receipt.java", "NavigationRegistry.java") {
		t.Fatal("owner overlap detection wrong")
	}
	a := customName(strings.Repeat("long", 20)+"/repo-a", "build", 123)
	b := customName(strings.Repeat("long", 20)+"/repo-b", "build", 123)
	if a == b || !tmux.ValidLoopName(a) || !tmux.ValidLoopName(b) {
		t.Fatalf("custom names collide/invalid: %s %s", a, b)
	}
}

func TestControlExamplesAreNotSpawnRequests(t *testing.T) {
	text := "Example:\n```\nSPAWN build backend 12 normal\n```\nSPAWN review full 13 deep\n"
	lines := strings.Join(controlLines(text), "\n")
	if strings.Contains(lines, "backend 12") || !strings.Contains(lines, "review full 13") {
		t.Fatal("example interpreted as action")
	}
	l := &Loop{Task: 13, Agent: "codex", Effort: "high", set: Settings{ReviewPolicy: "fixed"}}
	choice, err := l.nextReviewChoice(context.Background(), "", "")
	if err != nil || choice.Agent != "codex" || choice.Class != "deep" || choice.Effort != "high" {
		t.Fatalf("deep custom review route wrong: %+v %v", choice, err)
	}
}

func TestClaudeBuildersIgnoreQuotaAndNativeUsageHolds(t *testing.T) {
	for _, data := range []string{
		`[{"provider":"Claude","metrics":[{"label":"Session","remaining_percent":0},{"label":"Weekly","remaining_percent":0}]}]`,
		`[{"provider":"Claude","metrics":[]}]`,
		`[{"provider":"Claude","metrics":[{"label":"Weekly","remaining_percent":null}]}]`,
	} {
		usage := usageFixture(t, data)
		first, ok := chooseSpawn(usage, nil, "build", "normal", nil)
		if !ok {
			t.Fatalf("Claude builder quota-gated: %s", data)
		}
		loops := []tmux.Loop{{Agent: "claude", State: "blocked", Note: "usage limit", Custom: true, BudgetKey: first.Key, Reserve: 100}}
		choice, ok := chooseSpawn(usage, loops, "build", "deep", map[string]bool{first.Key: true})
		if !ok || choice.Model != "opus" || choice.Reserve != 0 {
			t.Fatalf("builder exemption lost: %+v %v", choice, ok)
		}
		if _, ok := chooseSpawn(usage, nil, "review", "normal", nil); ok {
			t.Fatal("Claude reviewer unexpectedly exempted")
		}
	}
}
