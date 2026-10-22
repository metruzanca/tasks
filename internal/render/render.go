package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	red    = "\x1b[31m"
	cyan   = "\x1b[36m"
)

// TaskJSON is the stable JSON shape used for agents.
type TaskJSON struct {
	ID           string   `json:"id"`
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Status       string   `json:"status"`
	Priority     string   `json:"priority"`
	Labels       []string `json:"labels"`
	Assignee     string   `json:"assignee"`
	URL          string   `json:"url,omitempty"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	Archived     bool     `json:"archived"`
	CommentCount int32    `json:"comment_count"`
}

// CommentJSON is the stable JSON shape for comments.
type CommentJSON struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// ConvertTask turns a protobuf task into its JSON shape.
func ConvertTask(t *pb.Task) TaskJSON {
	labels := t.GetLabels()
	if labels == nil {
		labels = []string{}
	}
	return TaskJSON{
		ID: t.GetId(), Key: t.GetKey(), Title: t.GetTitle(),
		Description: t.GetDescription(), Status: t.GetStatus(),
		Priority: sdk.PriorityLabel(t.GetPriority()), Labels: labels,
		Assignee: t.GetAssignee(), URL: t.GetUrl(),
		CreatedAt: formatTime(t.GetCreatedAt()), UpdatedAt: formatTime(t.GetUpdatedAt()),
		Archived: t.GetArchived(), CommentCount: t.GetCommentCount(),
	}
}

// ConvertComment turns a protobuf comment into its JSON shape.
func ConvertComment(c *pb.Comment) CommentJSON {
	return CommentJSON{
		ID: c.GetId(), Author: c.GetAuthor(), Body: c.GetBody(),
		CreatedAt: formatTime(c.GetCreatedAt()),
	}
}

func formatTime(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	t := ts.AsTime()
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// JSON writes indented JSON.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// SortTasks orders tasks by column order, then position, then priority.
func SortTasks(tasks []*pb.Task, columns []*pb.Column) {
	order := map[string]int{}
	for i, c := range columns {
		order[c.Name] = i
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		oi, oj := order[tasks[i].Status], order[tasks[j].Status]
		if oi != oj {
			return oi < oj
		}
		if tasks[i].Position != tasks[j].Position {
			return tasks[i].Position < tasks[j].Position
		}
		return tasks[i].Id < tasks[j].Id
	})
}

// Next returns the most important task in a ready column.
func Next(tasks []*pb.Task, ready string) *pb.Task {
	var candidates []*pb.Task
	for _, t := range tasks {
		if t.Archived {
			continue
		}
		if ready == "" || strings.EqualFold(t.Status, ready) {
			candidates = append(candidates, t)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		ri, rj := sdk.PriorityRank(candidates[i].Priority), sdk.PriorityRank(candidates[j].Priority)
		if ri != rj {
			return ri < rj
		}
		return candidates[i].Position < candidates[j].Position
	})
	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

// TasksTable writes a compact task table.
func TasksTable(w io.Writer, tasks []*pb.Task, noColor bool) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "No tasks.")
		return
	}
	type row struct {
		id, status, priority, title, labels, assignee string
		p                                             pb.Priority
	}
	rows := []row{{id: "ID", status: "STATUS", priority: "PRIORITY", title: "TITLE", labels: "LABELS", assignee: "ASSIGNEE"}}
	for _, t := range tasks {
		title := t.Title
		if t.Archived {
			title += " [archived]"
		}
		rows = append(rows, row{
			id: t.Key, status: t.Status, priority: sdk.PriorityLabel(t.Priority),
			title: title, labels: strings.Join(t.Labels, ","), assignee: t.Assignee,
			p: t.Priority,
		})
	}
	widths := [6]int{}
	for _, r := range rows {
		vals := []string{r.id, r.status, r.priority, r.title, r.labels, r.assignee}
		for i, v := range vals {
			if len(v) > widths[i] {
				widths[i] = len(v)
			}
		}
	}
	for i, r := range rows {
		id := pad(r.id, widths[0])
		status := pad(r.status, widths[1])
		pr := pad(r.priority, widths[2])
		if i > 0 {
			pr = priorityText(r.p, noColor) + strings.Repeat(" ", widths[2]-len(r.priority))
		}
		fmt.Fprintf(w, "%s  %s  %s  %s  %s  %s\n",
			id, status, pr, pad(r.title, widths[3]), pad(r.labels, widths[4]), r.assignee)
	}
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// TaskDetail writes a task with its comments.
func TaskDetail(w io.Writer, t *pb.Task, comments []*pb.Comment, noColor bool) {
	if !noColor {
		fmt.Fprintf(w, "%s%s%s %s\n", bold, t.Key, reset, t.Title)
	} else {
		fmt.Fprintf(w, "%s %s\n", t.Key, t.Title)
	}
	fmt.Fprintf(w, "Status:   %s\n", t.Status)
	fmt.Fprintf(w, "Priority: %s\n", priorityText(t.Priority, noColor))
	if t.Assignee != "" {
		fmt.Fprintf(w, "Assignee: %s\n", t.Assignee)
	}
	if len(t.Labels) > 0 {
		fmt.Fprintf(w, "Labels:   %s\n", strings.Join(t.Labels, ", "))
	}
	if t.Url != "" {
		fmt.Fprintf(w, "URL:      %s\n", t.Url)
	}
	fmt.Fprintf(w, "Created:  %s\n", formatTime(t.CreatedAt))
	fmt.Fprintf(w, "Updated:  %s\n", formatTime(t.UpdatedAt))
	if t.Archived {
		fmt.Fprintln(w, "Archived: yes")
	}
	if t.Description != "" {
		fmt.Fprintf(w, "\n%s\n", t.Description)
	}
	if len(comments) > 0 {
		if noColor {
			fmt.Fprintln(w, "\nComments")
		} else {
			fmt.Fprintf(w, "\n%sComments%s\n", bold, reset)
		}
		for _, c := range comments {
			if noColor {
				fmt.Fprintf(w, "\n%s %s\n%s\n", c.Author, formatTime(c.CreatedAt), c.Body)
			} else {
				fmt.Fprintf(w, "\n%s%s%s %s\n%s\n", dim, c.Author, reset, formatTime(c.CreatedAt), c.Body)
			}
		}
	}
}

// ColumnsTable writes the board columns.
func ColumnsTable(w io.Writer, cols []*pb.Column, ready string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tCATEGORY\tREADY")
	for _, c := range cols {
		readyMark := ""
		if strings.EqualFold(c.Name, ready) {
			readyMark = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, sdk.CategoryLabel(c.Category), readyMark)
	}
	tw.Flush()
}

func priorityText(p pb.Priority, noColor bool) string {
	s := sdk.PriorityLabel(p)
	if noColor {
		return s
	}
	switch p {
	case pb.Priority_PRIORITY_URGENT:
		return red + s + reset
	case pb.Priority_PRIORITY_HIGH:
		return yellow + s + reset
	case pb.Priority_PRIORITY_MEDIUM:
		return cyan + s + reset
	case pb.Priority_PRIORITY_LOW:
		return green + s + reset
	default:
		return dim + s + reset
	}
}

// StatusColor returns an ANSI color for a column category.
func StatusColor(c pb.ColumnCategory) string {
	switch c {
	case pb.ColumnCategory_COLUMN_CATEGORY_TODO:
		return cyan
	case pb.ColumnCategory_COLUMN_CATEGORY_IN_PROGRESS:
		return yellow
	case pb.ColumnCategory_COLUMN_CATEGORY_DONE:
		return green
	default:
		return ""
	}
}

// Colorize wraps text in a color when color is enabled.
func Colorize(s, color string, noColor bool) string {
	if noColor || color == "" {
		return s
	}
	return color + s + reset
}
