package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

type githubBudget struct{ Graph, REST time.Time }

var githubBudgets = struct {
	sync.Mutex
	ByRepo map[string]githubBudget
}{ByRepo: map[string]githubBudget{}}

type githubWait struct {
	Until    time.Time
	Resource string
}

func (e *githubWait) Error() string {
	return fmt.Sprintf("GitHub %s rate limited; retry after %s", e.Resource, e.Until.Local().Format("15:04:05"))
}
func githubLimited(err error) bool { var e *githubWait; return errors.As(err, &e) }
func rateError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "rate limit") || strings.Contains(s, "abuse detection") || strings.Contains(s, "too many requests")
}
func (g GH) budget() githubBudget {
	githubBudgets.Lock()
	defer githubBudgets.Unlock()
	return githubBudgets.ByRepo[ghBin+"\x00"+g.Dir]
}
func (g GH) setBudget(resource string, until time.Time) {
	githubBudgets.Lock()
	defer githubBudgets.Unlock()
	k := ghBin + "\x00" + g.Dir
	b := githubBudgets.ByRepo[k]
	if resource == "GraphQL" {
		b.Graph = until
	} else {
		b.REST = until
	}
	githubBudgets.ByRepo[k] = b
}

// gh api --include exposes authoritative headers on both successful and
// failed responses. Test doubles may return a bare JSON body instead.
func apiResponse(out []byte) ([]byte, map[string]string) {
	s := strings.ReplaceAll(string(out), "\r\n", "\n")
	h := map[string]string{}
	for strings.HasPrefix(s, "HTTP/") {
		i := strings.Index(s, "\n\n")
		if i < 0 {
			break
		}
		for _, line := range strings.Split(s[:i], "\n")[1:] {
			k, v, ok := strings.Cut(line, ":")
			if ok {
				h[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
			}
		}
		s = s[i+2:]
	}
	return []byte(s), h
}
func retryAt(h map[string]string, now time.Time) time.Time {
	if sec, err := strconv.Atoi(h["retry-after"]); err == nil && sec > 0 {
		return now.Add(time.Duration(sec)*time.Second + time.Second)
	}
	if h["x-ratelimit-remaining"] == "0" {
		if unix, err := strconv.ParseInt(h["x-ratelimit-reset"], 10, 64); err == nil && time.Unix(unix, 0).After(now) {
			return time.Unix(unix, 0).Add(time.Second)
		}
	}
	return now.Add(time.Minute)
}
func (g GH) graphReset(ctx context.Context) time.Time {
	out, err := g.raw(ctx, "api", "graphql", "--include", "-f", "query=query { rateLimit { remaining resetAt } }")
	body, h := apiResponse(out)
	if h["x-ratelimit-remaining"] == "0" {
		return retryAt(h, time.Now())
	}
	if err == nil {
		var p struct {
			Data struct {
				RateLimit struct {
					Remaining int
					ResetAt   time.Time
				}
			}
		}
		if json.Unmarshal(body, &p) == nil && p.Data.RateLimit.Remaining == 0 && p.Data.RateLimit.ResetAt.After(time.Now()) {
			return p.Data.RateLimit.ResetAt.Add(time.Second)
		}
	}
	return retryAt(h, time.Now())
}
func (g GH) api(ctx context.Context, args ...string) ([]byte, error) {
	if until := g.budget().REST; until.After(time.Now()) {
		return nil, &githubWait{until, "REST"}
	}
	argv := append([]string{"api"}, args...)
	argv = append(argv, "--include")
	out, err := g.raw(ctx, argv...)
	body, h := apiResponse(out)
	if rateError(err) || err != nil && h["x-ratelimit-remaining"] == "0" {
		until := retryAt(h, time.Now())
		g.setBudget("REST", until)
		return nil, &githubWait{until, "REST"}
	}
	return body, err
}
func (g GH) withFallback(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return g.raw(ctx, args...)
	}
	if args[0] == "api" && (len(args) < 2 || args[1] != "graphql") {
		return g.api(ctx, args[1:]...)
	}
	if until := g.budget().Graph; until.After(time.Now()) {
		out, ok, err := g.restFallback(ctx, args)
		if ok {
			return out, err
		}
		return nil, &githubWait{until, "GraphQL"}
	}
	out, err := g.raw(ctx, args...)
	if !rateError(err) {
		return out, err
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "secondary rate limit") || strings.Contains(message, "abuse detection") || strings.Contains(message, "too many requests") {
		until := time.Now().Add(time.Minute)
		g.setBudget("GraphQL", until)
		g.setBudget("REST", until)
		return nil, &githubWait{until, "secondary"}
	}
	if !strings.Contains(message, "graphql") {
		until := time.Now().Add(time.Minute)
		g.setBudget("REST", until)
		return nil, &githubWait{until, "REST"}
	}
	until := g.graphReset(ctx)
	g.setBudget("GraphQL", until)
	log.Printf("GitHub GraphQL limited; using REST until %s", until.Local().Format("15:04:05"))
	if out, ok, e := g.restFallback(ctx, args); ok {
		return out, e
	}
	return nil, &githubWait{until, "GraphQL"}
}
