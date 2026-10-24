package linear

import (
	"strings"
	"testing"

	"github.com/metruzanca/tasks/pkg/sdk/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPriorityMapping(t *testing.T) {
	cases := []struct {
		linear int
		want   pb.Priority
	}{
		{0, pb.Priority_PRIORITY_NONE},
		{1, pb.Priority_PRIORITY_URGENT},
		{2, pb.Priority_PRIORITY_HIGH},
		{3, pb.Priority_PRIORITY_MEDIUM},
		{4, pb.Priority_PRIORITY_LOW},
	}
	for _, c := range cases {
		if got := priorityFromLinear(c.linear); got != c.want {
			t.Fatalf("priorityFromLinear(%d) = %v, want %v", c.linear, got, c.want)
		}
		if got := priorityToLinear(c.want); got != c.linear {
			t.Fatalf("priorityToLinear(%v) = %d, want %d", c.want, got, c.linear)
		}
	}
}

func TestCategory(t *testing.T) {
	cases := map[string]pb.ColumnCategory{
		"backlog":   pb.ColumnCategory_COLUMN_CATEGORY_TODO,
		"unstarted": pb.ColumnCategory_COLUMN_CATEGORY_TODO,
		"triage":    pb.ColumnCategory_COLUMN_CATEGORY_TODO,
		"started":   pb.ColumnCategory_COLUMN_CATEGORY_IN_PROGRESS,
		"completed": pb.ColumnCategory_COLUMN_CATEGORY_DONE,
		"canceled":  pb.ColumnCategory_COLUMN_CATEGORY_DONE,
		"duplicate": pb.ColumnCategory_COLUMN_CATEGORY_DONE,
		"unknown":   pb.ColumnCategory_COLUMN_CATEGORY_OTHER,
	}
	for in, want := range cases {
		if got := category(in); got != want {
			t.Fatalf("category(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestTaskFromIssue(t *testing.T) {
	archived := "2026-10-20T00:00:00Z"
	is := &issue{
		ID: "uuid", Identifier: "ENG-123", Title: "Fix it", Description: "body",
		URL: "https://linear.app/x/issue/ENG-123", Priority: 2, SortOrder: 12.5,
		ArchivedAt: &archived, CreatedAt: "2026-10-19T00:00:00Z",
		UpdatedAt: "2026-10-20T00:00:00Z",
		State:     &state{Name: "In Progress", Type: "started", Position: 2},
		Assignee:  &user{DisplayName: "Ada", Email: "ada@example.com"},
	}
	is.Labels.Nodes = []label{{Name: "bug"}, {Name: "ui"}}
	task := taskFromIssue(is)
	if task.Id != "uuid" || task.Key != "ENG-123" || task.Title != "Fix it" {
		t.Fatalf("unexpected task: %+v", task)
	}
	if task.Status != "In Progress" || task.Priority != pb.Priority_PRIORITY_HIGH {
		t.Fatalf("status/priority: %+v", task)
	}
	if task.Assignee != "Ada" || len(task.Labels) != 2 || !task.Archived {
		t.Fatalf("mapping: %+v", task)
	}
	if task.Position != 12.5 || task.Url == "" {
		t.Fatalf("position/url: %+v", task)
	}
	if task.CreatedAt == nil || task.CreatedAt.AsTime().Year() != 2026 {
		t.Fatalf("created_at: %v", task.CreatedAt)
	}
	if task.UpdatedAt == nil || timestamppb.New(task.UpdatedAt.AsTime()).AsTime().IsZero() {
		t.Fatalf("updated_at: %v", task.UpdatedAt)
	}
}

func TestReadyStatePrefersUnstarted(t *testing.T) {
	team := &teamInfo{}
	team.States.Nodes = []state{
		{Name: "Backlog", Type: "backlog", Position: 0},
		{Name: "Todo", Type: "unstarted", Position: 1},
		{Name: "In Progress", Type: "started", Position: 2},
	}
	if got := readyState(team, ""); got == nil || got.Name != "Todo" {
		t.Fatalf("ready = %+v", got)
	}
	if got := readyState(team, "Backlog"); got == nil || got.Name != "Backlog" {
		t.Fatalf("override = %+v", got)
	}
}

func TestSortOrderBetween(t *testing.T) {
	tasks := []*pb.Task{
		{Id: "a", Position: 100},
		{Id: "b", Position: 200},
		{Id: "c", Position: 300},
	}
	got, err := sortOrderBetween(tasks, "b", true)
	if err != nil || got != 150 {
		t.Fatalf("before b = %v, %v", got, err)
	}
	got, err = sortOrderBetween(tasks, "b", false)
	if err != nil || got != 250 {
		t.Fatalf("after b = %v, %v", got, err)
	}
	got, err = sortOrderBetween(tasks, "a", true)
	if err != nil || got != -924 {
		t.Fatalf("before first = %v, %v", got, err)
	}
}

func TestOAuthAuthorizeURL(t *testing.T) {
	u := oauthAuthorizeURL("client123", "state456", "challenge789")
	for _, want := range []string{
		"client_id=client123", "state=state456", "code_challenge=challenge789",
		"code_challenge_method=S256", "scope=read%2Cwrite", "actor=user",
		"redirect_uri=http%3A%2F%2F127.0.0.1%3A43172%2Fcallback",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("authorize URL missing %q: %s", want, u)
		}
	}
}
