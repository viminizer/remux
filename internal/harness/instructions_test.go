package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func writeInstruction(t *testing.T, repo, name, body string) {
	t.Helper()
	p := filepath.Join(repo, InstructionDir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstructionFile(t *testing.T) {
	for _, c := range []struct{ scope, role, want string }{
		{"frontend", "build", ".remux/instructions/frontend-build.md"},
		{"backend", "build", ".remux/instructions/backend-build.md"},
		// A full-scope loop is not scoped, so it takes the role's own file.
		{"full", "build", ".remux/instructions/build.md"},
		{"", "review", ".remux/instructions/review.md"},
	} {
		if got := InstructionFile(c.scope, c.role); got != c.want {
			t.Errorf("InstructionFile(%q, %q) = %q, want %q", c.scope, c.role, got, c.want)
		}
	}
}

func TestInstructionsPrefersTheScopedFile(t *testing.T) {
	repo := t.TempDir()
	writeInstruction(t, repo, "frontend-build.md", "frontend builder brief")
	writeInstruction(t, repo, "build.md", "any builder brief")

	text, name := Instructions(repo, "frontend", "build")
	if text != "frontend builder brief" {
		t.Errorf("text = %q, want the scoped file's", text)
	}
	if name != ".remux/instructions/frontend-build.md" {
		t.Errorf("name = %q", name)
	}
}

func TestInstructionsFallsBackToTheRoleFile(t *testing.T) {
	repo := t.TempDir()
	// A reviewer reviews every scope, so one file serves them all.
	writeInstruction(t, repo, "review.md", "reviewer brief")

	text, name := Instructions(repo, "frontend", "review")
	if text != "reviewer brief" || name != ".remux/instructions/review.md" {
		t.Errorf("Instructions = %q from %q, want the role file", text, name)
	}
}

func TestInstructionsEmptyWhenTheRepoKeepsNone(t *testing.T) {
	repo := t.TempDir()
	if text, name := Instructions(repo, "frontend", "build"); text != "" || name != "" {
		t.Errorf("Instructions = %q from %q, want empty so the caller's own text stands", text, name)
	}
}

func TestInstructionsIgnoresABlankFile(t *testing.T) {
	repo := t.TempDir()
	// A file someone emptied must not silence the role file or the form.
	writeInstruction(t, repo, "frontend-build.md", "   \n\t\n")
	writeInstruction(t, repo, "build.md", "any builder brief")

	text, name := Instructions(repo, "frontend", "build")
	if text != "any builder brief" || name != ".remux/instructions/build.md" {
		t.Errorf("Instructions = %q from %q, want the role file", text, name)
	}
}

func TestInstructionsCannotEscapeTheRepo(t *testing.T) {
	repo := t.TempDir()
	secret := filepath.Join(filepath.Dir(repo), "escaped.md")
	if err := os.WriteFile(secret, []byte("should never be read"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The caller validates scope and role, but prove the join stays inside
	// even if a traversal ever reaches here.
	for _, scope := range []string{"../..", "../../..", ".."} {
		if text, _ := Instructions(repo, scope, "build"); text != "" {
			t.Errorf("Instructions(scope=%q) read %q from outside the repo", scope, text)
		}
	}
}
