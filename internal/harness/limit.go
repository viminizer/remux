package harness

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// An agent out of usage is not stuck on its work. Measured on educenter: a
// Claude session limit at 12:50 failed two build runs in seconds, the loop
// marked both issues stuck, the supervisor hit the same limit and passed both
// to Kevin - who could only answer "try again". So a run that ends on a limit
// message is not judged at all: the item goes back as it was and the loop
// waits for the reset.

var limitRe = regexp.MustCompile(`(?i)(hit your (session|usage|weekly|daily) limit|usage limit reached|rate limit reached|quota exceeded)`)

// "resets 1:50pm", "resets 3am" - Claude.
var resetsRe = regexp.MustCompile(`(?i)resets\s+(\d{1,2})(?::(\d{2}))?\s*([ap]m)`)

// "try again at 3:53 AM" - Codex.
var againAtRe = regexp.MustCompile(`(?i)try again at[^0-9]*?(\d{1,2}):(\d{2})\s*([ap]m)`)

// "try again in 1 day 2 hours 5 minutes" - Codex.
var againInRe = regexp.MustCompile(`(?i)try again in ((?:\d+\s*(?:days?|hours?|minutes?|mins?)[\s,and]*)+)`)
var partRe = regexp.MustCompile(`(?i)(\d+)\s*(day|hour|min)`)

// LimitWait is how long to wait when a limit message names no time.
var LimitWait = 30 * time.Minute

// usageLimit reports whether an agent's output ends on a usage limit, and
// when to try again.
func usageLimit(out string, now time.Time) (time.Time, bool) {
	if !limitRe.MatchString(out) {
		return time.Time{}, false
	}
	if m := resetsRe.FindStringSubmatch(out); m != nil {
		return clock(m[1], m[2], m[3], now), true
	}
	if m := againAtRe.FindStringSubmatch(out); m != nil {
		return clock(m[1], m[2], m[3], now), true
	}
	if m := againInRe.FindStringSubmatch(out); m != nil {
		var d time.Duration
		for _, p := range partRe.FindAllStringSubmatch(m[1], -1) {
			n, _ := strconv.Atoi(p[1])
			switch strings.ToLower(p[2]) {
			case "day":
				d += time.Duration(n) * 24 * time.Hour
			case "hour":
				d += time.Duration(n) * time.Hour
			default:
				d += time.Duration(n) * time.Minute
			}
		}
		if d > 0 {
			return now.Add(d), true
		}
	}
	return now.Add(LimitWait), true
}

// clock is the next time today, or tomorrow, that reads h:mm am/pm.
func clock(h, m, ampm string, now time.Time) time.Time {
	hour, _ := strconv.Atoi(h)
	min, _ := strconv.Atoi(m)
	hour %= 12
	if strings.EqualFold(ampm, "pm") {
		hour += 12
	}
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t
}
