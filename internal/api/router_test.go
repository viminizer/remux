package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/tmux"
)

// Tests for the REST layer.
//
// The layers either side of it were already covered - internal/tmux has a real
// suite and internal/agent has fixtures - but every handler here was
// unexercised, which is how #23's refusal could be deleted without anything
// failing. The rules that matter live at this layer: the key allowlist, pane
// id validation, and what a bad request is answered with.
//
// Everything that writes goes through scratchPane, in ws_test.go, which
// refuses any pane outside remux-test before a key is sent and kills its
// window afterwards.

// paneURL builds the path for a pane id.
//
// A pane id starts with %, which is the escape character in a URL path: "%187"
// parses as the escape "%18" followed by "7". The client encodes it, and
// paneID on the server puts it back, so a test that skips this step is not
// exercising the same path the phone uses - it fails before reaching the
// handler at all.
func paneURL(pane, suffix string) string {
	return "/api/panes/" + url.PathEscape(pane) + suffix
}

// do issues a request against the test server and returns the status and body.
func do(t *testing.T, ts *httptest.Server, method, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		switch v := body.(type) {
		case string: // a raw string is sent as-is, for malformed-body cases
			r = strings.NewReader(v)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			r = bytes.NewReader(b)
		}
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// screen captures a pane through the API, which is also what the phone reads.
func screen(t *testing.T, ts *httptest.Server, pane string) string {
	t.Helper()
	code, body := do(t, ts, "GET", paneURL(pane, "/capture"), nil)
	if code != http.StatusOK {
		t.Fatalf("capture %s: status %d, body %s", pane, code, body)
	}
	var out struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("capture json: %v", err)
	}
	return strings.Join(out.Lines, "\n")
}

// waitFor polls the pane until want shows up. tmux is not synchronous with the
// HTTP response: send-keys returns once tmux has the input, not once the shell
// has drawn the result.
func waitFor(t *testing.T, ts *httptest.Server, pane, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = screen(t, ts, pane)
		if strings.Contains(last, want) {
			return last
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("waiting for %q in pane %s; last screen:\n%s", want, pane, last)
	return ""
}

// TestKeysAllowlist is the reason this file exists.
//
// NormalizeKey is tested in internal/tmux, but nothing tested that the handler
// calls it. Deleting that call would have passed the whole suite while opening
// send-keys to any key name tmux understands, including prefix chords bound to
// destructive commands. This fails if the call goes.
func TestKeysAllowlist(t *testing.T) {
	tm, pane := scratchPane(t)
	ts := testServer(t, tm)

	for _, key := range []string{
		"C-z",          // a real tmux key, deliberately not on the list
		"C-b",          // the prefix: the dangerous one
		"kill-session", // not a key at all
		"Enter Enter",  // smuggling a second key in one string
		"",             // empty
	} {
		code, body := do(t, ts, "POST", paneURL(pane, "/keys"),
			map[string]any{"keys": []string{key}})
		if code != http.StatusBadRequest {
			t.Errorf("key %q: status %d (want 400), body %s", key, code, body)
		}
	}

	// And an allowed key still reaches the pane.
	code, body := do(t, ts, "POST", paneURL(pane, "/text"),
		map[string]any{"text": "echo allowlist-ok", "submit": false})
	if code != http.StatusOK {
		t.Fatalf("text: status %d, body %s", code, body)
	}
	if code, body = do(t, ts, "POST", paneURL(pane, "/keys"),
		map[string]any{"keys": []string{"Enter"}}); code != http.StatusOK {
		t.Fatalf("Enter: status %d, body %s", code, body)
	}
	waitFor(t, ts, pane, "allowlist-ok")
}

// TestPaneIDValidation checks that a malformed id is refused before any tmux
// command is built from it. Every route that takes an id shares one helper, so
// this is really a test that no route skipped it.
func TestPaneIDValidation(t *testing.T) {
	tm, _ := scratchPane(t)
	ts := testServer(t, tm)

	bad := []string{
		"notapane",
		"%",
		"%abc",
		"%1x",
		"%1 kill-server",
	}
	routes := []struct{ method, suffix string }{
		{"GET", "/capture"},
		{"POST", "/text"},
		{"POST", "/keys"},
		{"POST", "/interrupt"},
		{"DELETE", ""},
	}
	for _, id := range bad {
		for _, rt := range routes {
			var body any
			if rt.method == "POST" {
				body = map[string]any{"text": "x", "keys": []string{"Enter"}}
			}
			code, out := do(t, ts, rt.method, paneURL(id, rt.suffix), body)
			if code != http.StatusBadRequest {
				t.Errorf("%s /api/panes/%s%s: status %d (want 400), body %s",
					rt.method, id, rt.suffix, code, out)
			}
		}
	}
}

// TestTextSubmitSemantics covers the distinction the whole composer rests on:
// text with submit=false is staged, not run, and a separate Enter runs it.
//
// Asserted here rather than only at the tmux layer because it is the API
// contract the phone codes against - Settings has a "Submit with Enter" switch
// that turns exactly this pair into two different flows.
func TestTextSubmitSemantics(t *testing.T) {
	tm, pane := scratchPane(t)
	ts := testServer(t, tm)

	const marker = "staged-not-run"
	code, body := do(t, ts, "POST", paneURL(pane, "/text"),
		map[string]any{"text": "echo " + marker, "submit": false})
	if code != http.StatusOK {
		t.Fatalf("text: status %d, body %s", code, body)
	}

	// The command is on the input line, so the text is on screen - but its
	// output is not, because nothing submitted it.
	waitFor(t, ts, pane, "echo "+marker)
	time.Sleep(400 * time.Millisecond)
	before := screen(t, ts, pane)
	if strings.Count(before, marker) != 1 {
		t.Fatalf("submit=false looks like it ran: %q appears %d times, want 1 (the input line):\n%s",
			marker, strings.Count(before, marker), before)
	}

	if code, body = do(t, ts, "POST", paneURL(pane, "/keys"),
		map[string]any{"keys": []string{"Enter"}}); code != http.StatusOK {
		t.Fatalf("Enter: status %d, body %s", code, body)
	}

	// Now it has run: the marker appears twice, once echoed on the input line
	// and once as output.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(screen(t, ts, pane), marker) >= 2 {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("Enter did not submit the staged line; screen:\n%s", screen(t, ts, pane))
}

// TestMultiLineTextIsNotSubmitted is the case bracketed paste exists for: a
// three-line prompt must arrive as one staged block, not as three commands.
func TestMultiLineTextIsNotSubmitted(t *testing.T) {
	tm, pane := scratchPane(t)
	ts := testServer(t, tm)

	code, body := do(t, ts, "POST", paneURL(pane, "/text"),
		map[string]any{"text": "echo one\necho two\necho three", "submit": false})
	if code != http.StatusOK {
		t.Fatalf("text: status %d, body %s", code, body)
	}
	time.Sleep(900 * time.Millisecond)

	// If any line had been submitted, its output would be on screen. Under
	// bracketed paste none of them run, so the words appear only where they
	// were typed.
	got := screen(t, ts, pane)
	for _, word := range []string{"one", "two", "three"} {
		if strings.Count(got, word) > 1 {
			t.Fatalf("%q appears %d times - a line was submitted:\n%s",
				word, strings.Count(got, word), got)
		}
	}

	// Clear the staged text so the pane is left as it was found.
	do(t, ts, "POST", paneURL(pane, "/keys"), map[string]any{"keys": []string{"C-u"}})
}

// TestMalformedBody covers the other half of the 400 contract: a route that
// takes JSON must reject a body it cannot read, rather than acting on a
// zero-valued struct.
func TestMalformedBody(t *testing.T) {
	tm, pane := scratchPane(t)
	ts := testServer(t, tm)

	for _, body := range []string{
		"{",
		"not json at all",
		`{"text": }`,
	} {
		code, out := do(t, ts, "POST", paneURL(pane, "/text"), body)
		if code != http.StatusBadRequest {
			t.Errorf("text body %q: status %d (want 400), body %s", body, code, out)
		}
		code, out = do(t, ts, "POST", paneURL(pane, "/keys"), body)
		if code != http.StatusBadRequest {
			t.Errorf("keys body %q: status %d (want 400), body %s", body, code, out)
		}
	}
}

// TestCaptureGonePane checks the 404 the UI has a whole screen for. A pane
// that has been killed is not a server error, and reporting it as one would
// put the phone on the wrong screen.
func TestCaptureGonePane(t *testing.T) {
	tm, pane := scratchPane(t)
	ts := testServer(t, tm)

	if code, body := do(t, ts, "GET", paneURL(pane, "/capture"), nil); code != http.StatusOK {
		t.Fatalf("capture before kill: status %d, body %s", code, body)
	}
	if code, body := do(t, ts, "DELETE", paneURL(pane, ""), nil); code != http.StatusOK {
		t.Fatalf("kill: status %d, body %s", code, body)
	}
	// tmux frees the id asynchronously.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if code, _ := do(t, ts, "GET", paneURL(pane, "/capture"), nil); code == http.StatusNotFound {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	code, body := do(t, ts, "GET", paneURL(pane, "/capture"), nil)
	t.Fatalf("capture after kill: status %d (want 404), body %s", code, body)
}

// TestSettingsRoundTrip drives the notification toggles, which are the one
// setting that has to live on the server because the push watcher reads them.
//
// HOME is redirected first: config.Save writes to ~/.config/remux, and a test
// that overwrote the real config.json would silently change which
// notifications this machine sends.
func TestSettingsRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("config dir is %s, outside the temp home %s - refusing to write", dir, home)
	}

	tm := tmux.New()
	ts := testServer(t, tm)

	for _, want := range []notifySettings{
		{NotifyWaiting: false, NotifyDone: true},
		{NotifyWaiting: true, NotifyDone: false},
	} {
		code, body := do(t, ts, "PUT", "/api/settings", want)
		if code != http.StatusOK {
			t.Fatalf("put: status %d, body %s", code, body)
		}
		code, body = do(t, ts, "GET", "/api/settings", nil)
		if code != http.StatusOK {
			t.Fatalf("get: status %d, body %s", code, body)
		}
		var got notifySettings
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("get json: %v", err)
		}
		if got != want {
			t.Errorf("round trip: got %+v, want %+v", got, want)
		}
	}

	// A bad body is refused and changes nothing.
	before, _ := do(t, ts, "GET", "/api/settings", nil)
	_ = before
	code, body := do(t, ts, "PUT", "/api/settings", "{ nope")
	if code != http.StatusBadRequest {
		t.Fatalf("bad body: status %d (want 400), body %s", code, body)
	}
	code, after := do(t, ts, "GET", "/api/settings", nil)
	if code != http.StatusOK {
		t.Fatalf("get after bad put: status %d", code)
	}
	var got notifySettings
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatal(err)
	}
	if want := (notifySettings{NotifyWaiting: true, NotifyDone: false}); got != want {
		t.Errorf("a rejected put changed the settings: got %+v, want %+v", got, want)
	}
}

// TestAuditLog checks that mutating calls are recorded. The audit log is the
// only record of what the phone did to the laptop, so a route that quietly
// stopped writing to it would be invisible until it mattered.
func TestAuditLog(t *testing.T) {
	tm, pane := scratchPane(t)
	ts := testServer(t, tm)

	path := filepath.Join(t.TempDir(), "audit.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })

	// testServer builds the server without an audit log, which is why the
	// existing WebSocket tests never noticed one way or the other.
	srv := NewServer(config.Default(), tm, nil)
	srv.Audit = &AuditLog{f: f}
	local := httptest.NewServer(srv.Handler())
	t.Cleanup(local.Close)

	do(t, local, "POST", paneURL(pane, "/text"),
		map[string]any{"text": "audited", "submit": false})
	do(t, local, "POST", paneURL(pane, "/keys"),
		map[string]any{"keys": []string{"C-u"}})
	do(t, local, "POST", paneURL(pane, "/interrupt"), nil)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"text", "keys", "interrupt", pane, "audited"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("audit log has no %q; log is:\n%s", want, got)
		}
	}
	if lines := strings.Count(strings.TrimSpace(string(got)), "\n") + 1; lines != 3 {
		t.Errorf("audit log has %d lines, want 3:\n%s", lines, got)
	}
	_ = ts
}
