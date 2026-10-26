package cli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseGitHubRepo(t *testing.T) {
	cases := []struct {
		in, owner, repo string
	}{
		{"git@github.com:metruzanca/tasks.git", "metruzanca", "tasks"},
		{"https://github.com/metruzanca/tasks.git", "metruzanca", "tasks"},
		{"https://github.com/metruzanca/tasks", "metruzanca", "tasks"},
		{"ssh://git@github.com/metruzanca/tasks.git", "metruzanca", "tasks"},
		{"git://github.com/metruzanca/tasks.git", "metruzanca", "tasks"},
		{"git@gitlab.com:metruzanca/tasks.git", "", ""},
	}
	for _, c := range cases {
		owner, repo := parseGitHubRepo(c.in)
		if owner != c.owner || repo != c.repo {
			t.Fatalf("parseGitHubRepo(%q) = %q, %q; want %q, %q", c.in, owner, repo, c.owner, c.repo)
		}
	}
}

func TestNormalizeRepo(t *testing.T) {
	if got := normalizeRepo("metruzanca", "tasks"); got != "metruzanca/tasks" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeRepo("metruzanca", "other/tasks"); got != "other/tasks" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeRepo("", "tasks"); got != "tasks" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateInitValues(t *testing.T) {
	v := initValues{Backend: "github", Write: "repo", Owner: "metruzanca", Repo: "tasks", Project: 10}
	if err := validateInitValues(&v); err != nil {
		t.Fatal(err)
	}
	if v.Repo != "metruzanca/tasks" {
		t.Fatalf("repo = %q", v.Repo)
	}
	if err := validateInitValues(&initValues{Backend: "linear", Write: "global"}); err == nil {
		t.Fatal("expected missing team error")
	}
	if err := validateInitValues(&initValues{Backend: "sqlite", Write: "nope"}); err == nil {
		t.Fatal("expected invalid write target error")
	}
}

func TestDefaultProjectTitle(t *testing.T) {
	cases := map[string]string{
		"metruzanca/tasks":     "Tasks",
		"metruzanca/tasks.git": "Tasks",
		"tasks":                "Tasks",
		"":                     "Tasks",
		"acme/board-service":   "Board-service",
	}
	for repo, want := range cases {
		if got := defaultProjectTitle(repo); got != want {
			t.Fatalf("defaultProjectTitle(%q) = %q, want %q", repo, got, want)
		}
	}
}

func TestCreateGitHubProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *"project create"*"--owner octo"*"--title Tasks"*"--format json"*) ;;
  *) echo "unexpected args: $*" >&2; exit 1 ;;
esac
echo '{"number":42}'
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	n, err := createGitHubProject(context.Background(), "octo", "Tasks", "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Fatalf("number = %d", n)
	}
}

func TestLinkGitHubProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  "project link 42 --owner octo --repo octo/tasks") ;;
  *) echo "unexpected args: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := linkGitHubProject(context.Background(), "octo", 42, "octo/tasks", ""); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureGitHubProjectLinkedOwnerMismatch(t *testing.T) {
	err := ensureGitHubProjectLinked(context.Background(), "octo", 42, "other/tasks", "")
	if err == nil {
		t.Fatal("expected owner mismatch error")
	}
}

func TestEnsureGitHubProjectLinkedAlreadyLinked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "repo view") echo '{"projectsV2":{"Nodes":[{"number":42}]}}' ;;
  "project link") echo "should not link" >&2; exit 1 ;;
  *) echo "unexpected args: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := ensureGitHubProjectLinked(context.Background(), "octo", 42, "octo/tasks", ""); err != nil {
		t.Fatal(err)
	}
}
