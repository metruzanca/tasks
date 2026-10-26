package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	graphqlEndpoint = "https://api.linear.app/graphql"
	tokenEndpoint   = "https://api.linear.app/oauth/token"

	defaultOAuthClientID = "8667dfd6190a1f9a862ac0246ca447ff"
	oauthRedirectURI     = "http://127.0.0.1:43172/callback"
)

type user struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

func (u *user) label() string {
	if u == nil {
		return ""
	}
	if u.DisplayName != "" {
		return u.DisplayName
	}
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

type state struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Position float64 `json:"position"`
}

type label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type teamInfo struct {
	ID           string `json:"id"`
	Key          string `json:"key"`
	Name         string `json:"name"`
	Organization struct {
		URLKey string `json:"urlKey"`
	} `json:"organization"`
	States struct {
		Nodes []state `json:"nodes"`
	} `json:"states"`
	Labels struct {
		Nodes []label `json:"nodes"`
	} `json:"labels"`
	Members struct {
		Nodes []user `json:"nodes"`
	} `json:"members"`
}

type comment struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	User      *user  `json:"user"`
}

type pageInfo struct {
	HasNext bool   `json:"hasNextPage"`
	Cursor  string `json:"endCursor"`
}

type issue struct {
	ID          string  `json:"id"`
	Identifier  string  `json:"identifier"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	URL         string  `json:"url"`
	Priority    int     `json:"priority"`
	SortOrder   float64 `json:"sortOrder"`
	ArchivedAt  *string `json:"archivedAt"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	State       *state  `json:"state"`
	Assignee    *user   `json:"assignee"`
	Labels      struct {
		Nodes []label `json:"nodes"`
	} `json:"labels"`
	Comments struct {
		Nodes    []comment `json:"nodes"`
		PageInfo pageInfo  `json:"pageInfo"`
	} `json:"comments"`
}

type client struct {
	cfg        Config
	http       *http.Client
	graphqlURL string
	tokenURL   string
	now        func() time.Time

	teamOnce sync.Once
	team     *teamInfo
	teamErr  error

	viewerOnce sync.Once
	viewer     *user
	viewerErr  error
}

func newClient(cfg Config) *client {
	return &client{
		cfg:        cfg,
		http:       &http.Client{Timeout: 30 * time.Second},
		graphqlURL: graphqlEndpoint,
		tokenURL:   tokenEndpoint,
		now:        time.Now,
	}
}

func (c *client) teamInfo(ctx context.Context) (*teamInfo, error) {
	c.teamOnce.Do(func() {
		const q = `query($id:String!) {
  team(id:$id) {
    id key name
    organization { urlKey }
    states { nodes { id name type position } }
    labels(first:250) { nodes { id name } }
    members(first:250) { nodes { id name displayName email } }
  }
}`
		var out struct {
			Team *teamInfo `json:"team"`
		}
		if err := c.graphql(ctx, q, map[string]any{"id": c.cfg.Team}, &out); err != nil {
			c.teamErr = err
			return
		}
		if out.Team == nil {
			c.teamErr = fmt.Errorf("linear team %q not found", c.cfg.Team)
			return
		}
		c.team = out.Team
	})
	return c.team, c.teamErr
}

func (c *client) resetAuth() {
	c.viewerOnce = sync.Once{}
	c.viewer = nil
	c.viewerErr = nil
	c.teamOnce = sync.Once{}
	c.team = nil
	c.teamErr = nil
}

func (c *client) currentViewer(ctx context.Context) (*user, error) {
	c.viewerOnce.Do(func() {
		var out struct {
			Viewer *user `json:"viewer"`
		}
		if err := c.graphql(ctx, `query { viewer { id name displayName email } }`, nil, &out); err != nil {
			c.viewerErr = err
			return
		}
		c.viewer = out.Viewer
		if c.viewer == nil {
			c.viewerErr = errors.New("linear did not return the authenticated user")
		}
	})
	return c.viewer, c.viewerErr
}

func (c *client) authHeader(ctx context.Context) (string, error) {
	if c.cfg.TokenEnv != "" {
		v := os.Getenv(c.cfg.TokenEnv)
		if v == "" {
			return "", fmt.Errorf("token environment variable %s is empty", c.cfg.TokenEnv)
		}
		return rawOrBearer(v), nil
	}
	if v := os.Getenv("LINEAR_API_KEY"); v != "" {
		return v, nil
	}
	if v := os.Getenv("LINEAR_ACCESS_TOKEN"); v != "" {
		return "Bearer " + v, nil
	}
	creds, err := sdk.LoadCredential("linear")
	if err != nil {
		return "", err
	}
	if creds["auth_type"] == "api_key" && creds["token"] != "" {
		return creds["token"], nil
	}
	if creds["access_token"] != "" {
		if expiry, _ := time.Parse(time.RFC3339, creds["expires_at"]); expiry.IsZero() || expiry.After(c.now().Add(5*time.Minute)) {
			return "Bearer " + creds["access_token"], nil
		}
		return c.refresh(ctx, creds)
	}
	if creds["token"] != "" {
		return creds["token"], nil
	}
	return "", errors.New("no Linear credentials found: run `tasks auth login linear`")
}

func (c *client) refresh(ctx context.Context, creds map[string]string) (string, error) {
	refreshToken := creds["refresh_token"]
	if refreshToken == "" {
		return "", errors.New("Linear session expired: run `tasks auth login linear`")
	}
	clientID := creds["client_id"]
	if clientID == "" {
		clientID = c.oauthClientID()
	}
	if clientID == "" {
		return "", errors.New("Linear session expired and no OAuth client ID is configured: run `tasks auth login linear`")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Linear token refresh failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var tok tokenResponse
	if err := json.Unmarshal(data, &tok); err != nil {
		return "", fmt.Errorf("decode Linear token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("Linear token refresh returned no access token")
	}
	if err := sdk.UpdateCredential("linear", func(values map[string]string) error {
		values["auth_type"] = "oauth"
		values["access_token"] = tok.AccessToken
		if tok.RefreshToken != "" {
			values["refresh_token"] = tok.RefreshToken
		}
		values["token_type"] = tok.TokenType
		values["scope"] = tok.Scope
		values["client_id"] = clientID
		values["expires_at"] = c.now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
		return nil
	}); err != nil {
		return "", err
	}
	return "Bearer " + tok.AccessToken, nil
}

func (c *client) oauthClientID() string {
	if c.cfg.OAuthClientID != "" {
		if isOAuthDisabled(c.cfg.OAuthClientID) {
			return ""
		}
		return c.cfg.OAuthClientID
	}
	for _, k := range []string{"LINEAR_OAUTH_CLIENT_ID", "TASKS_LINEAR_CLIENT_ID"} {
		if v := os.Getenv(k); v != "" {
			if isOAuthDisabled(v) {
				return ""
			}
			return v
		}
	}
	return defaultOAuthClientID
}

func isOAuthDisabled(v string) bool {
	return strings.EqualFold(v, "none") || strings.EqualFold(v, "off") || v == "-"
}

func rawOrBearer(v string) string {
	if strings.HasPrefix(strings.ToLower(v), "bearer ") {
		return v
	}
	return v
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
}

func (c *client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	auth, err := c.authHeader(ctx)
	if err != nil {
		return err
	}
	return c.graphqlWithAuth(ctx, auth, query, vars, out)
}

func (c *client) graphqlWithAuth(ctx context.Context, auth, query string, vars map[string]any, out any) error {
	if vars == nil {
		vars = map[string]any{}
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Linear GraphQL HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				UserPresentable string `json:"userPresentableMessage"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode Linear response: %w", err)
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, len(env.Errors))
		for i, e := range env.Errors {
			msgs[i] = e.Message
			if e.Extensions.UserPresentable != "" {
				msgs[i] = e.Extensions.UserPresentable
			}
		}
		return errors.New("Linear: " + strings.Join(msgs, "; "))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decode Linear data: %w", err)
	}
	return nil
}

func (c *client) listIssues(ctx context.Context, f *pb.TaskFilter) ([]*pb.Task, error) {
	team, err := c.teamInfo(ctx)
	if err != nil {
		return nil, err
	}
	filter := map[string]any{
		"team": map[string]any{"id": map[string]any{"eq": team.ID}},
	}
	if len(f.Statuses) > 0 {
		var ids []string
		for _, name := range f.Statuses {
			st, err := resolveState(team, name)
			if err != nil {
				return nil, err
			}
			ids = append(ids, st.ID)
		}
		filter["state"] = map[string]any{"id": map[string]any{"in": ids}}
	}
	if f.HasPriority {
		filter["priority"] = map[string]any{"eq": priorityToLinear(f.Priority)}
	}
	if f.Assignee != "" {
		u, err := c.resolveUser(ctx, f.Assignee)
		if err != nil {
			return nil, err
		}
		filter["assignee"] = map[string]any{"id": map[string]any{"eq": u.ID}}
	}
	if len(f.Labels) > 0 {
		var and []any
		for _, name := range f.Labels {
			l, err := resolveLabel(team, name)
			if err != nil {
				return nil, err
			}
			and = append(and, map[string]any{
				"labels": map[string]any{"some": map[string]any{"id": map[string]any{"eq": l.ID}}},
			})
		}
		filter["and"] = and
	}
	if f.Search != "" {
		filter["or"] = []any{
			map[string]any{"title": map[string]any{"containsIgnoreCase": f.Search}},
			map[string]any{"description": map[string]any{"containsIgnoreCase": f.Search}},
		}
	}

	const q = `query($filter:IssueFilter, $includeArchived:Boolean, $first:Int, $after:String) {
  issues(filter:$filter, includeArchived:$includeArchived, first:$first, after:$after) {
    nodes {
      id identifier title description url priority sortOrder archivedAt createdAt updatedAt
      state { id name type position }
      assignee { id name displayName email }
      labels { nodes { id name } }
    }
    pageInfo { hasNextPage endCursor }
  }
}`
	var (
		tasks  []*pb.Task
		cursor string
	)
	for {
		vars := map[string]any{
			"filter":          filter,
			"includeArchived": f.IncludeArchived,
			"first":           100,
		}
		if cursor != "" {
			vars["after"] = cursor
		}
		var out struct {
			Issues struct {
				Nodes    []issue  `json:"nodes"`
				PageInfo pageInfo `json:"pageInfo"`
			} `json:"issues"`
		}
		if err := c.graphql(ctx, q, vars, &out); err != nil {
			return nil, err
		}
		for i := range out.Issues.Nodes {
			if !f.IncludeArchived && isArchived(&out.Issues.Nodes[i]) {
				continue
			}
			tasks = append(tasks, taskFromIssue(&out.Issues.Nodes[i]))
		}
		if !out.Issues.PageInfo.HasNext {
			break
		}
		cursor = out.Issues.PageInfo.Cursor
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		pi, pj := stateOrder(team, tasks[i].Status), stateOrder(team, tasks[j].Status)
		if pi != pj {
			return pi < pj
		}
		if tasks[i].Position != tasks[j].Position {
			return tasks[i].Position < tasks[j].Position
		}
		return tasks[i].Key < tasks[j].Key
	})
	start := int(f.Offset)
	if start > len(tasks) {
		start = len(tasks)
	}
	tasks = tasks[start:]
	if f.Limit > 0 && int(f.Limit) < len(tasks) {
		tasks = tasks[:f.Limit]
	}
	return tasks, nil
}

func stateOrder(team *teamInfo, name string) float64 {
	st, err := resolveState(team, name)
	if err != nil {
		return 1e18
	}
	return st.Position
}

func (c *client) getIssue(ctx context.Context, id string) (*pb.Task, error) {
	const q = `query($id:String!) {
  issue(id:$id) {
    id identifier title description url priority sortOrder archivedAt createdAt updatedAt
    state { id name type position }
    assignee { id name displayName email }
    labels { nodes { id name } }
    comments(first:100) { nodes { id body createdAt user { id name displayName email } } }
  }
}`
	var out struct {
		Issue *issue `json:"issue"`
	}
	if err := c.graphql(ctx, q, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Issue == nil {
		return nil, fmt.Errorf("linear issue %q not found", id)
	}
	t := taskFromIssue(out.Issue)
	t.CommentCount = int32(len(out.Issue.Comments.Nodes))
	return t, nil
}

func taskFromIssue(is *issue) *pb.Task {
	t := &pb.Task{
		Id:          is.ID,
		Key:         is.Identifier,
		Title:       is.Title,
		Description: is.Description,
		Url:         is.URL,
		Priority:    priorityFromLinear(is.Priority),
		Position:    is.SortOrder,
		Archived:    isArchived(is),
		CreatedAt:   parseTime(is.CreatedAt),
		UpdatedAt:   parseTime(is.UpdatedAt),
	}
	if is.State != nil {
		t.Status = is.State.Name
	}
	if is.Assignee != nil {
		t.Assignee = is.Assignee.label()
	}
	for _, l := range is.Labels.Nodes {
		t.Labels = append(t.Labels, l.Name)
	}
	return t
}

func isArchived(is *issue) bool {
	return is.ArchivedAt != nil && *is.ArchivedAt != ""
}

func parseTime(s string) *timestamppb.Timestamp {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return timestamppb.New(t)
}

func priorityFromLinear(p int) pb.Priority {
	switch p {
	case 1:
		return pb.Priority_PRIORITY_URGENT
	case 2:
		return pb.Priority_PRIORITY_HIGH
	case 3:
		return pb.Priority_PRIORITY_MEDIUM
	case 4:
		return pb.Priority_PRIORITY_LOW
	default:
		return pb.Priority_PRIORITY_NONE
	}
}

func priorityToLinear(p pb.Priority) int {
	switch p {
	case pb.Priority_PRIORITY_URGENT:
		return 1
	case pb.Priority_PRIORITY_HIGH:
		return 2
	case pb.Priority_PRIORITY_MEDIUM:
		return 3
	case pb.Priority_PRIORITY_LOW:
		return 4
	default:
		return 0
	}
}

func resolveState(team *teamInfo, name string) (*state, error) {
	if len(team.States.Nodes) == 0 {
		return nil, errors.New("linear team has no workflow states")
	}
	if name == "" {
		if s := readyState(team, ""); s != nil {
			return s, nil
		}
		return &team.States.Nodes[0], nil
	}
	for i := range team.States.Nodes {
		s := &team.States.Nodes[i]
		if strings.EqualFold(s.Name, name) || strings.EqualFold(normalize(s.Name), normalize(name)) {
			return s, nil
		}
	}
	names := make([]string, len(team.States.Nodes))
	for i, s := range team.States.Nodes {
		names[i] = s.Name
	}
	return nil, fmt.Errorf("unknown status %q (columns: %s)", name, strings.Join(names, ", "))
}

func readyState(team *teamInfo, override string) *state {
	if override != "" {
		for i := range team.States.Nodes {
			if strings.EqualFold(team.States.Nodes[i].Name, override) {
				return &team.States.Nodes[i]
			}
		}
	}
	best := -1
	for i := range team.States.Nodes {
		if team.States.Nodes[i].Type == "unstarted" && (best < 0 || team.States.Nodes[i].Position < team.States.Nodes[best].Position) {
			best = i
		}
	}
	if best >= 0 {
		return &team.States.Nodes[best]
	}
	for i := range team.States.Nodes {
		if team.States.Nodes[i].Type == "backlog" {
			return &team.States.Nodes[i]
		}
	}
	return nil
}

func normalize(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", " ")
}

func resolveLabel(team *teamInfo, name string) (*label, error) {
	for i := range team.Labels.Nodes {
		if strings.EqualFold(team.Labels.Nodes[i].Name, name) {
			return &team.Labels.Nodes[i], nil
		}
	}
	names := make([]string, len(team.Labels.Nodes))
	for i, l := range team.Labels.Nodes {
		names[i] = l.Name
	}
	return nil, fmt.Errorf("label %q does not exist in team %s (labels: %s)", name, team.Key, strings.Join(names, ", "))
}

func (c *client) resolveUser(ctx context.Context, name string) (*user, error) {
	if strings.EqualFold(name, "@me") || strings.EqualFold(name, "me") {
		return c.currentViewer(ctx)
	}
	team, err := c.teamInfo(ctx)
	if err != nil {
		return nil, err
	}
	for i := range team.Members.Nodes {
		m := &team.Members.Nodes[i]
		if strings.EqualFold(m.Email, name) || strings.EqualFold(m.DisplayName, name) || strings.EqualFold(m.Name, name) || m.ID == name {
			return m, nil
		}
	}
	return nil, fmt.Errorf("user %q is not a member of team %s", name, team.Key)
}

func (c *client) createIssue(ctx context.Context, input map[string]any) (string, error) {
	const q = `mutation($input:IssueCreateInput!) { issueCreate(input:$input) { success issue { id identifier } } }`
	var out struct {
		Create struct {
			Success bool `json:"success"`
			Issue   *struct {
				ID string `json:"id"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := c.graphql(ctx, q, map[string]any{"input": input}, &out); err != nil {
		return "", err
	}
	if !out.Create.Success || out.Create.Issue == nil || out.Create.Issue.ID == "" {
		return "", errors.New("Linear did not create the issue")
	}
	return out.Create.Issue.ID, nil
}

func (c *client) updateIssue(ctx context.Context, id string, input map[string]any) error {
	const q = `mutation($id:String!, $input:IssueUpdateInput!) { issueUpdate(id:$id, input:$input) { success issue { id } } }`
	var out struct {
		Update struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.graphql(ctx, q, map[string]any{"id": id, "input": input}, &out); err != nil {
		return err
	}
	if !out.Update.Success {
		return errors.New("Linear did not update the issue")
	}
	return nil
}

func (c *client) archiveIssue(ctx context.Context, id string) error {
	return c.deleteLike(ctx, "issueArchive", `mutation($id:String!) { issueArchive(id:$id) { success } }`, id)
}

func (c *client) hardDeleteIssue(ctx context.Context, id string) error {
	return c.deleteLike(ctx, "issueDelete", `mutation($id:String!) { issueDelete(id:$id) { success } }`, id)
}

func (c *client) deleteLike(ctx context.Context, field, q, id string) error {
	var out map[string]struct {
		Success bool `json:"success"`
	}
	if err := c.graphql(ctx, q, map[string]any{"id": id}, &out); err != nil {
		return err
	}
	if res, ok := out[field]; !ok || !res.Success {
		return fmt.Errorf("Linear did not complete %s for %s", field, id)
	}
	return nil
}

func (c *client) addComment(ctx context.Context, issueID, body string) (*pb.Comment, error) {
	const q = `mutation($input:CommentCreateInput!) { commentCreate(input:$input) { success comment { id body createdAt user { id name displayName email } } } }`
	var out struct {
		Create struct {
			Success bool     `json:"success"`
			Comment *comment `json:"comment"`
		} `json:"commentCreate"`
	}
	if err := c.graphql(ctx, q, map[string]any{"input": map[string]any{"issueId": issueID, "body": body}}, &out); err != nil {
		return nil, err
	}
	if !out.Create.Success || out.Create.Comment == nil {
		return nil, errors.New("Linear did not create the comment")
	}
	return commentFromLinear(out.Create.Comment), nil
}

func (c *client) listComments(ctx context.Context, issueID string) ([]*pb.Comment, error) {
	const q = `query($id:String!, $after:String) {
  issue(id:$id) {
    comments(first:100, after:$after) {
      nodes { id body createdAt user { id name displayName email } }
      pageInfo { hasNextPage endCursor }
    }
  }
}`
	var comments []*pb.Comment
	cursor := ""
	for {
		vars := map[string]any{"id": issueID}
		if cursor != "" {
			vars["after"] = cursor
		}
		var out struct {
			Issue *struct {
				Comments struct {
					Nodes    []comment `json:"nodes"`
					PageInfo pageInfo  `json:"pageInfo"`
				} `json:"comments"`
			} `json:"issue"`
		}
		if err := c.graphql(ctx, q, vars, &out); err != nil {
			return nil, err
		}
		if out.Issue == nil {
			return nil, fmt.Errorf("linear issue %q not found", issueID)
		}
		for i := range out.Issue.Comments.Nodes {
			comments = append(comments, commentFromLinear(&out.Issue.Comments.Nodes[i]))
		}
		if !out.Issue.Comments.PageInfo.HasNext {
			break
		}
		cursor = out.Issue.Comments.PageInfo.Cursor
	}
	return comments, nil
}

func commentFromLinear(c *comment) *pb.Comment {
	return &pb.Comment{
		Id:        c.ID,
		Body:      c.Body,
		Author:    c.User.label(),
		CreatedAt: parseTime(c.CreatedAt),
	}
}

func (c *client) verify(ctx context.Context, auth string) (*user, error) {
	var out struct {
		Viewer *user `json:"viewer"`
	}
	if err := c.graphqlWithAuth(ctx, auth, `query { viewer { id name displayName email } }`, nil, &out); err != nil {
		return nil, err
	}
	if out.Viewer == nil {
		return nil, errors.New("Linear did not return the authenticated user")
	}
	return out.Viewer, nil
}

func sortOrderBetween(tasks []*pb.Task, neighborID string, before bool) (float64, error) {
	idx := -1
	for i, t := range tasks {
		if t.Id == neighborID || strings.EqualFold(t.Key, neighborID) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, fmt.Errorf("neighbor task %q was not found in the target column", neighborID)
	}
	n := tasks[idx].Position
	if before {
		if idx == 0 {
			return n - 1024, nil
		}
		return (tasks[idx-1].Position + n) / 2, nil
	}
	if idx == len(tasks)-1 {
		return n + 1024, nil
	}
	return (n + tasks[idx+1].Position) / 2, nil
}

// Team describes a Linear team for selection UIs.
type Team struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// ListTeams returns the teams visible to the configured credentials.
func ListTeams(ctx context.Context, cfg Config) ([]Team, error) {
	c := newClient(cfg)
	const q = `query { teams { nodes { id key name } } }`
	var out struct {
		Teams struct {
			Nodes []Team `json:"nodes"`
		} `json:"teams"`
	}
	if err := c.graphql(ctx, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Teams.Nodes, nil
}
