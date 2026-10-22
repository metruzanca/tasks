package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/metruzanca/tasks/internal/render"
	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
	"github.com/spf13/cobra"
)

func printJSON(w io.Writer, v any) error { return render.JSON(w, v) }

func (a *App) addCmd() *cobra.Command {
	var (
		desc, status, priority, assignee string
		labels                           []string
	)
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			pr, ok := sdk.ParsePriority(priority)
			if !ok {
				return fmt.Errorf("invalid priority %q", priority)
			}
			t, err := s.Backend.CreateTask(ctx, &pb.CreateTaskRequest{
				Title: strings.Join(args, " "), Description: desc, Status: status,
				Priority: pr, Labels: splitList(labels), Assignee: assignee,
			})
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), render.ConvertTask(t))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", t.Key, t.Title)
			return nil
		},
	}
	cmd.Flags().StringVarP(&desc, "description", "d", "", "task description")
	cmd.Flags().StringVarP(&status, "status", "s", "", "initial status/column")
	cmd.Flags().StringVarP(&priority, "priority", "p", "none", "priority: none, low, medium, high, urgent")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "label (repeatable)")
	cmd.Flags().StringVarP(&assignee, "assignee", "a", "", "assignee (@me for yourself)")
	return cmd
}

func (a *App) listCmd() *cobra.Command {
	var (
		statuses, labels           []string
		assignee, priority, search string
		limit                      int
		all                        bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			filter := &pb.TaskFilter{
				Statuses: splitList(statuses), Labels: splitList(labels),
				Assignee: assignee, Search: search, IncludeArchived: all,
				Limit: int32(limit),
			}
			if cmd.Flags().Changed("priority") {
				pr, ok := sdk.ParsePriority(priority)
				if !ok {
					return fmt.Errorf("invalid priority %q", priority)
				}
				filter.Priority = pr
				filter.HasPriority = true
			}
			tasks, err := s.Backend.ListTasks(ctx, filter)
			if err != nil {
				return err
			}
			info, _ := s.Backend.Describe(ctx)
			if info != nil {
				render.SortTasks(tasks, info.Columns)
			}
			if a.jsonOut {
				out := make([]render.TaskJSON, 0, len(tasks))
				for _, t := range tasks {
					out = append(out, render.ConvertTask(t))
				}
				return printJSON(cmd.OutOrStdout(), out)
			}
			render.TasksTable(cmd.OutOrStdout(), tasks, a.noColor)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&statuses, "status", nil, "filter by status (repeatable)")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "filter by label (repeatable)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee")
	cmd.Flags().StringVar(&priority, "priority", "", "filter by priority")
	cmd.Flags().StringVar(&search, "search", "", "search title and description")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum tasks")
	cmd.Flags().BoolVar(&all, "all", false, "include archived tasks")
	return cmd
}

func (a *App) showCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a task and its comments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			t, err := s.Backend.GetTask(ctx, args[0])
			if err != nil {
				return err
			}
			comments, err := s.Backend.ListComments(ctx, args[0])
			if err != nil {
				return err
			}
			if a.jsonOut {
				cj := make([]render.CommentJSON, 0, len(comments))
				for _, c := range comments {
					cj = append(cj, render.ConvertComment(c))
				}
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"task": render.ConvertTask(t), "comments": cj,
				})
			}
			render.TaskDetail(cmd.OutOrStdout(), t, comments, a.noColor)
			return nil
		},
	}
}

func (a *App) editCmd() *cobra.Command {
	var (
		title, desc, priority, assignee string
		labels                          []string
		clearAssignee                   bool
	)
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			req := &pb.UpdateTaskRequest{Id: args[0]}
			if cmd.Flags().Changed("title") {
				req.Title = &title
			}
			if cmd.Flags().Changed("description") {
				req.Description = &desc
			}
			if cmd.Flags().Changed("priority") {
				pr, ok := sdk.ParsePriority(priority)
				if !ok {
					return fmt.Errorf("invalid priority %q", priority)
				}
				req.Priority = &pr
			}
			if cmd.Flags().Changed("assignee") {
				req.Assignee = &assignee
			}
			req.ClearAssignee = clearAssignee
			if cmd.Flags().Changed("label") {
				req.SetLabels = true
				req.Labels = splitList(labels)
			}
			t, err := s.Backend.UpdateTask(ctx, req)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), render.ConvertTask(t))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated %s\n", t.Key)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVarP(&desc, "description", "d", "", "new description")
	cmd.Flags().StringVarP(&priority, "priority", "p", "", "new priority")
	cmd.Flags().StringVarP(&assignee, "assignee", "a", "", "new assignee")
	cmd.Flags().BoolVar(&clearAssignee, "clear-assignee", false, "remove the assignee")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "replace labels (repeatable)")
	return cmd
}

func (a *App) moveCmd() *cobra.Command {
	var before, after string
	cmd := &cobra.Command{
		Use:   "move <id> <status>",
		Short: "Move a task to another column",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if before != "" && after != "" {
				return errors.New("--before and --after are mutually exclusive")
			}
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			req := &pb.MoveTaskRequest{Id: args[0], Status: args[1]}
			if before != "" {
				req.BeforeId = &before
			}
			if after != "" {
				req.AfterId = &after
			}
			t, err := s.Backend.MoveTask(ctx, req)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), render.ConvertTask(t))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Moved %s to %s\n", t.Key, t.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&before, "before", "", "place before this task id")
	cmd.Flags().StringVar(&after, "after", "", "place after this task id")
	return cmd
}

func (a *App) doneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <id>",
		Short: "Move a task to the done column",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			info, err := s.Backend.Describe(ctx)
			if err != nil {
				return err
			}
			status := "done"
			for _, c := range info.Columns {
				if c.Category == pb.ColumnCategory_COLUMN_CATEGORY_DONE {
					status = c.Name
					break
				}
			}
			t, err := s.Backend.MoveTask(ctx, &pb.MoveTaskRequest{Id: args[0], Status: status})
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), render.ConvertTask(t))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Moved %s to %s\n", t.Key, t.Status)
			return nil
		},
	}
}

func (a *App) nextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "next",
		Short: "Show the task to work on next",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			info, err := s.Backend.Describe(ctx)
			if err != nil {
				return err
			}
			filter := &pb.TaskFilter{}
			if info.ReadyStatus != "" {
				filter.Statuses = []string{info.ReadyStatus}
			}
			tasks, err := s.Backend.ListTasks(ctx, filter)
			if err != nil {
				return err
			}
			t := render.Next(tasks, info.ReadyStatus)
			if t == nil {
				if a.jsonOut {
					return printJSON(cmd.OutOrStdout(), nil)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "No ready tasks.")
				return nil
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), render.ConvertTask(t))
			}
			render.TasksTable(cmd.OutOrStdout(), []*pb.Task{t}, a.noColor)
			return nil
		},
	}
}

func (a *App) commentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "comment <id> [text]",
		Short: "Add a comment to a task (reads stdin when text is omitted or '-')",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := strings.Join(args[1:], " ")
			if body == "" || body == "-" {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				body = string(data)
			}
			if strings.TrimSpace(body) == "" {
				return errors.New("comment text is required")
			}
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			c, err := s.Backend.AddComment(ctx, args[0], body)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), render.ConvertComment(c))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Commented on %s\n", args[0])
			return nil
		},
	}
}

func (a *App) commentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "comments <id>",
		Short: "List comments on a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			comments, err := s.Backend.ListComments(ctx, args[0])
			if err != nil {
				return err
			}
			if a.jsonOut {
				out := make([]render.CommentJSON, 0, len(comments))
				for _, c := range comments {
					out = append(out, render.ConvertComment(c))
				}
				return printJSON(cmd.OutOrStdout(), out)
			}
			if len(comments) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No comments.")
				return nil
			}
			for _, c := range comments {
				fmt.Fprintf(cmd.OutOrStdout(), "%s at %s\n%s\n\n",
					render.ConvertComment(c).Author, render.ConvertComment(c).CreatedAt, c.Body)
			}
			return nil
		},
	}
}

func (a *App) archiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <id>",
		Short: "Archive a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Backend.DeleteTask(ctx, args[0], false); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Archived %s\n", args[0])
			return nil
		},
	}
}

func (a *App) rmCmd() *cobra.Command {
	var hard, yes bool
	cmd := &cobra.Command{
		Use:   "rm <id>",
		Short: "Remove a task (archives unless --hard)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if hard && !yes {
				return errors.New("--hard is destructive; add --yes to confirm")
			}
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Backend.DeleteTask(ctx, args[0], hard); err != nil {
				return err
			}
			if hard {
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s\n", args[0])
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Archived %s\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&hard, "hard", false, "permanently delete")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm destructive action")
	return cmd
}

func (a *App) columnsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "columns",
		Short: "List board columns",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer s.Close()
			info, err := s.Backend.Describe(ctx)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), info)
			}
			render.ColumnsTable(cmd.OutOrStdout(), info.Columns, info.ReadyStatus)
			return nil
		},
	}
}

func splitList(in []string) []string {
	var out []string
	for _, v := range in {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}
