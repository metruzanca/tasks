// Package sqlite implements the default local tasks backend.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

// Config is the [sqlite] section of the config file.
type Config struct {
	Path        string `json:"path"`
	Me          string `json:"me"`
	ReadyStatus string `json:"ready_status"`
}

// Backend is the SQLite task store.
type Backend struct {
	db  *sql.DB
	cfg Config
}

var defaultColumns = []struct {
	Name     string
	Position float64
	Category pb.ColumnCategory
}{
	{"backlog", 0, pb.ColumnCategory_COLUMN_CATEGORY_OTHER},
	{"todo", 1024, pb.ColumnCategory_COLUMN_CATEGORY_TODO},
	{"in_progress", 2048, pb.ColumnCategory_COLUMN_CATEGORY_IN_PROGRESS},
	{"in_review", 3072, pb.ColumnCategory_COLUMN_CATEGORY_IN_PROGRESS},
	{"done", 4096, pb.ColumnCategory_COLUMN_CATEGORY_DONE},
}

// NewFromEnv creates a backend from the JSON config passed by the host.
func NewFromEnv() (*Backend, error) {
	var cfg Config
	raw := os.Getenv(sdk.PluginConfigEnv)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, fmt.Errorf("parse sqlite config: %w", err)
		}
	}
	if cfg.Path == "" {
		return nil, errors.New("sqlite path is not configured")
	}
	if cfg.Me == "" {
		cfg.Me = os.Getenv("USER")
	}
	return New(cfg)
}

// New opens and migrates a SQLite database.
func New(cfg Config) (*Backend, error) {
	cfg.Path = expandHome(cfg.Path)
	if cfg.Path == "" {
		return nil, errors.New("sqlite path is empty")
	}
	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// Board data lives in .tasks/ inside a repository. Keep it out of git
	// without requiring changes to the repository's own .gitignore.
	if filepath.Base(dir) == ".tasks" {
		ignore := filepath.Join(dir, ".gitignore")
		if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
			_ = os.WriteFile(ignore, []byte("*\n"), 0o644)
		}
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	dsn := "file:" + filepath.ToSlash(cfg.Path) + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	b := &Backend{db: db, cfg: cfg}
	if err := b.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return b, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}

func (b *Backend) migrate(ctx context.Context) error {
	var version int
	if err := b.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= 1 {
		return nil
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS columns (
  name TEXT PRIMARY KEY,
  position REAL NOT NULL,
  category INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL REFERENCES columns(name),
  priority INTEGER NOT NULL DEFAULT 1,
  position REAL NOT NULL,
  assignee TEXT NOT NULL DEFAULT '',
  url TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  archived_at TEXT
);
CREATE INDEX IF NOT EXISTS tasks_status_position ON tasks(status, position);
CREATE TABLE IF NOT EXISTS labels (
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  PRIMARY KEY (task_id, label)
);
CREATE INDEX IF NOT EXISTS labels_label ON labels(label);
CREATE TABLE IF NOT EXISTS comments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  author TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS comments_task ON comments(task_id);
`)
	if err != nil {
		return err
	}
	for _, c := range defaultColumns {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO columns(name, position, category) VALUES(?,?,?)`,
			c.Name, c.Position, int(c.Category)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		return err
	}
	return tx.Commit()
}

func (b *Backend) Close() error { return b.db.Close() }

func (b *Backend) Describe(ctx context.Context) (*pb.BackendInfo, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT name, position, category FROM columns ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []*pb.Column
	for rows.Next() {
		var name string
		var pos float64
		var cat int
		if err := rows.Scan(&name, &pos, &cat); err != nil {
			return nil, err
		}
		cols = append(cols, &pb.Column{
			Id: name, Name: name, Position: pos,
			Category: pb.ColumnCategory(cat),
		})
	}
	ready, _ := b.readyStatus(ctx)
	return &pb.BackendInfo{
		Name: "sqlite", DisplayName: "SQLite", Version: "built-in",
		ProtocolVersion: sdk.ProtocolVersion,
		BoardName:       filepath.Base(b.cfg.Path),
		Columns:         cols,
		ReadyStatus:     ready,
		Capabilities: &pb.Capabilities{
			Comments: true, Labels: true, Assignee: true,
			Ordering: true, Archive: true, Priority: true, Search: true,
		},
	}, nil
}

func (b *Backend) Setup(ctx context.Context, createMissing bool) ([]string, error) {
	return nil, nil
}

func (b *Backend) readyStatus(ctx context.Context) (string, error) {
	cols, err := b.columnNames(ctx)
	if err != nil {
		return "", err
	}
	if b.cfg.ReadyStatus != "" {
		for _, c := range cols {
			if strings.EqualFold(c.Name, b.cfg.ReadyStatus) {
				return c.Name, nil
			}
		}
	}
	for _, c := range cols {
		if c.Name == "todo" {
			return c.Name, nil
		}
	}
	for _, c := range cols {
		if c.Category == pb.ColumnCategory_COLUMN_CATEGORY_TODO {
			return c.Name, nil
		}
	}
	if len(cols) > 0 {
		return cols[0].Name, nil
	}
	return "", errors.New("no columns configured")
}

type column struct {
	Name     string
	Position float64
	Category pb.ColumnCategory
}

func (b *Backend) columnNames(ctx context.Context) ([]column, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT name, position, category FROM columns ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []column
	for rows.Next() {
		var c column
		var cat int
		if err := rows.Scan(&c.Name, &c.Position, &cat); err != nil {
			return nil, err
		}
		c.Category = pb.ColumnCategory(cat)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *Backend) resolveStatus(ctx context.Context, status string) (string, error) {
	cols, err := b.columnNames(ctx)
	if err != nil {
		return "", err
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return b.readyStatus(ctx)
	}
	for _, c := range cols {
		if strings.EqualFold(c.Name, status) || strings.EqualFold(strings.ReplaceAll(c.Name, "_", " "), strings.ReplaceAll(status, "_", " ")) {
			return c.Name, nil
		}
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return "", fmt.Errorf("unknown status %q (columns: %s)", status, strings.Join(names, ", "))
}

func parseID(id string) (int64, error) {
	s := strings.TrimSpace(id)
	s = strings.TrimPrefix(s, "#")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "T-"), "t-")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unknown task %q", id)
	}
	return n, nil
}

func key(n int64) string { return fmt.Sprintf("T-%d", n) }

func (b *Backend) ListTasks(ctx context.Context, f *pb.TaskFilter) ([]*pb.Task, error) {
	if f == nil {
		f = &pb.TaskFilter{}
	}
	var where []string
	var args []any
	if !f.IncludeArchived {
		where = append(where, "t.archived_at IS NULL")
	}
	if len(f.Statuses) > 0 {
		phs := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			phs[i] = "?"
			args = append(args, strings.ToLower(s))
		}
		where = append(where, "lower(t.status) IN ("+strings.ToLower(strings.Join(phs, ","))+")")
	}
	for _, l := range f.Labels {
		where = append(where, "EXISTS (SELECT 1 FROM labels l WHERE l.task_id=t.id AND lower(l.label)=lower(?))")
		args = append(args, l)
	}
	if f.Assignee != "" {
		where = append(where, "lower(t.assignee)=lower(?)")
		args = append(args, f.Assignee)
	}
	if f.HasPriority {
		where = append(where, "t.priority=?")
		args = append(args, int(f.Priority))
	}
	if f.Search != "" {
		where = append(where, "(lower(t.title) LIKE lower(?) OR lower(t.description) LIKE lower(?))")
		like := "%" + f.Search + "%"
		args = append(args, like, like)
	}
	q := `SELECT t.id, t.title, t.description, t.status, t.priority, t.position,
		t.assignee, t.url, t.created_at, t.updated_at, t.archived_at,
		(SELECT COUNT(*) FROM comments c WHERE c.task_id=t.id)
		FROM tasks t`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += ` ORDER BY (SELECT c.position FROM columns c WHERE c.name=t.status), t.position, t.id`
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
	}
	rows, err := b.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*pb.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if err := b.loadLabels(ctx, t); err != nil {
			return nil, err
		}
	}
	return tasks, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanTask(row scannable) (*pb.Task, error) {
	var (
		id, priority, count int64
		title, desc, status string
		pos                 float64
		assignee, url       string
		created, updated    string
		archived            sql.NullString
	)
	if err := row.Scan(&id, &title, &desc, &status, &priority, &pos,
		&assignee, &url, &created, &updated, &archived, &count); err != nil {
		return nil, err
	}
	t := &pb.Task{
		Id: key(id), Key: key(id), Title: title, Description: desc,
		Status: status, Position: pos, Priority: pb.Priority(priority),
		Assignee: assignee, Url: url, CommentCount: int32(count),
		Archived: archived.Valid,
	}
	t.CreatedAt = parseTime(created)
	t.UpdatedAt = parseTime(updated)
	return t, nil
}

func parseTime(s string) *timestamppb.Timestamp {
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return timestamppb.New(tm)
}

func (b *Backend) loadLabels(ctx context.Context, t *pb.Task) error {
	rows, err := b.db.QueryContext(ctx, `SELECT label FROM labels WHERE task_id=? ORDER BY label`, mustID(t.Id))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return err
		}
		t.Labels = append(t.Labels, l)
	}
	return rows.Err()
}

func mustID(s string) int64 {
	n, _ := parseID(s)
	return n
}

func (b *Backend) GetTask(ctx context.Context, id string) (*pb.Task, error) {
	n, err := parseID(id)
	if err != nil {
		return nil, err
	}
	row := b.db.QueryRowContext(ctx, `SELECT t.id, t.title, t.description, t.status, t.priority, t.position,
		t.assignee, t.url, t.created_at, t.updated_at, t.archived_at,
		(SELECT COUNT(*) FROM comments c WHERE c.task_id=t.id)
		FROM tasks t WHERE t.id=?`, n)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	if err := b.loadLabels(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (b *Backend) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.Task, error) {
	title := strings.TrimSpace(req.GetTitle())
	if title == "" {
		return nil, errors.New("title is required")
	}
	status, err := b.resolveStatus(ctx, req.GetStatus())
	if err != nil {
		return nil, err
	}
	priority := req.GetPriority()
	if priority == pb.Priority_PRIORITY_UNSPECIFIED {
		priority = pb.Priority_PRIORITY_NONE
	}
	assignee := b.resolveAssignee(req.GetAssignee())
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var pos float64
	if err := b.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position),0)+1024 FROM tasks WHERE status=?`, status).Scan(&pos); err != nil {
		return nil, err
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO tasks(title,description,status,priority,position,assignee,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		title, req.GetDescription(), status, int(priority), pos, assignee, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := replaceLabels(ctx, tx, id, req.GetLabels()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.GetTask(ctx, key(id))
}

func (b *Backend) resolveAssignee(a string) string {
	if strings.EqualFold(a, "@me") || strings.EqualFold(a, "me") {
		return b.cfg.Me
	}
	return a
}

func replaceLabels(ctx context.Context, tx *sql.Tx, id int64, labels []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM labels WHERE task_id=?`, id); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" || seen[strings.ToLower(l)] {
			continue
		}
		seen[strings.ToLower(l)] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO labels(task_id,label) VALUES(?,?)`, id, l); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) UpdateTask(ctx context.Context, req *pb.UpdateTaskRequest) (*pb.Task, error) {
	n, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	if _, err := b.GetTask(ctx, req.GetId()); err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return nil, errors.New("title cannot be empty")
		}
		sets = append(sets, "title=?")
		args = append(args, title)
	}
	if req.Description != nil {
		sets = append(sets, "description=?")
		args = append(args, *req.Description)
	}
	if req.Priority != nil {
		sets = append(sets, "priority=?")
		args = append(args, int(*req.Priority))
	}
	if req.ClearAssignee {
		sets = append(sets, "assignee=''")
	} else if req.Assignee != nil {
		sets = append(sets, "assignee=?")
		args = append(args, b.resolveAssignee(*req.Assignee))
	}
	sets = append(sets, "updated_at=?")
	args = append(args, time.Now().UTC().Format(time.RFC3339Nano))
	args = append(args, n)
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...); err != nil {
		return nil, err
	}
	if req.SetLabels {
		if err := replaceLabels(ctx, tx, n, req.Labels); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.GetTask(ctx, req.GetId())
}

func (b *Backend) MoveTask(ctx context.Context, req *pb.MoveTaskRequest) (*pb.Task, error) {
	n, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	if _, err := b.GetTask(ctx, req.GetId()); err != nil {
		return nil, err
	}
	status, err := b.resolveStatus(ctx, req.GetStatus())
	if err != nil {
		return nil, err
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE status=? AND id<>? ORDER BY position, id`, status, n)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	insertAt := len(ids)
	targetID := int64(0)
	if req.AfterId != nil && *req.AfterId != "" {
		targetID, err = parseID(*req.AfterId)
		if err != nil {
			return nil, err
		}
		insertAt = -1
	} else if req.BeforeId != nil && *req.BeforeId != "" {
		targetID, err = parseID(*req.BeforeId)
		if err != nil {
			return nil, err
		}
		insertAt = -1
	}
	if insertAt == -1 {
		pos := -1
		for i, id := range ids {
			if id == targetID {
				pos = i
				break
			}
		}
		if pos < 0 {
			return nil, fmt.Errorf("task %s is not in status %q", strconv.FormatInt(targetID, 10), status)
		}
		if req.BeforeId != nil && *req.BeforeId != "" {
			insertAt = pos
		} else {
			insertAt = pos + 1
		}
	}
	ordered := make([]int64, 0, len(ids)+1)
	ordered = append(ordered, ids[:insertAt]...)
	ordered = append(ordered, n)
	ordered = append(ordered, ids[insertAt:]...)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status=?, updated_at=? WHERE id=?`, status, now, n); err != nil {
		return nil, err
	}
	for i, id := range ordered {
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET position=? WHERE id=?`, float64(i+1)*1024, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.GetTask(ctx, req.GetId())
}

func (b *Backend) DeleteTask(ctx context.Context, id string, hard bool) error {
	n, err := parseID(id)
	if err != nil {
		return err
	}
	if _, err := b.GetTask(ctx, id); err != nil {
		return err
	}
	if hard {
		_, err = b.db.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, n)
		return err
	}
	_, err = b.db.ExecContext(ctx, `UPDATE tasks SET archived_at=?, updated_at=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), n)
	return err
}

func (b *Backend) AddComment(ctx context.Context, taskID, body string) (*pb.Comment, error) {
	n, err := parseID(taskID)
	if err != nil {
		return nil, err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("comment body is required")
	}
	if _, err := b.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	author := b.cfg.Me
	if author == "" {
		author = "unknown"
	}
	now := time.Now().UTC()
	res, err := b.db.ExecContext(ctx, `INSERT INTO comments(task_id,author,body,created_at) VALUES(?,?,?,?)`,
		n, author, body, now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &pb.Comment{
		Id: strconv.FormatInt(id, 10), Author: author, Body: body,
		CreatedAt: timestamppb.New(now),
	}, nil
}

func (b *Backend) ListComments(ctx context.Context, taskID string) ([]*pb.Comment, error) {
	n, err := parseID(taskID)
	if err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx, `SELECT id, author, body, created_at FROM comments WHERE task_id=? ORDER BY id`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*pb.Comment
	for rows.Next() {
		var id int64
		var author, body, created string
		if err := rows.Scan(&id, &author, &body, &created); err != nil {
			return nil, err
		}
		out = append(out, &pb.Comment{
			Id: strconv.FormatInt(id, 10), Author: author, Body: body,
			CreatedAt: parseTime(created),
		})
	}
	return out, rows.Err()
}

func (b *Backend) Health(ctx context.Context) error {
	return b.db.PingContext(ctx)
}

// SortTasks orders tasks for board display.
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
