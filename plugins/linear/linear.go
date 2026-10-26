// Package linear implements a Linear backend. Tasks are issues in one team;
// workflow states are board columns.
package linear

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

// Config is the [linear] section of the config file.
type Config struct {
	Team          string `json:"team"`
	OAuthClientID string `json:"oauth_client_id"`
	TokenEnv      string `json:"token_env"`
	ReadyStatus   string `json:"ready_status"`
}

// Backend implements sdk.Backend for Linear.
type Backend struct {
	cfg       Config
	client    *client
	oauthFlow *oauthFlow
}

// NewFromEnv builds the backend from the host-provided JSON config.
func NewFromEnv() (*Backend, error) {
	var cfg Config
	if raw := os.Getenv(sdk.PluginConfigEnv); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, fmt.Errorf("parse linear config: %w", err)
		}
	}
	return New(cfg)
}

// New constructs a Linear backend. Credentials are resolved lazily so that
// `tasks auth login linear` can open the plugin without a token.
func New(cfg Config) (*Backend, error) {
	if cfg.Team == "" {
		return nil, errors.New("linear team is not configured (run `tasks init --backend linear --team <key>`)")
	}
	return &Backend{cfg: cfg, client: newClient(cfg)}, nil
}

func (b *Backend) Describe(ctx context.Context) (*pb.BackendInfo, error) {
	team, err := b.client.teamInfo(ctx)
	if err != nil {
		return nil, err
	}
	cols := append([]state(nil), team.States.Nodes...)
	sort.SliceStable(cols, func(i, j int) bool { return cols[i].Position < cols[j].Position })
	columns := make([]*pb.Column, 0, len(cols))
	for _, s := range cols {
		columns = append(columns, &pb.Column{
			Id: s.ID, Name: s.Name, Position: s.Position, Category: category(s.Type),
		})
	}
	ready := ""
	if s := readyState(team, b.cfg.ReadyStatus); s != nil {
		ready = s.Name
	} else if len(columns) > 0 {
		ready = columns[0].Name
	}
	boardURL := ""
	if team.Organization.URLKey != "" {
		boardURL = fmt.Sprintf("https://linear.app/%s/team/%s/all", team.Organization.URLKey, team.Key)
	}
	return &pb.BackendInfo{
		Name: "linear", DisplayName: "Linear", Version: "1",
		ProtocolVersion: sdk.ProtocolVersion,
		BoardName:       team.Name, BoardUrl: boardURL, Columns: columns,
		ReadyStatus: ready,
		Capabilities: &pb.Capabilities{
			Comments: true, Labels: true, Assignee: true,
			Ordering: true, Archive: true, Priority: true, Search: true,
		},
	}, nil
}

func category(stateType string) pb.ColumnCategory {
	switch stateType {
	case "backlog", "unstarted", "triage":
		return pb.ColumnCategory_COLUMN_CATEGORY_TODO
	case "started":
		return pb.ColumnCategory_COLUMN_CATEGORY_IN_PROGRESS
	case "completed", "canceled", "duplicate":
		return pb.ColumnCategory_COLUMN_CATEGORY_DONE
	default:
		return pb.ColumnCategory_COLUMN_CATEGORY_OTHER
	}
}

func (b *Backend) Setup(ctx context.Context, createMissing bool) ([]string, error) {
	team, err := b.client.teamInfo(ctx)
	if err != nil {
		return nil, err
	}
	if len(team.States.Nodes) == 0 {
		return nil, fmt.Errorf("linear team %s has no workflow states", team.Key)
	}
	if b.cfg.ReadyStatus != "" {
		found := false
		for _, s := range team.States.Nodes {
			if strings.EqualFold(s.Name, b.cfg.ReadyStatus) {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("ready_status %q is not a workflow state of team %s", b.cfg.ReadyStatus, team.Key)
		}
	}
	return nil, nil
}

func (b *Backend) ListTasks(ctx context.Context, f *pb.TaskFilter) ([]*pb.Task, error) {
	if f == nil {
		f = &pb.TaskFilter{}
	}
	return b.client.listIssues(ctx, f)
}

func (b *Backend) GetTask(ctx context.Context, id string) (*pb.Task, error) {
	return b.client.getIssue(ctx, id)
}

func (b *Backend) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.Task, error) {
	title := strings.TrimSpace(req.GetTitle())
	if title == "" {
		return nil, errors.New("title is required")
	}
	team, err := b.client.teamInfo(ctx)
	if err != nil {
		return nil, err
	}
	st, err := resolveState(team, req.GetStatus())
	if err != nil {
		return nil, err
	}
	input := map[string]any{
		"teamId":   team.ID,
		"title":    title,
		"stateId":  st.ID,
		"priority": priorityToLinear(req.GetPriority()),
	}
	if req.GetDescription() != "" {
		input["description"] = req.GetDescription()
	}
	if labels := req.GetLabels(); len(labels) > 0 {
		ids := make([]string, 0, len(labels))
		seen := map[string]bool{}
		for _, name := range labels {
			name = strings.TrimSpace(name)
			if name == "" || seen[strings.ToLower(name)] {
				continue
			}
			seen[strings.ToLower(name)] = true
			l, err := resolveLabel(team, name)
			if err != nil {
				return nil, err
			}
			ids = append(ids, l.ID)
		}
		if len(ids) > 0 {
			input["labelIds"] = ids
		}
	}
	if a := req.GetAssignee(); a != "" {
		u, err := b.client.resolveUser(ctx, a)
		if err != nil {
			return nil, err
		}
		input["assigneeId"] = u.ID
	}
	id, err := b.client.createIssue(ctx, input)
	if err != nil {
		return nil, err
	}
	return b.client.getIssue(ctx, id)
}

func (b *Backend) UpdateTask(ctx context.Context, req *pb.UpdateTaskRequest) (*pb.Task, error) {
	current, err := b.client.getIssue(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	team, err := b.client.teamInfo(ctx)
	if err != nil {
		return nil, err
	}
	input := map[string]any{}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return nil, errors.New("title cannot be empty")
		}
		input["title"] = title
	}
	if req.Description != nil {
		input["description"] = *req.Description
	}
	if req.Priority != nil {
		input["priority"] = priorityToLinear(*req.Priority)
	}
	if req.ClearAssignee {
		input["assigneeId"] = nil
	} else if req.Assignee != nil {
		u, err := b.client.resolveUser(ctx, *req.Assignee)
		if err != nil {
			return nil, err
		}
		input["assigneeId"] = u.ID
	}
	if req.SetLabels {
		ids := make([]string, 0, len(req.Labels))
		seen := map[string]bool{}
		for _, name := range req.Labels {
			name = strings.TrimSpace(name)
			if name == "" || seen[strings.ToLower(name)] {
				continue
			}
			seen[strings.ToLower(name)] = true
			l, err := resolveLabel(team, name)
			if err != nil {
				return nil, err
			}
			ids = append(ids, l.ID)
		}
		input["labelIds"] = ids
	}
	if len(input) == 0 {
		return current, nil
	}
	if err := b.client.updateIssue(ctx, current.Id, input); err != nil {
		return nil, err
	}
	return b.client.getIssue(ctx, current.Id)
}

func (b *Backend) MoveTask(ctx context.Context, req *pb.MoveTaskRequest) (*pb.Task, error) {
	current, err := b.client.getIssue(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	team, err := b.client.teamInfo(ctx)
	if err != nil {
		return nil, err
	}
	st, err := resolveState(team, req.GetStatus())
	if err != nil {
		return nil, err
	}
	input := map[string]any{"stateId": st.ID}
	if req.BeforeId != nil || req.AfterId != nil {
		neighborID := ""
		before := false
		if req.BeforeId != nil {
			neighborID, before = *req.BeforeId, true
		} else {
			neighborID = *req.AfterId
		}
		neighbor, err := b.client.getIssue(ctx, neighborID)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(neighbor.Status, st.Name) {
			return nil, fmt.Errorf("neighbor task %s is not in column %q", neighbor.Key, st.Name)
		}
		tasks, err := b.client.listIssues(ctx, &pb.TaskFilter{Statuses: []string{st.Name}})
		if err != nil {
			return nil, err
		}
		filtered := tasks[:0]
		for _, t := range tasks {
			if t.Id != current.Id {
				filtered = append(filtered, t)
			}
		}
		order, err := sortOrderBetween(filtered, neighbor.Id, before)
		if err != nil {
			return nil, err
		}
		input["sortOrder"] = order
	}
	if err := b.client.updateIssue(ctx, current.Id, input); err != nil {
		return nil, err
	}
	return b.client.getIssue(ctx, current.Id)
}

func (b *Backend) DeleteTask(ctx context.Context, id string, hard bool) error {
	current, err := b.client.getIssue(ctx, id)
	if err != nil {
		return err
	}
	if hard {
		return b.client.hardDeleteIssue(ctx, current.Id)
	}
	return b.client.archiveIssue(ctx, current.Id)
}

func (b *Backend) AddComment(ctx context.Context, taskID, body string) (*pb.Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("comment body is required")
	}
	current, err := b.client.getIssue(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return b.client.addComment(ctx, current.Id, body)
}

func (b *Backend) ListComments(ctx context.Context, taskID string) ([]*pb.Comment, error) {
	current, err := b.client.getIssue(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return b.client.listComments(ctx, current.Id)
}

func (b *Backend) Health(ctx context.Context) error {
	if _, err := b.client.authHeader(ctx); err != nil {
		return err
	}
	if _, err := b.client.currentViewer(ctx); err != nil {
		return err
	}
	_, err := b.client.teamInfo(ctx)
	return err
}
