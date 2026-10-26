// Package github implements a GitHub Projects v2 backend. Tasks are issues in a
// configured repository that are added to a project board.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Config is the [github] section of the config file.
type Config struct {
	Owner         string `json:"owner"`
	Project       int    `json:"project"`
	Repo          string `json:"repo"`
	StatusField   string `json:"status_field"`
	PriorityField string `json:"priority_field"`
	TokenEnv      string `json:"token_env"`
}

// Backend implements sdk.Backend for GitHub Projects.
type Backend struct {
	cfg    Config
	client *client
}

// NewFromEnv builds the backend from the host-provided JSON config.
func NewFromEnv() (*Backend, error) {
	var cfg Config
	if raw := os.Getenv(sdk.PluginConfigEnv); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, fmt.Errorf("parse github config: %w", err)
		}
	}
	return New(cfg)
}

// New constructs a GitHub backend and validates required settings.
func New(cfg Config) (*Backend, error) {
	if cfg.Owner == "" {
		return nil, errors.New("github owner is not configured (run `tasks init --backend github`)")
	}
	if cfg.Project == 0 {
		return nil, errors.New("github project number is not configured")
	}
	if cfg.Repo == "" {
		return nil, errors.New("github repo is not configured (expected owner/name)")
	}
	if !strings.Contains(cfg.Repo, "/") {
		return nil, fmt.Errorf("github repo %q must be owner/name", cfg.Repo)
	}
	if cfg.StatusField == "" {
		cfg.StatusField = "Status"
	}
	if cfg.PriorityField == "" {
		cfg.PriorityField = "Priority"
	}
	return &Backend{cfg: cfg, client: newClient(cfg)}, nil
}

func resolveToken(ctx context.Context, envName string) (string, error) {
	if envName != "" {
		if v := os.Getenv(envName); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("token environment variable %s is empty", envName)
	}
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v, nil
		}
	}
	if creds, err := sdk.LoadCredential("github"); err == nil && creds["token"] != "" {
		return creds["token"], nil
	}
	if out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output(); err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t, nil
		}
	}
	return "", errors.New("no GitHub token found: run `tasks auth login github`, set GH_TOKEN/GITHUB_TOKEN, or run `gh auth login`")
}

func (b *Backend) Describe(ctx context.Context) (*pb.BackendInfo, error) {
	p, err := b.client.project(ctx)
	if err != nil {
		return nil, err
	}
	status := b.client.statusField(p)
	priority := b.client.priorityField(p)
	if status == nil {
		return nil, fmt.Errorf("project %q has no %q single-select field", p.Title, b.cfg.StatusField)
	}
	var cols []*pb.Column
	for i, o := range status.Options {
		cols = append(cols, &pb.Column{
			Id: o.Name, Name: o.Name, Position: float64(i+1) * 1024,
			Category: categorize(o.Name),
		})
	}
	ready := ""
	for _, c := range cols {
		if strings.EqualFold(c.Name, "todo") {
			ready = c.Name
			break
		}
	}
	if ready == "" {
		for _, c := range cols {
			if strings.EqualFold(c.Name, "ready") {
				ready = c.Name
				break
			}
		}
	}
	if ready == "" {
		for _, c := range cols {
			if c.Category == pb.ColumnCategory_COLUMN_CATEGORY_TODO {
				ready = c.Name
				break
			}
		}
	}
	if ready == "" && len(cols) > 0 {
		ready = cols[0].Name
	}
	return &pb.BackendInfo{
		Name: "github", DisplayName: "GitHub Projects", Version: "1",
		ProtocolVersion: sdk.ProtocolVersion,
		BoardName:       p.Title, BoardUrl: p.URL, Columns: cols,
		ReadyStatus: ready,
		Capabilities: &pb.Capabilities{
			Comments: true, Labels: true, Assignee: true,
			Ordering: false, Archive: true, Priority: priority != nil, Search: true,
		},
	}, nil
}

func categorize(name string) pb.ColumnCategory {
	l := strings.ToLower(name)
	switch {
	case strings.Contains(l, "done"), strings.Contains(l, "complete"), strings.Contains(l, "closed"), strings.Contains(l, "shipped"):
		return pb.ColumnCategory_COLUMN_CATEGORY_DONE
	case strings.Contains(l, "progress"), strings.Contains(l, "doing"), strings.Contains(l, "review"), strings.Contains(l, "active"):
		return pb.ColumnCategory_COLUMN_CATEGORY_IN_PROGRESS
	case strings.Contains(l, "todo"), strings.Contains(l, "backlog"), strings.Contains(l, "ready"), strings.Contains(l, "next"):
		return pb.ColumnCategory_COLUMN_CATEGORY_TODO
	default:
		return pb.ColumnCategory_COLUMN_CATEGORY_OTHER
	}
}

func (b *Backend) Setup(ctx context.Context, createMissing bool) ([]string, error) {
	p, err := b.client.project(ctx)
	if err != nil {
		return nil, err
	}
	if b.client.statusField(p) == nil {
		return nil, fmt.Errorf("project %q has no %q single-select field; create it before using the backend", p.Title, b.cfg.StatusField)
	}
	if b.client.priorityField(p) != nil || !createMissing {
		return nil, nil
	}
	if err := b.client.createPriorityField(ctx, p.ID); err != nil {
		return nil, err
	}
	b.client.invalidate()
	return []string{fmt.Sprintf("created single-select field %q with None/Low/Medium/High/Urgent", b.cfg.PriorityField)}, nil
}

func (b *Backend) ListTasks(ctx context.Context, f *pb.TaskFilter) ([]*pb.Task, error) {
	if f == nil {
		f = &pb.TaskFilter{}
	}
	tasks, err := b.client.listIssues(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*pb.Task, 0, len(tasks))
	for _, t := range tasks {
		if !f.IncludeArchived && t.Archived {
			continue
		}
		if len(f.Statuses) > 0 && !containsFold(f.Statuses, t.Status) {
			continue
		}
		if f.Assignee != "" && !strings.EqualFold(f.Assignee, t.Assignee) {
			continue
		}
		if f.HasPriority && t.Priority != f.Priority {
			continue
		}
		if f.Search != "" && !strings.Contains(strings.ToLower(t.Title+" "+t.Description), strings.ToLower(f.Search)) {
			continue
		}
		ok := true
		for _, l := range f.Labels {
			if !containsFold(t.Labels, l) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func (b *Backend) GetTask(ctx context.Context, id string) (*pb.Task, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "#"))
	if err != nil {
		return nil, fmt.Errorf("unknown task %q", id)
	}
	return b.client.getIssue(ctx, n)
}

func (b *Backend) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.Task, error) {
	title := strings.TrimSpace(req.GetTitle())
	if title == "" {
		return nil, errors.New("title is required")
	}
	status, err := b.client.resolveStatus(ctx, req.GetStatus())
	if err != nil {
		return nil, err
	}
	priority := req.GetPriority()
	if err := b.client.checkPriority(ctx, priority); err != nil {
		return nil, err
	}
	for _, l := range req.GetLabels() {
		if l = strings.TrimSpace(l); l != "" {
			if err := b.client.ensureLabel(ctx, l); err != nil {
				return nil, err
			}
		}
	}
	assignee := req.GetAssignee()
	if strings.EqualFold(assignee, "@me") || strings.EqualFold(assignee, "me") {
		assignee, err = b.client.viewer(ctx)
		if err != nil {
			return nil, err
		}
	}
	issue, err := b.client.createIssue(ctx, title, req.GetDescription(), req.GetLabels(), assignee)
	if err != nil {
		return nil, err
	}
	itemID, err := b.client.addItem(ctx, issue.NodeID)
	if err != nil {
		return nil, fmt.Errorf("issue #%d created but adding it to the project failed: %w", issue.Number, err)
	}
	if err := b.client.setStatus(ctx, itemID, status); err != nil {
		return nil, err
	}
	if priority != pb.Priority_PRIORITY_UNSPECIFIED && priority != pb.Priority_PRIORITY_NONE {
		if err := b.client.setPriority(ctx, itemID, priority); err != nil {
			return nil, err
		}
	}
	return b.GetTask(ctx, strconv.Itoa(issue.Number))
}

func (b *Backend) UpdateTask(ctx context.Context, req *pb.UpdateTaskRequest) (*pb.Task, error) {
	n, err := b.GetTask(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	issueNum, _ := strconv.Atoi(n.Id)
	var patch map[string]any
	if req.Title != nil || req.Description != nil {
		patch = map[string]any{}
		if req.Title != nil {
			t := strings.TrimSpace(*req.Title)
			if t == "" {
				return nil, errors.New("title cannot be empty")
			}
			patch["title"] = t
		}
		if req.Description != nil {
			patch["body"] = *req.Description
		}
	}
	if len(patch) > 0 {
		if err := b.client.patchIssue(ctx, issueNum, patch); err != nil {
			return nil, err
		}
	}
	if req.SetLabels {
		for _, l := range req.Labels {
			if l = strings.TrimSpace(l); l != "" {
				if err := b.client.ensureLabel(ctx, l); err != nil {
					return nil, err
				}
			}
		}
		if err := b.client.setIssueLabels(ctx, issueNum, req.Labels); err != nil {
			return nil, err
		}
	}
	if req.ClearAssignee {
		if err := b.client.setIssueAssignees(ctx, issueNum, nil); err != nil {
			return nil, err
		}
	} else if req.Assignee != nil {
		a := *req.Assignee
		if strings.EqualFold(a, "@me") || strings.EqualFold(a, "me") {
			a, err = b.client.viewer(ctx)
			if err != nil {
				return nil, err
			}
		}
		if err := b.client.setIssueAssignees(ctx, issueNum, []string{a}); err != nil {
			return nil, err
		}
	}
	if req.Priority != nil {
		if err := b.client.checkPriority(ctx, *req.Priority); err != nil {
			return nil, err
		}
		itemID, err := b.client.itemForIssue(ctx, issueNum)
		if err != nil {
			return nil, err
		}
		if err := b.client.setPriority(ctx, itemID, *req.Priority); err != nil {
			return nil, err
		}
	}
	return b.GetTask(ctx, req.GetId())
}

func (b *Backend) MoveTask(ctx context.Context, req *pb.MoveTaskRequest) (*pb.Task, error) {
	if req.BeforeId != nil || req.AfterId != nil {
		return nil, errors.New("this backend does not support manual ordering within a column")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(req.GetId(), "#"))
	if err != nil {
		return nil, fmt.Errorf("unknown task %q", req.GetId())
	}
	status, err := b.client.resolveStatus(ctx, req.GetStatus())
	if err != nil {
		return nil, err
	}
	itemID, err := b.client.itemForIssue(ctx, n)
	if err != nil {
		return nil, err
	}
	if err := b.client.setStatus(ctx, itemID, status); err != nil {
		return nil, err
	}
	return b.GetTask(ctx, req.GetId())
}

func (b *Backend) DeleteTask(ctx context.Context, id string, hard bool) error {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "#"))
	if err != nil {
		return fmt.Errorf("unknown task %q", id)
	}
	itemID, err := b.client.itemForIssue(ctx, n)
	if err != nil {
		return err
	}
	if err := b.client.archiveItem(ctx, itemID); err != nil {
		return err
	}
	return b.client.patchIssue(ctx, n, map[string]any{"state": "closed"})
}

func (b *Backend) AddComment(ctx context.Context, taskID, body string) (*pb.Comment, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(taskID, "#"))
	if err != nil {
		return nil, fmt.Errorf("unknown task %q", taskID)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("comment body is required")
	}
	return b.client.addComment(ctx, n, body)
}

func (b *Backend) ListComments(ctx context.Context, taskID string) ([]*pb.Comment, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(taskID, "#"))
	if err != nil {
		return nil, fmt.Errorf("unknown task %q", taskID)
	}
	return b.client.listComments(ctx, n)
}

func (b *Backend) Health(ctx context.Context) error {
	if _, err := b.client.authToken(ctx); err != nil {
		return err
	}
	if _, err := b.client.project(ctx); err != nil {
		return err
	}
	return nil
}

func timePtr(s string) *timestamppb.Timestamp {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return timestamppb.New(t)
}
