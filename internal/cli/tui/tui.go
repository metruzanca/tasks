// Package tui implements the interactive kanban board.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/metruzanca/tasks/internal/render"
	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeNew
	modeEdit
	modeDetail
	modeConfirmArchive
)

type loadedMsg struct {
	tasks   []*pb.Task
	info    *pb.BackendInfo
	message string
	err     error
}

// Model is the board state.
type Model struct {
	ctx     context.Context
	backend sdk.Backend
	info    *pb.BackendInfo
	tasks   []*pb.Task

	col, row int
	filter   string
	mode     mode
	message  string
	err      error
	width    int
	height   int

	input    textinput.Model
	textarea textarea.Model
}

// Run starts the board.
func Run(ctx context.Context, backend sdk.Backend) error {
	m := &Model{ctx: ctx, backend: backend, width: 120, height: 40}
	m.textarea = textarea.New()
	m.input = textinput.New()
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	return m.load("")
}

func (m *Model) load(message string) tea.Cmd {
	return func() tea.Msg {
		info, err := m.backend.Describe(m.ctx)
		if err != nil {
			return loadedMsg{err: err}
		}
		tasks, err := m.backend.ListTasks(m.ctx, &pb.TaskFilter{})
		if err != nil {
			return loadedMsg{err: err}
		}
		return loadedMsg{tasks: tasks, info: info, message: message}
	}
}

func (m *Model) action(fn func() error, message string) tea.Cmd {
	return func() tea.Msg {
		if err := fn(); err != nil {
			return loadedMsg{err: err}
		}
		return m.load(message)()
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.textarea.SetWidth(max(40, msg.Width-12))
		m.textarea.SetHeight(max(6, msg.Height/3))
		return m, nil
	case loadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		if msg.info != nil {
			m.info = msg.info
		}
		if msg.tasks != nil {
			m.tasks = msg.tasks
		}
		m.message = msg.message
		m.clamp()
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeFilter:
			return m.updateFilter(msg)
		case modeNew:
			return m.updateNew(msg)
		case modeEdit:
			return m.updateEdit(msg)
		case modeDetail:
			if msg.String() == "esc" || msg.String() == "enter" || msg.String() == "q" {
				m.mode = modeNormal
			}
			return m, nil
		case modeConfirmArchive:
			switch msg.String() {
			case "y":
				t := m.selected()
				if t == nil {
					m.mode = modeNormal
					return m, nil
				}
				m.mode = modeNormal
				return m, m.action(func() error {
					return m.backend.DeleteTask(m.ctx, t.Id, false)
				}, "Archived "+t.Key)
			case "n", "esc":
				m.mode = modeNormal
				return m, nil
			}
			return m, nil
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m *Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "r":
		return m, m.load("Refreshed")
	case "left", "h":
		if m.col > 0 {
			m.col--
			m.row = 0
			m.clamp()
		}
	case "right", "l":
		if m.info != nil && m.col < len(m.info.Columns)-1 {
			m.col++
			m.row = 0
			m.clamp()
		}
	case "up", "k":
		if m.row > 0 {
			m.row--
		}
	case "down", "j":
		if m.row < len(m.columnTasks())-1 {
			m.row++
		}
	case "H":
		return m, m.moveColumn(-1)
	case "L":
		return m, m.moveColumn(1)
	case "K":
		return m, m.reorder(-1)
	case "J":
		return m, m.reorder(1)
	case "n":
		m.input = textinput.New()
		m.input.Placeholder = "Task title"
		m.input.Focus()
		m.mode = modeNew
		return m, textinput.Blink
	case "e":
		t := m.selected()
		if t == nil {
			return m, nil
		}
		m.textarea = textarea.New()
		m.textarea.SetValue(t.Description)
		m.textarea.SetWidth(max(40, m.width-12))
		m.textarea.SetHeight(max(6, m.height/3))
		m.textarea.Focus()
		m.mode = modeEdit
		return m, textarea.Blink
	case "enter":
		if m.selected() != nil {
			m.mode = modeDetail
		}
	case "/":
		m.input = textinput.New()
		m.input.Placeholder = "Filter"
		m.input.SetValue(m.filter)
		m.input.Focus()
		m.mode = modeFilter
		return m, textinput.Blink
	case "d":
		t := m.selected()
		if t == nil || m.info == nil {
			return m, nil
		}
		status := "done"
		for _, c := range m.info.Columns {
			if c.Category == pb.ColumnCategory_COLUMN_CATEGORY_DONE {
				status = c.Name
				break
			}
		}
		return m, m.action(func() error {
			_, err := m.backend.MoveTask(m.ctx, &pb.MoveTaskRequest{Id: t.Id, Status: status})
			return err
		}, "Moved "+t.Key+" to "+status)
	case "a":
		if m.selected() != nil {
			m.mode = modeConfirmArchive
		}
	}
	return m, nil
}

func (m *Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "enter":
		m.filter = m.input.Value()
		m.mode = modeNormal
		m.row = 0
		m.clamp()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) updateNew(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "enter":
		title := strings.TrimSpace(m.input.Value())
		if title == "" {
			return m, nil
		}
		status := ""
		if m.info != nil && m.col < len(m.info.Columns) {
			status = m.info.Columns[m.col].Name
		}
		m.mode = modeNormal
		return m, m.action(func() error {
			_, err := m.backend.CreateTask(m.ctx, &pb.CreateTaskRequest{Title: title, Status: status})
			return err
		}, "Created "+title)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.mode = modeNormal
		return m, nil
	}
	if msg.String() == "ctrl+s" || msg.String() == "ctrl+enter" {
		t := m.selected()
		if t == nil {
			m.mode = modeNormal
			return m, nil
		}
		desc := m.textarea.Value()
		m.mode = modeNormal
		return m, m.action(func() error {
			_, err := m.backend.UpdateTask(m.ctx, &pb.UpdateTaskRequest{Id: t.Id, Description: &desc})
			return err
		}, "Updated "+t.Key)
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m *Model) moveColumn(delta int) tea.Cmd {
	t := m.selected()
	if t == nil || m.info == nil {
		return nil
	}
	target := m.col + delta
	if target < 0 || target >= len(m.info.Columns) {
		return nil
	}
	status := m.info.Columns[target].Name
	if strings.EqualFold(status, t.Status) {
		return nil
	}
	return m.action(func() error {
		_, err := m.backend.MoveTask(m.ctx, &pb.MoveTaskRequest{Id: t.Id, Status: status})
		return err
	}, "Moved "+t.Key+" to "+status)
}

func (m *Model) reorder(delta int) tea.Cmd {
	t := m.selected()
	if t == nil || m.info == nil || !m.info.Capabilities.GetOrdering() {
		return nil
	}
	tasks := m.columnTasks()
	if m.row+delta < 0 || m.row+delta >= len(tasks) {
		return nil
	}
	neighbor := tasks[m.row+delta]
	req := &pb.MoveTaskRequest{Id: t.Id, Status: t.Status}
	if delta < 0 {
		req.BeforeId = &neighbor.Id
	} else {
		req.AfterId = &neighbor.Id
	}
	return m.action(func() error {
		_, err := m.backend.MoveTask(m.ctx, req)
		return err
	}, "Reordered "+t.Key)
}

func (m *Model) selected() *pb.Task {
	tasks := m.columnTasks()
	if m.row < 0 || m.row >= len(tasks) {
		return nil
	}
	return tasks[m.row]
}

func (m *Model) columnTasks() []*pb.Task {
	if m.info == nil || m.col < 0 || m.col >= len(m.info.Columns) {
		return nil
	}
	name := m.info.Columns[m.col].Name
	var out []*pb.Task
	for _, t := range m.tasks {
		if t.Archived || !strings.EqualFold(t.Status, name) {
			continue
		}
		if m.filter != "" && !matches(t, m.filter) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func matches(t *pb.Task, f string) bool {
	f = strings.ToLower(f)
	if strings.Contains(strings.ToLower(t.Title), f) || strings.Contains(strings.ToLower(t.Description), f) {
		return true
	}
	for _, l := range t.Labels {
		if strings.Contains(strings.ToLower(l), f) {
			return true
		}
	}
	return false
}

func (m *Model) clamp() {
	if m.info == nil {
		return
	}
	if m.col >= len(m.info.Columns) {
		m.col = max(0, len(m.info.Columns)-1)
	}
	n := len(m.columnTasks())
	if m.row >= n {
		m.row = max(0, n-1)
	}
}

func (m *Model) View() string {
	if m.info == nil {
		if m.err != nil {
			return "tasks: " + m.err.Error() + "\n"
		}
		return "Loading board..."
	}
	header := lipgloss.NewStyle().Bold(true).Render(m.info.BoardName)
	meta := fmt.Sprintf("  %s backend", m.info.Name)
	if m.filter != "" {
		meta += "  filter: " + m.filter
	}
	top := header + meta
	if m.message != "" {
		top += "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(m.message)
	}
	if m.err != nil {
		top += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("error: "+m.err.Error())
	}

	cols := m.renderColumns()
	footer := "j/k move  h/l column  H/L change status  J/K reorder  n new  e edit  enter detail  d done  a archive  / filter  r refresh  q quit"
	if m.info != nil && !m.info.Capabilities.GetOrdering() {
		footer = strings.ReplaceAll(footer, "J/K reorder  ", "")
	}
	help := lipgloss.NewStyle().Faint(true).Render(footer)

	body := lipgloss.JoinVertical(lipgloss.Left, top, "", cols)
	switch m.mode {
	case modeFilter:
		return m.overlay("Filter", m.input.View())
	case modeNew:
		return m.overlay("New task in "+m.info.Columns[m.col].Name, m.input.View()+"\n\nenter create  esc cancel")
	case modeEdit:
		return m.overlay("Edit description", m.textarea.View()+"\n\nctrl+s save  esc cancel")
	case modeDetail:
		if t := m.selected(); t != nil {
			return m.overlay(t.Key+" "+t.Title, detail(t))
		}
	case modeConfirmArchive:
		if t := m.selected(); t != nil {
			return m.overlay("Archive "+t.Key+"?", "y archive  n cancel")
		}
	}
	return body + "\n\n" + help
}

func detail(t *pb.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status:   %s\n", t.Status)
	fmt.Fprintf(&b, "priority: %s\n", sdk.PriorityLabel(t.Priority))
	if t.Assignee != "" {
		fmt.Fprintf(&b, "assignee: %s\n", t.Assignee)
	}
	if len(t.Labels) > 0 {
		fmt.Fprintf(&b, "labels:   %s\n", strings.Join(t.Labels, ", "))
	}
	if t.Url != "" {
		fmt.Fprintf(&b, "url:      %s\n", t.Url)
	}
	if t.Description != "" {
		fmt.Fprintf(&b, "\n%s", t.Description)
	}
	b.WriteString("\n\nesc close")
	return b.String()
}

func (m *Model) overlay(title, body string) string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Padding(1, 2).
		Width(min(max(50, m.width/2), m.width-4))
	content := lipgloss.NewStyle().Bold(true).Render(title) + "\n\n" + body
	return lipgloss.Place(m.width, m.height-4, lipgloss.Center, lipgloss.Center, style.Render(content))
}

func (m *Model) renderColumns() string {
	n := len(m.info.Columns)
	if n == 0 {
		return ""
	}
	width := (m.width - (n - 1)) / n
	if width < 16 {
		width = 16
	}
	views := make([]string, 0, n)
	for i, c := range m.info.Columns {
		views = append(views, m.renderColumn(i, c, width))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, views...)
}

func (m *Model) renderColumn(idx int, c *pb.Column, width int) string {
	tasks := m.tasksFor(c.Name)
	active := idx == m.col
	border := lipgloss.RoundedBorder()
	color := "8"
	if active {
		color = "6"
	}
	header := lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%s (%d)", c.Name, len(tasks)))
	var cards []string
	for i, t := range tasks {
		cards = append(cards, m.renderCard(t, i == m.row && active, width-2))
	}
	if len(cards) == 0 {
		cards = append(cards, lipgloss.NewStyle().Faint(true).Render("empty"))
	}
	box := lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(color)).
		Width(width-2).
		Padding(0, 1).
		Render(header + "\n\n" + strings.Join(cards, "\n\n"))
	return box + " "
}

func (m *Model) tasksFor(status string) []*pb.Task {
	var out []*pb.Task
	for _, t := range m.tasks {
		if t.Archived || !strings.EqualFold(t.Status, status) {
			continue
		}
		if m.filter != "" && !matches(t, m.filter) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (m *Model) renderCard(t *pb.Task, selected bool, width int) string {
	pr := render.ConvertTask(t).Priority
	title := t.Title
	if len([]rune(title)) > width-2 {
		title = string([]rune(title)[:width-5]) + "..."
	}
	key := lipgloss.NewStyle().Faint(true).Render(t.Key)
	prColor := "8"
	switch t.Priority {
	case pb.Priority_PRIORITY_URGENT:
		prColor = "1"
	case pb.Priority_PRIORITY_HIGH:
		prColor = "3"
	case pb.Priority_PRIORITY_MEDIUM:
		prColor = "6"
	case pb.Priority_PRIORITY_LOW:
		prColor = "2"
	}
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color(prColor)).Render(pr)
	if len(t.Labels) > 0 {
		meta += " " + lipgloss.NewStyle().Faint(true).Render(strings.Join(t.Labels, ","))
	}
	if t.Assignee != "" {
		meta += " @" + t.Assignee
	}
	style := lipgloss.NewStyle()
	if selected {
		style = style.Bold(true).Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("6")).PaddingLeft(1)
	}
	return style.Render(key + " " + title + "\n" + meta)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
