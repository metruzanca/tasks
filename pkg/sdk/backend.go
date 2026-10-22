// Package sdk contains the public plugin contract for tasks backends.
package sdk

import (
	"context"

	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

// ProtocolVersion is bumped whenever the plugin RPC contract changes.
const ProtocolVersion = 1

// Backend is the interface every tasks backend implements.
type Backend interface {
	Describe(ctx context.Context) (*pb.BackendInfo, error)
	Setup(ctx context.Context, createMissing bool) ([]string, error)
	ListTasks(ctx context.Context, filter *pb.TaskFilter) ([]*pb.Task, error)
	GetTask(ctx context.Context, id string) (*pb.Task, error)
	CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.Task, error)
	UpdateTask(ctx context.Context, req *pb.UpdateTaskRequest) (*pb.Task, error)
	MoveTask(ctx context.Context, req *pb.MoveTaskRequest) (*pb.Task, error)
	DeleteTask(ctx context.Context, id string, hard bool) error
	AddComment(ctx context.Context, taskID, body string) (*pb.Comment, error)
	ListComments(ctx context.Context, taskID string) ([]*pb.Comment, error)
	Health(ctx context.Context) error
}

// PriorityLabel returns the human name for a priority.
func PriorityLabel(p pb.Priority) string {
	switch p {
	case pb.Priority_PRIORITY_NONE:
		return "none"
	case pb.Priority_PRIORITY_LOW:
		return "low"
	case pb.Priority_PRIORITY_MEDIUM:
		return "medium"
	case pb.Priority_PRIORITY_HIGH:
		return "high"
	case pb.Priority_PRIORITY_URGENT:
		return "urgent"
	default:
		return "unspecified"
	}
}

// ParsePriority parses a human priority name.
func ParsePriority(s string) (pb.Priority, bool) {
	switch s {
	case "", "none":
		return pb.Priority_PRIORITY_NONE, true
	case "low", "p3":
		return pb.Priority_PRIORITY_LOW, true
	case "medium", "med", "p2":
		return pb.Priority_PRIORITY_MEDIUM, true
	case "high", "p1":
		return pb.Priority_PRIORITY_HIGH, true
	case "urgent", "p0", "critical":
		return pb.Priority_PRIORITY_URGENT, true
	default:
		return pb.Priority_PRIORITY_UNSPECIFIED, false
	}
}

// PriorityRank sorts urgent first.
func PriorityRank(p pb.Priority) int {
	switch p {
	case pb.Priority_PRIORITY_URGENT:
		return 0
	case pb.Priority_PRIORITY_HIGH:
		return 1
	case pb.Priority_PRIORITY_MEDIUM:
		return 2
	case pb.Priority_PRIORITY_LOW:
		return 3
	case pb.Priority_PRIORITY_NONE:
		return 4
	default:
		return 5
	}
}

// CategoryLabel returns a short label for a column category.
func CategoryLabel(c pb.ColumnCategory) string {
	switch c {
	case pb.ColumnCategory_COLUMN_CATEGORY_TODO:
		return "todo"
	case pb.ColumnCategory_COLUMN_CATEGORY_IN_PROGRESS:
		return "in-progress"
	case pb.ColumnCategory_COLUMN_CATEGORY_DONE:
		return "done"
	default:
		return "other"
	}
}
