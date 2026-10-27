package api

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/viminizer/remux/internal/config"
	"github.com/viminizer/remux/internal/harness"
)

// Text-only alias normalization: never probe protected folders during polling.
func projectPath(path string) string {
	for _, suffix := range []string{"/.claude/worktrees/", "/.worktrees/"} {
		if root, _, ok := strings.Cut(path, suffix); ok {
			return filepath.Clean(root)
		}
	}
	return filepath.Clean(path)
}

type instructionPreview struct {
	Text   string `json:"text"`
	Source string `json:"source"`
}
type loopProject struct {
	Repo     string                        `json:"repo"`
	Quality  bool                          `json:"quality"`
	Defaults string                        `json:"defaults"`
	Roles    map[string]instructionPreview `json:"roles"`
}

// A macOS permission read can stall in the kernel. Bound both request time and
// outstanding readers, so a selected-project lookup cannot wedge background GETs.
var projectReaders = make(chan struct{}, 2)

func (s *Server) handleLoopProject(w http.ResponseWriter, r *http.Request) {
	repo := filepath.Clean(expandHome(r.URL.Query().Get("repo")))
	scope := r.URL.Query().Get("scope")
	if !filepath.IsAbs(repo) || !scopeRe.MatchString(scope) {
		writeErr(w, http.StatusBadRequest, "a full project path and valid scope are required")
		return
	}
	select {
	case projectReaders <- struct{}{}:
	default:
		writeErr(w, http.StatusServiceUnavailable, "project files are still being read; try again shortly")
		return
	}
	type result struct {
		value loopProject
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-projectReaders }()
		value, err := readLoopProject(repo, scope)
		done <- result{value, err}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		writeErr(w, http.StatusGatewayTimeout, "project files could not be read in time; check folder access and retry")
	case got := <-done:
		if got.err != nil {
			writeErr(w, http.StatusInternalServerError, got.err.Error())
			return
		}
		writeJSON(w, http.StatusOK, got.value)
	}
}

func readLoopProject(repo, scope string) (loopProject, error) {
	out := loopProject{Repo: repo, Roles: map[string]instructionPreview{}}
	// Distinguish inaccessible or nonexistent projects from unconfigured workflows.
	if stat, err := os.Stat(repo); err != nil {
		return out, err
	} else if !stat.IsDir() {
		return out, errors.New("project is not a directory")
	}
	settings, err := harness.LoadSettings(repo)
	if err != nil {
		return out, err
	}
	out.Defaults = settings.Instructions
	read := func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(repo, name))
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return string(bytes.TrimSpace(b)), err
	}
	quality, err := read(harness.InstructionFile("code-quality", "build"))
	if err != nil {
		return out, err
	}
	out.Quality = quality != ""
	for _, role := range []string{"build", "review", "supervise"} {
		name := harness.InstructionFile(scope, role)
		text, err := read(name)
		if err != nil {
			return out, err
		}
		if text == "" && scope != "code-quality" {
			name = harness.InstructionFile("", role)
			text, err = read(name)
			if err != nil {
				return out, err
			}
		}
		if text != "" {
			out.Roles[role] = instructionPreview{Text: text, Source: name}
		}
	}
	return out, nil
}

// Explicit form-open cleanup only. Missing historical paths are omitted from
// this response, never deleted from config; inaccessible paths stay selectable.
func (s *Server) handleLoopProjects(w http.ResponseWriter, r *http.Request) {
	choices := s.repoChoices()
	select {
	case projectReaders <- struct{}{}:
	default:
		writeErr(w, http.StatusServiceUnavailable, "project files are still being read")
		return
	}
	done := make(chan []config.HarnessRepo, 1)
	go func() {
		defer func() { <-projectReaders }()
		out := make([]config.HarnessRepo, 0, len(choices))
		for _, choice := range choices {
			if _, err := os.Stat(choice.Path); !errors.Is(err, fs.ErrNotExist) {
				out = append(out, choice)
			}
		}
		done <- out
	}()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		writeErr(w, http.StatusGatewayTimeout, "project choices could not be verified in time")
	case repos := <-done:
		writeJSON(w, http.StatusOK, repos)
	}
}
