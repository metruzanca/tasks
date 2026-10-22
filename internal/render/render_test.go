package render

import (
	"testing"

	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

func TestNextPrefersPriorityThenPosition(t *testing.T) {
	tasks := []*pb.Task{
		{Id: "1", Status: "todo", Position: 1024, Priority: pb.Priority_PRIORITY_LOW},
		{Id: "2", Status: "todo", Position: 2048, Priority: pb.Priority_PRIORITY_URGENT},
		{Id: "3", Status: "todo", Position: 1024, Priority: pb.Priority_PRIORITY_URGENT},
		{Id: "4", Status: "done", Position: 1024, Priority: pb.Priority_PRIORITY_URGENT},
	}
	got := Next(tasks, "todo")
	if got == nil || got.Id != "3" {
		t.Fatalf("next = %+v, want id 3", got)
	}
}

func TestSortTasksUsesColumnOrder(t *testing.T) {
	cols := []*pb.Column{{Name: "todo"}, {Name: "done"}}
	tasks := []*pb.Task{
		{Id: "1", Status: "done", Position: 1},
		{Id: "2", Status: "todo", Position: 2},
		{Id: "3", Status: "todo", Position: 1},
	}
	SortTasks(tasks, cols)
	if tasks[0].Id != "3" || tasks[1].Id != "2" || tasks[2].Id != "1" {
		t.Fatalf("unexpected order: %v %v %v", tasks[0].Id, tasks[1].Id, tasks[2].Id)
	}
}
