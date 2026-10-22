package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

func newTestBackend(t *testing.T) *Backend {
	t.Helper()
	b, err := New(Config{Path: filepath.Join(t.TempDir(), "tasks.db"), Me: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestCreateListAndGet(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	task, err := b.CreateTask(ctx, &pb.CreateTaskRequest{
		Title: "  Fix redirect  ", Description: "details",
		Priority: pb.Priority_PRIORITY_HIGH, Labels: []string{"bug", "auth"},
		Assignee: "@me",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Key != "T-1" || task.Title != "Fix redirect" {
		t.Fatalf("unexpected task: %+v", task)
	}
	if task.Status != "todo" {
		t.Fatalf("expected default ready status todo, got %q", task.Status)
	}
	if task.Assignee != "tester" {
		t.Fatalf("expected @me resolution, got %q", task.Assignee)
	}
	if len(task.Labels) != 2 {
		t.Fatalf("expected 2 labels, got %v", task.Labels)
	}
	got, err := b.GetTask(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Id != task.Id {
		t.Fatalf("id mismatch: %s vs %s", got.Id, task.Id)
	}
}

func TestMoveOrderingAndArchive(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	a, _ := b.CreateTask(ctx, &pb.CreateTaskRequest{Title: "A"})
	c, _ := b.CreateTask(ctx, &pb.CreateTaskRequest{Title: "C"})
	bb, _ := b.CreateTask(ctx, &pb.CreateTaskRequest{Title: "B"})

	if _, err := b.MoveTask(ctx, &pb.MoveTaskRequest{Id: bb.Id, Status: "todo", BeforeId: &c.Id}); err != nil {
		t.Fatal(err)
	}
	tasks, err := b.ListTasks(ctx, &pb.TaskFilter{Statuses: []string{"todo"}})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{tasks[0].Title, tasks[1].Title, tasks[2].Title}
	want := []string{"A", "B", "C"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}

	if _, err := b.MoveTask(ctx, &pb.MoveTaskRequest{Id: a.Id, Status: "in_progress"}); err != nil {
		t.Fatal(err)
	}
	moved, _ := b.GetTask(ctx, a.Id)
	if moved.Status != "in_progress" {
		t.Fatalf("status = %q", moved.Status)
	}

	if err := b.DeleteTask(ctx, a.Id, false); err != nil {
		t.Fatal(err)
	}
	visible, _ := b.ListTasks(ctx, &pb.TaskFilter{})
	if len(visible) != 2 {
		t.Fatalf("archived task should be hidden, got %d", len(visible))
	}
	all, _ := b.ListTasks(ctx, &pb.TaskFilter{IncludeArchived: true})
	if len(all) != 3 {
		t.Fatalf("expected archived task in --all, got %d", len(all))
	}
	if err := b.DeleteTask(ctx, bb.Id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.GetTask(ctx, bb.Id); err == nil {
		t.Fatal("expected hard-deleted task to be gone")
	}
}

func TestUpdateCommentsAndFilters(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	task, _ := b.CreateTask(ctx, &pb.CreateTaskRequest{
		Title: "Write docs", Priority: pb.Priority_PRIORITY_LOW, Labels: []string{"docs"},
	})
	title := "Write user docs"
	desc := "cover the CLI"
	pr := pb.Priority_PRIORITY_URGENT
	updated, err := b.UpdateTask(ctx, &pb.UpdateTaskRequest{
		Id: task.Id, Title: &title, Description: &desc, Priority: &pr,
		SetLabels: true, Labels: []string{"docs", "writing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || updated.Priority != pr || len(updated.Labels) != 2 {
		t.Fatalf("unexpected update: %+v", updated)
	}
	if _, err := b.AddComment(ctx, task.Id, "looks good"); err != nil {
		t.Fatal(err)
	}
	comments, err := b.ListComments(ctx, task.Id)
	if err != nil || len(comments) != 1 {
		t.Fatalf("comments: %v %v", comments, err)
	}
	found, err := b.ListTasks(ctx, &pb.TaskFilter{Search: "CLI"})
	if err != nil || len(found) != 1 {
		t.Fatalf("search: %v %v", found, err)
	}
	none, err := b.ListTasks(ctx, &pb.TaskFilter{Labels: []string{"missing"}})
	if err != nil || len(none) != 0 {
		t.Fatalf("label filter: %v %v", none, err)
	}
}
