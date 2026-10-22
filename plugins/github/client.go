package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

type client struct {
	cfg        Config
	token      string
	http       *http.Client
	graphqlURL string
	restURL    string

	once    sync.Once
	proj    *project
	projErr error
}

func newClient(cfg Config, token string) *client {
	return &client{
		cfg:        cfg,
		token:      token,
		http:       &http.Client{Timeout: 30 * time.Second},
		graphqlURL: "https://api.github.com/graphql",
		restURL:    "https://api.github.com",
	}
}

func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

type project struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Fields struct {
		Nodes []field `json:"nodes"`
	} `json:"fields"`
}

type field struct {
	Typename string   `json:"__typename"`
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Options  []option `json:"options"`
}

type option struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type fieldValue struct {
	Typename string `json:"__typename"`
	Name     string `json:"name"`
	OptionID string `json:"optionId"`
	Field    struct {
		Name string `json:"name"`
	} `json:"field"`
}

func (c *client) invalidate() {
	c.once = sync.Once{}
	c.proj = nil
	c.projErr = nil
}

func (c *client) project(ctx context.Context) (*project, error) {
	c.once.Do(func() {
		c.proj, c.projErr = c.fetchProject(ctx)
	})
	return c.proj, c.projErr
}

func (c *client) fetchProject(ctx context.Context) (*project, error) {
	const q = `query($owner:String!, $number:Int!) {
  user: repositoryOwner(login:$owner) {
    ... on User { projectV2(number:$number) { id title url fields(first:50) { nodes {
      __typename
      ... on ProjectV2SingleSelectField { id name options { id name } }
      ... on ProjectV2FieldCommon { id name }
    } } } }
  }
  org: repositoryOwner(login:$owner) {
    ... on Organization { projectV2(number:$number) { id title url fields(first:50) { nodes {
      __typename
      ... on ProjectV2SingleSelectField { id name options { id name } }
      ... on ProjectV2FieldCommon { id name }
    } } } }
  }
}`
	var out struct {
		User *struct {
			Project *project `json:"projectV2"`
		} `json:"user"`
		Org *struct {
			Project *project `json:"projectV2"`
		} `json:"org"`
	}
	if err := c.graphql(ctx, q, map[string]any{"owner": c.cfg.Owner, "number": c.cfg.Project}, &out); err != nil {
		return nil, err
	}
	if out.User != nil && out.User.Project != nil {
		return out.User.Project, nil
	}
	if out.Org != nil && out.Org.Project != nil {
		return out.Org.Project, nil
	}
	return nil, fmt.Errorf("project #%d not found for %s", c.cfg.Project, c.cfg.Owner)
}

func (c *client) statusField(p *project) *field {
	for i := range p.Fields.Nodes {
		f := &p.Fields.Nodes[i]
		if strings.EqualFold(f.Name, c.cfg.StatusField) && len(f.Options) > 0 {
			return f
		}
	}
	return nil
}

func (c *client) priorityField(p *project) *field {
	for i := range p.Fields.Nodes {
		f := &p.Fields.Nodes[i]
		if strings.EqualFold(f.Name, c.cfg.PriorityField) && len(f.Options) > 0 {
			return f
		}
	}
	return nil
}

func (c *client) resolveStatus(ctx context.Context, status string) (string, error) {
	p, err := c.project(ctx)
	if err != nil {
		return "", err
	}
	f := c.statusField(p)
	if f == nil {
		return "", fmt.Errorf("project has no %q single-select field", c.cfg.StatusField)
	}
	if status == "" {
		for _, o := range f.Options {
			if categorize(o.Name) == pb.ColumnCategory_COLUMN_CATEGORY_TODO {
				return o.Name, nil
			}
		}
		return f.Options[0].Name, nil
	}
	for _, o := range f.Options {
		if strings.EqualFold(o.Name, status) || strings.EqualFold(normalize(o.Name), normalize(status)) {
			return o.Name, nil
		}
	}
	names := make([]string, len(f.Options))
	for i, o := range f.Options {
		names[i] = o.Name
	}
	return "", fmt.Errorf("unknown status %q (options: %s)", status, strings.Join(names, ", "))
}

func normalize(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", " ")
}

func priorityOption(f *field, p pb.Priority) (*option, error) {
	want := sdk.PriorityLabel(p)
	if p == pb.Priority_PRIORITY_UNSPECIFIED || p == pb.Priority_PRIORITY_NONE {
		want = "none"
	}
	for i := range f.Options {
		if strings.EqualFold(f.Options[i].Name, want) {
			return &f.Options[i], nil
		}
	}
	names := make([]string, len(f.Options))
	for i, o := range f.Options {
		names[i] = o.Name
	}
	return nil, fmt.Errorf("project has no %q option in field %q (options: %s)", want, f.Name, strings.Join(names, ", "))
}

func (c *client) checkPriority(ctx context.Context, p pb.Priority) error {
	if p == pb.Priority_PRIORITY_UNSPECIFIED || p == pb.Priority_PRIORITY_NONE {
		return nil
	}
	proj, err := c.project(ctx)
	if err != nil {
		return err
	}
	f := c.priorityField(proj)
	if f == nil {
		return fmt.Errorf("project has no %q field; create a single-select field with options None, Low, Medium, High, Urgent or edit the config", c.cfg.PriorityField)
	}
	_, err = priorityOption(f, p)
	return err
}

func (c *client) setStatus(ctx context.Context, itemID, status string) error {
	p, err := c.project(ctx)
	if err != nil {
		return err
	}
	f := c.statusField(p)
	if f == nil {
		return fmt.Errorf("project has no %q field", c.cfg.StatusField)
	}
	var opt *option
	for i := range f.Options {
		if strings.EqualFold(f.Options[i].Name, status) {
			opt = &f.Options[i]
			break
		}
	}
	if opt == nil {
		return fmt.Errorf("status %q is not an option of %q", status, f.Name)
	}
	return c.setField(ctx, p.ID, itemID, f.ID, opt.ID)
}

func (c *client) setPriority(ctx context.Context, itemID string, pr pb.Priority) error {
	p, err := c.project(ctx)
	if err != nil {
		return err
	}
	f := c.priorityField(p)
	if f == nil {
		return fmt.Errorf("project has no %q field", c.cfg.PriorityField)
	}
	opt, err := priorityOption(f, pr)
	if err != nil {
		return err
	}
	return c.setField(ctx, p.ID, itemID, f.ID, opt.ID)
}

func (c *client) setField(ctx context.Context, projectID, itemID, fieldID, optionID string) error {
	const q = `mutation($project:ID!, $item:ID!, $field:ID!, $option:String!) {
  updateProjectV2ItemFieldValue(input:{projectId:$project, itemId:$item, fieldId:$field, value:{singleSelectOptionId:$option}}) {
    projectV2Item { id }
  }
}`
	return c.graphql(ctx, q, map[string]any{
		"project": projectID, "item": itemID, "field": fieldID, "option": optionID,
	}, nil)
}

func (c *client) createPriorityField(ctx context.Context, projectID string) error {
	const q = `mutation($project:ID!, $name:String!) {
  createProjectV2Field(input:{projectId:$project, dataType:SINGLE_SELECT, name:$name, singleSelectOptions:[
    {name:"None", color:GRAY, description:"Not prioritized"},
    {name:"Low", color:BLUE, description:"Nice to have"},
    {name:"Medium", color:GREEN, description:"Normal priority"},
    {name:"High", color:YELLOW, description:"Important"},
    {name:"Urgent", color:RED, description:"Do next"}
  ]}) { projectV2Field { ... on ProjectV2SingleSelectField { id name } } }
}`
	return c.graphql(ctx, q, map[string]any{"project": projectID, "name": c.cfg.PriorityField}, nil)
}

func (c *client) addItem(ctx context.Context, contentID string) (string, error) {
	p, err := c.project(ctx)
	if err != nil {
		return "", err
	}
	const q = `mutation($project:ID!, $content:ID!) {
  addProjectV2ItemById(input:{projectId:$project, contentId:$content}) { item { id } }
}`
	var out struct {
		Add struct {
			Item struct{ ID string } `json:"item"`
		} `json:"addProjectV2ItemById"`
	}
	if err := c.graphql(ctx, q, map[string]any{"project": p.ID, "content": contentID}, &out); err != nil {
		return "", err
	}
	if out.Add.Item.ID == "" {
		return "", errors.New("GitHub did not return a project item id")
	}
	return out.Add.Item.ID, nil
}

func (c *client) archiveItem(ctx context.Context, itemID string) error {
	p, err := c.project(ctx)
	if err != nil {
		return err
	}
	const q = `mutation($project:ID!, $item:ID!) {
  archiveProjectV2Item(input:{projectId:$project, itemId:$item}) { item { id } }
}`
	return c.graphql(ctx, q, map[string]any{"project": p.ID, "item": itemID}, nil)
}

type itemContent struct {
	Typename  string `json:"__typename"`
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	URL       string `json:"url"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Assignees struct {
		Nodes []struct {
			Login string `json:"login"`
		} `json:"nodes"`
	} `json:"assignees"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
}

type projectItem struct {
	ID      string `json:"id"`
	Project struct {
		ID string `json:"id"`
	} `json:"project"`
	FieldValues struct {
		Nodes []fieldValue `json:"nodes"`
	} `json:"fieldValues"`
	Content itemContent `json:"content"`
}

func (c *client) listIssues(ctx context.Context) ([]*pb.Task, error) {
	p, err := c.project(ctx)
	if err != nil {
		return nil, err
	}
	const q = `query($id:ID!, $cursor:String) {
  node(id:$id) { ... on ProjectV2 { items(first:100, after:$cursor) {
    pageInfo { hasNextPage endCursor }
    nodes {
      id
      fieldValues(first:50) { nodes {
        __typename
        ... on ProjectV2ItemFieldSingleSelectValue { name optionId field { ... on ProjectV2FieldCommon { name } } }
      } }
      content {
        __typename
        ... on Issue {
          number title body url state createdAt updatedAt
          assignees(first:10) { nodes { login } }
          labels(first:50) { nodes { name } }
        }
      }
    }
  } } }
}`
	var out struct {
		Node struct {
			Items struct {
				PageInfo struct {
					HasNext bool   `json:"hasNextPage"`
					Cursor  string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []projectItem `json:"nodes"`
			} `json:"items"`
		} `json:"node"`
	}
	var tasks []*pb.Task
	cursor := ""
	for {
		vars := map[string]any{"id": p.ID}
		if cursor != "" {
			vars["cursor"] = cursor
		}
		out.Node.Items.Nodes = nil
		if err := c.graphql(ctx, q, vars, &out); err != nil {
			return nil, err
		}
		for i := range out.Node.Items.Nodes {
			t, ok := c.taskFromItem(&out.Node.Items.Nodes[i], i)
			if ok {
				tasks = append(tasks, t)
			}
		}
		if !out.Node.Items.PageInfo.HasNext {
			break
		}
		cursor = out.Node.Items.PageInfo.Cursor
	}
	return tasks, nil
}

func (c *client) taskFromItem(it *projectItem, index int) (*pb.Task, bool) {
	if it.Content.Typename != "Issue" || it.Content.Number == 0 {
		return nil, false
	}
	status := ""
	priority := pb.Priority_PRIORITY_NONE
	for _, fv := range it.FieldValues.Nodes {
		switch {
		case strings.EqualFold(fv.Field.Name, c.cfg.StatusField):
			status = fv.Name
		case strings.EqualFold(fv.Field.Name, c.cfg.PriorityField):
			priority, _ = sdk.ParsePriority(strings.ToLower(fv.Name))
		}
	}
	t := &pb.Task{
		Id: strconv.Itoa(it.Content.Number), Key: "#" + strconv.Itoa(it.Content.Number),
		Title: it.Content.Title, Description: it.Content.Body,
		Status: status, Priority: priority, Url: it.Content.URL,
		Archived:  strings.EqualFold(it.Content.State, "CLOSED"),
		Position:  float64(index+1) * 1024,
		CreatedAt: timePtr(it.Content.CreatedAt), UpdatedAt: timePtr(it.Content.UpdatedAt),
	}
	for _, a := range it.Content.Assignees.Nodes {
		if t.Assignee == "" {
			t.Assignee = a.Login
		}
	}
	for _, l := range it.Content.Labels.Nodes {
		t.Labels = append(t.Labels, l.Name)
	}
	return t, true
}

func (c *client) getIssue(ctx context.Context, number int) (*pb.Task, error) {
	p, err := c.project(ctx)
	if err != nil {
		return nil, err
	}
	const q = `query($owner:String!, $repo:String!, $number:Int!) {
  repository(owner:$owner, name:$repo) {
    issue(number:$number) {
      __typename
      number title body url state createdAt updatedAt
      assignees(first:10) { nodes { login } }
      labels(first:50) { nodes { name } }
      projectItems(first:20) {
        nodes {
          id project { id }
          fieldValues(first:50) { nodes {
            __typename
            ... on ProjectV2ItemFieldSingleSelectValue { name optionId field { ... on ProjectV2FieldCommon { name } } }
          } }
        }
      }
    }
  }
}`
	var out struct {
		Repository struct {
			Issue *struct {
				itemContent
				ProjectItems struct {
					Nodes []projectItem `json:"nodes"`
				} `json:"projectItems"`
			} `json:"issue"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, q, map[string]any{
		"owner": c.cfg.Owner, "repo": repoName(c.cfg.Repo), "number": number,
	}, &out); err != nil {
		return nil, err
	}
	if out.Repository.Issue == nil {
		return nil, fmt.Errorf("issue #%d not found in %s", number, c.cfg.Repo)
	}
	iss := out.Repository.Issue
	for i := range iss.ProjectItems.Nodes {
		it := &iss.ProjectItems.Nodes[i]
		if it.ID == "" || projectIDOf(it) != p.ID {
			continue
		}
		it.Content = iss.itemContent
		t, ok := c.taskFromItem(it, 0)
		if !ok {
			break
		}
		return t, nil
	}
	return nil, fmt.Errorf("issue #%d is not on project %q", number, p.Title)
}

func projectIDOf(it *projectItem) string {
	return it.Project.ID
}

func (c *client) itemForIssue(ctx context.Context, number int) (string, error) {
	p, err := c.project(ctx)
	if err != nil {
		return "", err
	}
	const q = `query($owner:String!, $repo:String!, $number:Int!) {
  repository(owner:$owner, name:$repo) {
    issue(number:$number) {
      projectItems(first:20) { nodes { id project { id } } }
    }
  }
}`
	var out struct {
		Repository struct {
			Issue *struct {
				ProjectItems struct {
					Nodes []struct {
						ID      string `json:"id"`
						Project struct {
							ID string `json:"id"`
						} `json:"project"`
					} `json:"nodes"`
				} `json:"projectItems"`
			} `json:"issue"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, q, map[string]any{
		"owner": c.cfg.Owner, "repo": repoName(c.cfg.Repo), "number": number,
	}, &out); err != nil {
		return "", err
	}
	if out.Repository.Issue == nil {
		return "", fmt.Errorf("issue #%d not found in %s", number, c.cfg.Repo)
	}
	for _, n := range out.Repository.Issue.ProjectItems.Nodes {
		if n.Project.ID == p.ID {
			return n.ID, nil
		}
	}
	return "", fmt.Errorf("issue #%d is not on project %q", number, p.Title)
}

func repoName(repo string) string {
	parts := strings.SplitN(repo, "/", 2)
	return parts[1]
}

func (c *client) viewer(ctx context.Context) (string, error) {
	var out struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}
	if err := c.graphql(ctx, `query { viewer { login } }`, nil, &out); err != nil {
		return "", err
	}
	if out.Viewer.Login == "" {
		return "", errors.New("could not resolve the authenticated GitHub user")
	}
	return out.Viewer.Login, nil
}

type restIssue struct {
	Number    int    `json:"number"`
	NodeID    string `json:"node_id"`
	HTMLURL   string `json:"html_url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (c *client) createIssue(ctx context.Context, title, body string, labels []string, assignee string) (*restIssue, error) {
	payload := map[string]any{"title": title, "body": body}
	var clean []string
	for _, l := range labels {
		if l = strings.TrimSpace(l); l != "" {
			clean = append(clean, l)
		}
	}
	if len(clean) > 0 {
		payload["labels"] = clean
	}
	if assignee != "" {
		payload["assignees"] = []string{assignee}
	}
	var out restIssue
	if err := c.rest(ctx, http.MethodPost, "/repos/"+c.cfg.Repo+"/issues", payload, &out); err != nil {
		return nil, err
	}
	if out.NodeID == "" {
		return nil, errors.New("GitHub did not return an issue node id")
	}
	return &out, nil
}

func (c *client) patchIssue(ctx context.Context, number int, payload map[string]any) error {
	return c.rest(ctx, http.MethodPatch,
		"/repos/"+c.cfg.Repo+"/issues/"+strconv.Itoa(number), payload, nil)
}

func (c *client) setIssueLabels(ctx context.Context, number int, labels []string) error {
	if labels == nil {
		labels = []string{}
	}
	return c.rest(ctx, http.MethodPut,
		"/repos/"+c.cfg.Repo+"/issues/"+strconv.Itoa(number)+"/labels",
		map[string]any{"labels": labels}, nil)
}

func (c *client) setIssueAssignees(ctx context.Context, number int, assignees []string) error {
	if assignees == nil {
		assignees = []string{}
	}
	return c.rest(ctx, http.MethodPatch,
		"/repos/"+c.cfg.Repo+"/issues/"+strconv.Itoa(number),
		map[string]any{"assignees": assignees}, nil)
}

func (c *client) ensureLabel(ctx context.Context, name string) error {
	err := c.rest(ctx, http.MethodPost, "/repos/"+c.cfg.Repo+"/labels",
		map[string]any{"name": name, "color": "ededed"}, nil)
	if err != nil && strings.Contains(err.Error(), "422") {
		return nil
	}
	return err
}

type restComment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (c *client) addComment(ctx context.Context, number int, body string) (*pb.Comment, error) {
	var out restComment
	if err := c.rest(ctx, http.MethodPost,
		"/repos/"+c.cfg.Repo+"/issues/"+strconv.Itoa(number)+"/comments",
		map[string]any{"body": body}, &out); err != nil {
		return nil, err
	}
	return &pb.Comment{
		Id: strconv.FormatInt(out.ID, 10), Author: out.User.Login, Body: out.Body,
		CreatedAt: timePtr(out.CreatedAt),
	}, nil
}

func (c *client) listComments(ctx context.Context, number int) ([]*pb.Comment, error) {
	var out []restComment
	if err := c.rest(ctx, http.MethodGet,
		"/repos/"+c.cfg.Repo+"/issues/"+strconv.Itoa(number)+"/comments?per_page=100", nil, &out); err != nil {
		return nil, err
	}
	comments := make([]*pb.Comment, 0, len(out))
	for _, rc := range out {
		comments = append(comments, &pb.Comment{
			Id: strconv.FormatInt(rc.ID, 10), Author: rc.User.Login, Body: rc.Body,
			CreatedAt: timePtr(rc.CreatedAt),
		})
	}
	return comments, nil
}

func (c *client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
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
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
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
		return fmt.Errorf("GitHub GraphQL HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, len(env.Errors))
		for i, e := range env.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("GitHub GraphQL: %s", strings.Join(msgs, "; "))
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("decode GitHub data: %w", err)
		}
	}
	return nil
}

func (c *client) rest(ctx context.Context, method, path string, payload, out any) error {
	var rdr io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	base, err := url.Parse(c.restURL)
	if err != nil {
		return err
	}
	ref, err := url.Parse(path)
	if err != nil {
		return err
	}
	u := base.ResolveReference(ref)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &msg)
		if msg.Message == "" {
			msg.Message = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("GitHub API %d: %s", resp.StatusCode, msg.Message)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode GitHub response: %w", err)
		}
	}
	return nil
}
