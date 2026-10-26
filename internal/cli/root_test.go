package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func findCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, cmd := range parent.Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}
	return nil
}

func TestCommandGroups(t *testing.T) {
	root := (&App{}).root()

	task := findCommand(root, "task")
	if task == nil {
		t.Fatal("missing task command")
	}
	for _, name := range []string{"add", "list", "show", "edit", "move", "done", "next", "comment", "comments", "archive", "rm"} {
		if findCommand(task, name) == nil {
			t.Fatalf("missing task %s command", name)
		}
	}

	auth := findCommand(root, "auth")
	if auth == nil {
		t.Fatal("missing auth command")
	}
	for _, name := range []string{"login", "logout"} {
		if findCommand(auth, name) == nil {
			t.Fatalf("missing auth %s command", name)
		}
	}

	board := findCommand(root, "board")
	if board == nil || findCommand(board, "columns") == nil {
		t.Fatal("missing board columns command")
	}

	for _, name := range []string{"add", "list", "show", "edit", "move", "done", "next", "comment", "comments", "archive", "rm", "columns", "login", "logout"} {
		if findCommand(root, name) != nil {
			t.Fatalf("task or auth command %q is still top-level", name)
		}
	}
}
