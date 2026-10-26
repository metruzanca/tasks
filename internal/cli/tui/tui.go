// Package tui implements the interactive kanban board.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
)

type mode int

const (
	modeNormal mode = iota
	modeFilter
	modeNew
	modeEdit
)

type loadedMsg struct {
	tasks   []*pb.Task
	info    *pb.BackendInfo
	message string
	focusID string
	err     error
	tracked bool
}

const moveDebounce = 250 * time.Millisecond

type pendingMove struct {
	desired           *pb.MoveTaskRequest
	desiredPosition   float64
	sent              *pb.MoveTaskRequest
	sentPosition      float64
	inFlight          bool
	confirmedStatus   string
	confirmedPosition float64
}

type moveFlushMsg struct {
	seq int
}

type moveResultMsg struct {
	taskID  string
	request *pb.MoveTaskRequest
	err     error
}

type hitRect struct {
	x, y, w, h int
}

func (r hitRect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

type cardHit struct {
	taskID  string
	taskKey string
	col     int
	row     int
	rect    hitRect
}

type columnHit struct {
	col  int
	rect hitRect
}

type dragState struct {
	taskID     string
	taskKey    string
	fromStatus string
	x, y       int
	hoverCol   int
	moved      bool
}

type editField int

const (
	editTitle editField = iota
	editDescription
	editLabels
	editStatus
	editPriority
	editAssignee
	editSave
	editDelete
	editClose
	editFieldCount
)

type editForm struct {
	taskID        string
	taskKey       string
	initialStatus string
	url           string
	createdAt     string
	updatedAt     string
	focus         editField
	title         textinput.Model
	description   textarea.Model
	labels        textinput.Model
	status        textinput.Model
	priority      textinput.Model
	assignee      textinput.Model
	fieldHits     map[editField]hitRect
	saveRect      hitRect
	deleteRect    hitRect
	closeRect     hitRect
	err           string

	confirmDelete     bool
	confirmFocus      int
	confirmDeleteRect hitRect
	confirmCancelRect hitRect
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

	cardHits   []cardHit
	columnHits []columnHit
	drag       *dragState
	edit       *editForm

	lastClickID string
	lastClickAt time.Time

	moves    map[string]*pendingMove
	moveSeq  int
	ops      int
	quitting bool

	input textinput.Model
}

// Run starts the board.
func Run(ctx context.Context, backend sdk.Backend) error {
	m := &Model{ctx: ctx, backend: backend, width: 120, height: 40, moves: map[string]*pendingMove{}}
	m.input = textinput.New()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	return m.load("")
}

func (m *Model) load(message string) tea.Cmd {
	return m.loadFocus(message, "")
}

func (m *Model) loadFocus(message, focusID string) tea.Cmd {
	return func() tea.Msg {
		info, err := m.backend.Describe(m.ctx)
		if err != nil {
			return loadedMsg{err: err}
		}
		tasks, err := m.backend.ListTasks(m.ctx, &pb.TaskFilter{})
		if err != nil {
			return loadedMsg{err: err}
		}
		return loadedMsg{tasks: tasks, info: info, message: message, focusID: focusID}
	}
}

func (m *Model) action(fn func() error, message string) tea.Cmd {
	return m.actionFocus(fn, message, "")
}

func (m *Model) actionFocus(fn func() error, message, focusID string) tea.Cmd {
	m.ops++
	return func() tea.Msg {
		if err := fn(); err != nil {
			return loadedMsg{err: err, tracked: true}
		}
		msg := m.loadFocus(message, focusID)()
		if lm, ok := msg.(loadedMsg); ok {
			lm.tracked = true
			return lm
		}
		return msg
	}
}

func (m *Model) requestQuit() (tea.Model, tea.Cmd) {
	if len(m.moves) == 0 && m.ops == 0 {
		return m, tea.Quit
	}
	m.quitting = true
	m.message = "Saving changes..."
	m.moveSeq++
	cmd := m.flushMoves()
	if cmd == nil && len(m.moves) == 0 && m.ops == 0 {
		return m, tea.Quit
	}
	return m, cmd
}

func (m *Model) scheduleMoveFlush() tea.Cmd {
	m.moveSeq++
	seq := m.moveSeq
	return tea.Tick(moveDebounce, func(time.Time) tea.Msg {
		return moveFlushMsg{seq: seq}
	})
}

func (m *Model) flushMoves() tea.Cmd {
	var cmds []tea.Cmd
	for id, state := range m.moves {
		if state.inFlight || state.desired == nil {
			continue
		}
		state.sent = state.desired
		state.sentPosition = state.desiredPosition
		state.desired = nil
		state.inFlight = true
		req := state.sent
		taskID := id
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
			defer cancel()
			_, err := m.backend.MoveTask(ctx, req)
			return moveResultMsg{taskID: taskID, request: req, err: err}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) handleMoveResult(msg moveResultMsg) (tea.Model, tea.Cmd) {
	state := m.moves[msg.taskID]
	if state == nil {
		return m, nil
	}
	state.inFlight = false
	if msg.err != nil {
		if state.desired != nil {
			// A newer move supersedes the failed request.
			if m.quitting {
				return m, m.flushMoves()
			}
			return m, m.scheduleMoveFlush()
		}
		t := m.taskByID(msg.taskID)
		if t != nil {
			t.Status = state.confirmedStatus
			t.Position = state.confirmedPosition
			m.focusTask(t.Id)
			m.clamp()
		}
		delete(m.moves, msg.taskID)
		m.err = fmt.Errorf("move failed: %w", msg.err)
		m.message = ""
		if m.quitting {
			m.quitting = false
		}
		return m, nil
	}
	if state.sent != nil {
		state.confirmedStatus = state.sent.Status
		state.confirmedPosition = state.sentPosition
	} else if msg.request != nil {
		state.confirmedStatus = msg.request.Status
	}
	state.sent = nil
	if state.desired != nil {
		if m.quitting {
			return m, m.flushMoves()
		}
		return m, m.scheduleMoveFlush()
	}
	delete(m.moves, msg.taskID)
	if m.quitting && len(m.moves) == 0 && m.ops == 0 {
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) queueMove(req *pb.MoveTaskRequest, follow bool, message string) tea.Cmd {
	t := m.taskByID(req.Id)
	if t == nil || m.info == nil {
		return nil
	}
	if m.moves == nil {
		m.moves = map[string]*pendingMove{}
	}
	state := m.moves[t.Id]
	if state == nil {
		state = &pendingMove{
			confirmedStatus:   t.Status,
			confirmedPosition: t.Position,
		}
		m.moves[t.Id] = state
	}

	pos := m.positionForMove(t, req)
	t.Status = req.Status
	t.Position = pos
	state.desired = req
	state.desiredPosition = pos
	m.message = message
	m.err = nil
	if follow {
		m.focusTask(t.Id)
	}

	if state.inFlight {
		return nil
	}
	if strings.EqualFold(state.confirmedStatus, req.Status) && req.BeforeId == nil && req.AfterId == nil {
		t.Status = state.confirmedStatus
		t.Position = state.confirmedPosition
		delete(m.moves, t.Id)
		if follow {
			m.focusTask(t.Id)
		}
		m.message = ""
		return nil
	}
	return m.scheduleMoveFlush()
}

func (m *Model) applyPendingMoves() {
	for id, state := range m.moves {
		t := m.taskByID(id)
		if t == nil {
			continue
		}
		switch {
		case state.desired != nil:
			t.Status = state.desired.Status
			t.Position = state.desiredPosition
		case state.sent != nil:
			t.Status = state.sent.Status
			t.Position = state.sentPosition
		}
	}
}

func (m *Model) positionForMove(t *pb.Task, req *pb.MoveTaskRequest) float64 {
	list := m.tasksInStatus(req.Status, t.Id)
	if req.BeforeId != nil {
		if pos, ok := relativePosition(list, *req.BeforeId, true); ok {
			return pos
		}
	} else if req.AfterId != nil {
		if pos, ok := relativePosition(list, *req.AfterId, false); ok {
			return pos
		}
	}
	if len(list) == 0 {
		return 1024
	}
	return list[len(list)-1].Position + 1024
}

func (m *Model) tasksInStatus(status, excludeID string) []*pb.Task {
	var out []*pb.Task
	for _, t := range m.tasks {
		if t.Archived || t.Id == excludeID || !strings.EqualFold(t.Status, status) {
			continue
		}
		out = append(out, t)
	}
	sortTasks(out)
	return out
}

func sortTasks(tasks []*pb.Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].Position < tasks[j].Position
	})
}

func relativePosition(tasks []*pb.Task, neighborID string, before bool) (float64, bool) {
	idx := -1
	for i, t := range tasks {
		if t.Id == neighborID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, false
	}
	pos := tasks[idx].Position
	if before {
		if idx == 0 {
			return pos - 1024, true
		}
		return (tasks[idx-1].Position + pos) / 2, true
	}
	if idx == len(tasks)-1 {
		return pos + 1024, true
	}
	return (pos + tasks[idx+1].Position) / 2, true
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case loadedMsg:
		if msg.tracked && m.ops > 0 {
			m.ops--
		}
		if msg.err != nil {
			m.err = msg.err
			if m.quitting {
				m.quitting = false
				m.message = ""
			}
			return m, nil
		}
		m.err = nil
		if msg.info != nil {
			m.info = msg.info
		}
		if msg.tasks != nil {
			m.tasks = msg.tasks
			m.applyPendingMoves()
		}
		m.message = msg.message
		if msg.focusID != "" {
			m.focusTask(msg.focusID)
		}
		m.clamp()
		if m.quitting && len(m.moves) == 0 && m.ops == 0 {
			return m, tea.Quit
		}
		return m, nil
	case moveFlushMsg:
		if msg.seq != m.moveSeq {
			return m, nil
		}
		return m, m.flushMoves()
	case moveResultMsg:
		return m.handleMoveResult(msg)
	case tea.MouseMsg:
		if m.mode == modeEdit {
			return m.updateEditMouse(tea.MouseEvent(msg))
		}
		if m.mode != modeNormal {
			return m, nil
		}
		return m.updateMouse(tea.MouseEvent(msg))
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.quitting {
			return m, nil
		}
		switch m.mode {
		case modeFilter:
			return m.updateFilter(msg)
		case modeNew:
			return m.updateNew(msg)
		case modeEdit:
			return m.updateEdit(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m *Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.drag = nil
		return m, nil
	case "q":
		return m.requestQuit()
	case "r":
		if len(m.moves) > 0 {
			m.message = "Saving changes..."
			return m, nil
		}
		return m, m.load("Refreshed")
	case "left":
		if m.col > 0 {
			m.col--
			m.row = 0
			m.clamp()
		}
	case "right":
		if m.info != nil && m.col < len(m.info.Columns)-1 {
			m.col++
			m.row = 0
			m.clamp()
		}
	case "up":
		if m.row > 0 {
			m.row--
		}
	case "down":
		if m.row < len(m.columnTasks())-1 {
			m.row++
		}
	case "shift+left", "ctrl+left":
		return m, m.moveColumn(-1)
	case "shift+right", "ctrl+right":
		return m, m.moveColumn(1)
	case "shift+up", "ctrl+up":
		return m, m.reorder(-1)
	case "shift+down", "ctrl+down":
		return m, m.reorder(1)
	case "n":
		m.input = textinput.New()
		m.input.Placeholder = "Task title"
		m.input.Focus()
		m.mode = modeNew
		return m, textinput.Blink
	case "enter":
		if t := m.selected(); t != nil {
			return m, m.openEdit(t.Id)
		}
	case "/", "f":
		m.input = textinput.New()
		m.input.Placeholder = "Filter"
		m.input.SetValue(m.filter)
		m.input.Focus()
		m.mode = modeFilter
		return m, textinput.Blink
	}
	return m, nil
}

func (m *Model) updateMouse(msg tea.MouseEvent) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if msg.Action == tea.MouseActionPress && m.row > 0 {
			m.row--
		}
		return m, nil
	case tea.MouseButtonWheelDown:
		if msg.Action == tea.MouseActionPress && m.row < len(m.columnTasks())-1 {
			m.row++
		}
		return m, nil
	}

	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		if h, ok := m.cardAt(msg.X, msg.Y); ok {
			m.col = h.col
			m.row = h.row
			m.clamp()
			status := ""
			if m.info != nil && h.col < len(m.info.Columns) {
				status = m.info.Columns[h.col].Name
			}
			m.drag = &dragState{
				taskID: h.taskID, taskKey: h.taskKey, fromStatus: status,
				x: msg.X, y: msg.Y, hoverCol: h.col,
			}
		} else if c, ok := m.columnAt(msg.X, msg.Y); ok {
			m.col = c.col
			m.clamp()
		}
	case tea.MouseActionMotion:
		if m.drag != nil {
			if msg.X != m.drag.x || msg.Y != m.drag.y {
				m.drag.moved = true
			}
			m.drag.x, m.drag.y = msg.X, msg.Y
			if c, ok := m.columnAt(msg.X, msg.Y); ok {
				m.drag.hoverCol = c.col
			}
		}
	case tea.MouseActionRelease:
		if m.drag != nil {
			return m.finishDrag(msg.X, msg.Y)
		}
	}
	return m, nil
}

func (m *Model) finishDrag(x, y int) (tea.Model, tea.Cmd) {
	d := m.drag
	m.drag = nil
	if d == nil {
		return m, nil
	}
	if !d.moved {
		if m.registerClick(d.taskID) {
			return m, m.openEdit(d.taskID)
		}
		return m, nil
	}
	m.lastClickID = ""
	if m.info == nil {
		return m, nil
	}
	target, ok := m.columnAt(x, y)
	if !ok {
		return m, nil
	}
	status := m.info.Columns[target.col].Name
	req := &pb.MoveTaskRequest{Id: d.taskID, Status: status}
	if m.info.Capabilities.GetOrdering() {
		if h, ok := m.cardAt(x, y); ok && h.taskID != d.taskID {
			if y < h.rect.y+h.rect.h/2 {
				req.BeforeId = &h.taskID
			} else {
				req.AfterId = &h.taskID
			}
		}
	}
	if strings.EqualFold(status, d.fromStatus) && req.BeforeId == nil && req.AfterId == nil {
		return m, nil
	}
	message := "Moved " + d.taskKey + " to " + status
	if strings.EqualFold(status, d.fromStatus) {
		message = "Reordered " + d.taskKey
	}
	return m, m.queueMove(req, true, message)
}

func (m *Model) cardAt(x, y int) (cardHit, bool) {
	for _, h := range m.cardHits {
		if h.rect.contains(x, y) {
			return h, true
		}
	}
	return cardHit{}, false
}

func (m *Model) columnAt(x, y int) (columnHit, bool) {
	for _, h := range m.columnHits {
		if h.rect.contains(x, y) {
			return h, true
		}
	}
	return columnHit{}, false
}

func (m *Model) dragStatus() string {
	if m.drag == nil || m.info == nil || m.drag.hoverCol < 0 || m.drag.hoverCol >= len(m.info.Columns) {
		return ""
	}
	return m.info.Columns[m.drag.hoverCol].Name
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
	e := m.edit
	if e == nil {
		m.mode = modeNormal
		return m, nil
	}
	if e.confirmDelete {
		return m.updateDeleteConfirm(msg)
	}
	switch msg.String() {
	case "esc":
		return m.closeEdit()
	case "tab":
		return m, m.cycleEditFocus(1)
	case "shift+tab":
		return m, m.cycleEditFocus(-1)
	case "ctrl+s", "ctrl+enter":
		return m.saveEdit()
	case "enter":
		switch e.focus {
		case editSave:
			return m.saveEdit()
		case editDelete:
			return m, m.requestDelete()
		case editClose:
			return m.closeEdit()
		case editDescription:
			// Let the textarea insert a newline.
		default:
			return m, m.cycleEditFocus(1)
		}
	case " ":
		switch e.focus {
		case editSave:
			return m.saveEdit()
		case editDelete:
			return m, m.requestDelete()
		case editClose:
			return m.closeEdit()
		}
	}

	var cmd tea.Cmd
	switch e.focus {
	case editTitle:
		e.title, cmd = e.title.Update(msg)
	case editDescription:
		e.description, cmd = e.description.Update(msg)
	case editLabels:
		e.labels, cmd = e.labels.Update(msg)
	case editStatus:
		e.status, cmd = e.status.Update(msg)
	case editPriority:
		e.priority, cmd = e.priority.Update(msg)
	case editAssignee:
		e.assignee, cmd = e.assignee.Update(msg)
	}
	return m, cmd
}

func (m *Model) updateDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.edit
	if e == nil {
		m.mode = modeNormal
		return m, nil
	}
	switch msg.String() {
	case "esc", "n":
		e.confirmDelete = false
		return m, m.setEditFocus(editDelete)
	case "tab", "shift+tab", "left", "right":
		if e.confirmFocus == 0 {
			e.confirmFocus = 1
		} else {
			e.confirmFocus = 0
		}
		return m, nil
	case "enter", " ":
		if e.confirmFocus == 1 {
			e.confirmDelete = false
			return m, m.setEditFocus(editDelete)
		}
		return m.deleteTask()
	case "y":
		return m.deleteTask()
	}
	return m, nil
}

func (m *Model) requestDelete() tea.Cmd {
	e := m.edit
	if e == nil {
		return nil
	}
	e.confirmDelete = true
	e.confirmFocus = 1
	return m.setEditFocus(editDelete)
}

func (m *Model) deleteTask() (tea.Model, tea.Cmd) {
	e := m.edit
	if e == nil {
		m.mode = modeNormal
		return m, nil
	}
	id, key := e.taskID, e.taskKey
	delete(m.moves, id)
	m.mode = modeNormal
	m.edit = nil
	return m, m.action(func() error {
		return m.backend.DeleteTask(m.ctx, id, true)
	}, "Deleted "+key)
}

func (m *Model) updateEditMouse(msg tea.MouseEvent) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	e := m.edit
	if e == nil {
		return m, nil
	}
	if e.confirmDelete {
		if e.confirmCancelRect.contains(msg.X, msg.Y) {
			e.confirmDelete = false
			return m, m.setEditFocus(editDelete)
		}
		if e.confirmDeleteRect.contains(msg.X, msg.Y) {
			return m.deleteTask()
		}
		return m, nil
	}
	if e.saveRect.contains(msg.X, msg.Y) {
		return m.saveEdit()
	}
	if e.deleteRect.contains(msg.X, msg.Y) {
		return m, m.requestDelete()
	}
	if e.closeRect.contains(msg.X, msg.Y) {
		return m.closeEdit()
	}
	for f, r := range e.fieldHits {
		if r.contains(msg.X, msg.Y) {
			return m, m.setEditFocus(f)
		}
	}
	return m, nil
}

func (m *Model) openEdit(taskID string) tea.Cmd {
	t := m.taskByID(taskID)
	if t == nil {
		return nil
	}
	e := &editForm{
		taskID: t.Id, taskKey: t.Key, initialStatus: t.Status, url: t.Url,
		fieldHits: map[editField]hitRect{},
	}
	if ts := t.GetCreatedAt(); ts != nil {
		e.createdAt = ts.AsTime().UTC().Format(time.RFC3339)
	}
	if ts := t.GetUpdatedAt(); ts != nil {
		e.updatedAt = ts.AsTime().UTC().Format(time.RFC3339)
	}
	newInput := func(value, placeholder string) textinput.Model {
		in := textinput.New()
		in.Prompt = ""
		in.Placeholder = placeholder
		in.SetValue(value)
		return in
	}
	e.title = newInput(t.Title, "Task title")
	e.labels = newInput(strings.Join(t.Labels, ", "), "bug, backend")
	e.status = newInput(t.Status, "todo, in_progress, done")
	e.priority = newInput(sdk.PriorityLabel(t.Priority), "none, low, medium, high, urgent")
	e.assignee = newInput(t.Assignee, "username or @me")
	e.description = textarea.New()
	e.description.Prompt = ""
	e.description.Placeholder = "Description"
	e.description.SetValue(t.Description)
	e.description.ShowLineNumbers = false
	e.description.SetHeight(3)
	m.edit = e
	m.mode = modeEdit
	m.lastClickID = ""
	return m.setEditFocus(editTitle)
}

func (m *Model) taskByID(id string) *pb.Task {
	for _, t := range m.tasks {
		if t.Id == id {
			return t
		}
	}
	return nil
}

func (m *Model) setEditFocus(f editField) tea.Cmd {
	e := m.edit
	if e == nil {
		return nil
	}
	e.title.Blur()
	e.description.Blur()
	e.labels.Blur()
	e.status.Blur()
	e.priority.Blur()
	e.assignee.Blur()
	e.focus = f
	switch f {
	case editTitle:
		return e.title.Focus()
	case editDescription:
		return e.description.Focus()
	case editLabels:
		return e.labels.Focus()
	case editStatus:
		return e.status.Focus()
	case editPriority:
		return e.priority.Focus()
	case editAssignee:
		return e.assignee.Focus()
	}
	return nil
}

func (m *Model) cycleEditFocus(delta int) tea.Cmd {
	if m.edit == nil {
		return nil
	}
	n := int(editFieldCount)
	next := (int(m.edit.focus) + delta%n + n) % n
	return m.setEditFocus(editField(next))
}

func (m *Model) closeEdit() (tea.Model, tea.Cmd) {
	m.mode = modeNormal
	m.edit = nil
	return m, nil
}

func (m *Model) saveEdit() (tea.Model, tea.Cmd) {
	e := m.edit
	if e == nil {
		m.mode = modeNormal
		return m, nil
	}
	title := strings.TrimSpace(e.title.Value())
	if title == "" {
		e.err = "title is required"
		return m, nil
	}
	priority, ok := sdk.ParsePriority(strings.TrimSpace(e.priority.Value()))
	if !ok {
		e.err = "priority must be none, low, medium, high, or urgent"
		return m, nil
	}
	status, statusNames, ok := m.resolveFormStatus(e.status.Value())
	if !ok {
		e.err = "status must be one of: " + strings.Join(statusNames, ", ")
		return m, nil
	}
	desc := e.description.Value()
	assignee := strings.TrimSpace(e.assignee.Value())
	req := &pb.UpdateTaskRequest{
		Id: e.taskID, Title: &title, Description: &desc,
		Priority: &priority, SetLabels: true, Labels: parseLabels(e.labels.Value()),
	}
	if assignee == "" {
		req.ClearAssignee = true
	} else {
		req.Assignee = &assignee
	}
	statusChanged := !strings.EqualFold(status, e.initialStatus)
	key := e.taskKey
	id := e.taskID
	m.mode = modeNormal
	m.edit = nil

	updateCmd := m.actionFocus(func() error {
		_, err := m.backend.UpdateTask(m.ctx, req)
		return err
	}, "Updated "+key, id)
	if !statusChanged {
		return m, updateCmd
	}
	moveCmd := m.queueMove(&pb.MoveTaskRequest{Id: id, Status: status}, true, "Moved "+key+" to "+status)
	if moveCmd == nil {
		return m, updateCmd
	}
	return m, tea.Batch(updateCmd, moveCmd)
}

func (m *Model) resolveFormStatus(value string) (string, []string, bool) {
	value = strings.TrimSpace(value)
	if m.info == nil {
		return "", nil, value == ""
	}
	names := make([]string, 0, len(m.info.Columns))
	for _, c := range m.info.Columns {
		names = append(names, c.Name)
		if strings.EqualFold(c.Name, value) ||
			strings.EqualFold(normalizeStatus(c.Name), normalizeStatus(value)) {
			return c.Name, names, true
		}
	}
	return "", names, false
}

func normalizeStatus(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", " ")
}

func (m *Model) registerClick(taskID string) bool {
	now := time.Now()
	if m.lastClickID == taskID && now.Sub(m.lastClickAt) <= 450*time.Millisecond {
		m.lastClickID = ""
		m.lastClickAt = time.Time{}
		return true
	}
	m.lastClickID = taskID
	m.lastClickAt = now
	return false
}

func parseLabels(s string) []string {
	var labels []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			labels = append(labels, part)
		}
	}
	return labels
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
	return m.queueMove(&pb.MoveTaskRequest{Id: t.Id, Status: status}, true, "Moved "+t.Key+" to "+status)
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
	return m.queueMove(req, true, "Reordered "+t.Key)
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
	return m.tasksFor(m.info.Columns[m.col].Name)
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

func (m *Model) focusTask(id string) {
	if m.info == nil {
		return
	}
	var target *pb.Task
	for _, t := range m.tasks {
		if t.Id == id && !t.Archived {
			target = t
			break
		}
	}
	if target == nil {
		return
	}
	for i, c := range m.info.Columns {
		if strings.EqualFold(c.Name, target.Status) {
			m.col = i
			break
		}
	}
	for i, t := range m.columnTasks() {
		if t.Id == id {
			m.row = i
			return
		}
	}
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

	if m.drag != nil && m.drag.moved {
		target := m.dragStatus()
		if target != "" {
			top += "  moving " + m.drag.taskKey + " to " + target
		}
	}
	columnsY := lipgloss.Height(top) + 1
	cols := m.renderColumns(columnsY)
	help := lipgloss.NewStyle().Faint(true).Width(max(1, m.width)).Render(m.helpText())

	body := lipgloss.JoinVertical(lipgloss.Left, top, "", cols)
	switch m.mode {
	case modeFilter:
		return m.overlay("Filter", m.input.View())
	case modeNew:
		return m.overlay("New task in "+m.info.Columns[m.col].Name, m.input.View()+"\n\nenter create  esc cancel")
	case modeEdit:
		if m.edit != nil && m.edit.confirmDelete {
			return m.renderDeleteConfirm()
		}
		return m.renderEdit()
	}
	return m.withFooter(body, help)
}

func (m *Model) withFooter(body, footer string) string {
	gap := m.height - lipgloss.Height(body) - lipgloss.Height(footer) + 1
	if gap < 1 {
		gap = 1
	}
	return body + strings.Repeat("\n", gap) + footer
}

func (m *Model) renderEdit() string {
	e := m.edit
	if e == nil {
		return ""
	}
	contentWidth := m.width - 12
	if contentWidth > 76 {
		contentWidth = 76
	}
	if contentWidth < 40 {
		contentWidth = max(20, m.width-4)
	}
	fieldWidth := max(10, contentWidth-2)
	labelWidth := min(12, max(6, fieldWidth-12))
	inputWidth := max(1, fieldWidth-2-labelWidth-1)
	if e.title.Width != inputWidth {
		e.title.Width = inputWidth
	}
	if e.labels.Width != inputWidth {
		e.labels.Width = inputWidth
	}
	if e.status.Width != inputWidth {
		e.status.Width = inputWidth
	}
	if e.priority.Width != inputWidth {
		e.priority.Width = inputWidth
	}
	if e.assignee.Width != inputWidth {
		e.assignee.Width = inputWidth
	}
	if e.description.Width() != inputWidth {
		e.description.SetWidth(inputWidth)
	}
	if e.description.Height() != 3 {
		e.description.SetHeight(3)
	}

	var lines []string
	row := 0
	add := func(s string) int {
		line := row
		lines = append(lines, s)
		row += lipgloss.Height(s)
		return line
	}
	fieldLines := map[editField]int{}
	fieldHeights := map[editField]int{}
	addField := func(f editField, labelText, view string) {
		labelStyle := lipgloss.NewStyle().Faint(true)
		borderColor := "8"
		if e.focus == f {
			labelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
			borderColor = "6"
		}
		label := labelStyle.Width(labelWidth).Render(truncate(labelText, labelWidth))
		content := lipgloss.JoinHorizontal(lipgloss.Top, label, " ", view)
		block := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color(borderColor)).
			PaddingLeft(1).
			Render(content)
		fieldLines[f] = add(block)
		fieldHeights[f] = lipgloss.Height(block)
		add("")
	}

	add(lipgloss.NewStyle().Bold(true).Render(truncate("Task "+e.taskKey, fieldWidth)))
	if e.url != "" {
		add(lipgloss.NewStyle().Faint(true).Render(truncate("URL: "+e.url, fieldWidth)))
	}
	if e.createdAt != "" || e.updatedAt != "" {
		add(lipgloss.NewStyle().Faint(true).Render(truncate(
			"Created: "+e.createdAt+"  Updated: "+e.updatedAt, fieldWidth)))
	}
	add("")
	addField(editTitle, "Title", e.title.View())
	addField(editDescription, "Description", e.description.View())
	addField(editLabels, "Labels", e.labels.View())
	addField(editStatus, "Status", e.status.View())
	addField(editPriority, "Priority", e.priority.View())
	addField(editAssignee, "Assignee", e.assignee.View())
	if e.err != "" {
		add(lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(truncate(e.err, fieldWidth)))
		add("")
	}
	buttonStyle := func(focused bool, color string) lipgloss.Style {
		style := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(color)).
			Padding(0, 2)
		if focused {
			style = style.
				Background(lipgloss.AdaptiveColor{Light: "254", Dark: "237"}).
				Bold(true)
		}
		return style
	}
	save := buttonStyle(e.focus == editSave, "2").Render("Save")
	deleteButton := buttonStyle(e.focus == editDelete, "1").Render("Delete")
	closeButton := buttonStyle(e.focus == editClose, "6").Render("Close")
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, save, "  ", deleteButton, "  ", closeButton)
	buttonLine := add(buttons)
	add(lipgloss.NewStyle().Faint(true).Render(truncate(
		"tab next  shift+tab previous  ctrl+s save  esc close", fieldWidth)))

	content := strings.Join(lines, "\n")
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")).
		Padding(1, 1).
		Width(contentWidth).
		Render(content)
	outerW := lipgloss.Width(modal)
	outerH := lipgloss.Height(modal)
	areaH := max(1, m.height-4)
	left, top := 0, 0
	if m.width > outerW {
		left = (m.width - outerW) / 2
	}
	if areaH > outerH {
		top = (areaH - outerH) / 2
	}
	contentX := left + 2
	contentY := top + 2
	e.fieldHits = make(map[editField]hitRect, len(fieldLines))
	for f, line := range fieldLines {
		e.fieldHits[f] = hitRect{
			x: contentX, y: contentY + line,
			w: fieldWidth, h: fieldHeights[f],
		}
	}
	e.saveRect = hitRect{
		x: contentX, y: contentY + buttonLine,
		w: lipgloss.Width(save), h: lipgloss.Height(save),
	}
	deleteX := contentX + lipgloss.Width(save) + 2
	e.deleteRect = hitRect{
		x: deleteX, y: contentY + buttonLine,
		w: lipgloss.Width(deleteButton), h: lipgloss.Height(deleteButton),
	}
	e.closeRect = hitRect{
		x: deleteX + lipgloss.Width(deleteButton) + 2, y: contentY + buttonLine,
		w: lipgloss.Width(closeButton), h: lipgloss.Height(closeButton),
	}
	return lipgloss.Place(m.width, areaH, lipgloss.Center, lipgloss.Center, modal)
}

func (m *Model) renderDeleteConfirm() string {
	e := m.edit
	if e == nil {
		return ""
	}
	contentWidth := min(max(44, m.width/3), 64)
	if contentWidth > m.width-4 {
		contentWidth = max(20, m.width-4)
	}
	fieldWidth := max(10, contentWidth-2)

	var lines []string
	row := 0
	add := func(s string) int {
		line := row
		lines = append(lines, s)
		row += lipgloss.Height(s)
		return line
	}
	add(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")).Render(
		truncate("Delete "+e.taskKey+"?", fieldWidth)))
	add(lipgloss.NewStyle().Faint(true).Width(fieldWidth).Render(
		"This permanently deletes the task. This cannot be undone."))
	add("")

	buttonStyle := func(focused bool, color string) lipgloss.Style {
		style := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(color)).
			Padding(0, 2)
		if focused {
			style = style.
				Background(lipgloss.AdaptiveColor{Light: "254", Dark: "237"}).
				Bold(true)
		}
		return style
	}
	deleteButton := buttonStyle(e.confirmFocus == 0, "1").Render("Delete")
	cancelButton := buttonStyle(e.confirmFocus == 1, "6").Render("Cancel")
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, deleteButton, "  ", cancelButton)
	buttonLine := add(buttons)
	add(lipgloss.NewStyle().Faint(true).Render(truncate(
		"tab switch  enter confirm  esc cancel", fieldWidth)))

	content := strings.Join(lines, "\n")
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("1")).
		Padding(1, 1).
		Width(contentWidth).
		Render(content)
	outerW := lipgloss.Width(modal)
	outerH := lipgloss.Height(modal)
	areaH := max(1, m.height-4)
	left, top := 0, 0
	if m.width > outerW {
		left = (m.width - outerW) / 2
	}
	if areaH > outerH {
		top = (areaH - outerH) / 2
	}
	contentX := left + 2
	contentY := top + 2
	e.confirmDeleteRect = hitRect{
		x: contentX, y: contentY + buttonLine,
		w: lipgloss.Width(deleteButton), h: lipgloss.Height(deleteButton),
	}
	e.confirmCancelRect = hitRect{
		x: contentX + lipgloss.Width(deleteButton) + 2, y: contentY + buttonLine,
		w: lipgloss.Width(cancelButton), h: lipgloss.Height(cancelButton),
	}
	return lipgloss.Place(m.width, areaH, lipgloss.Center, lipgloss.Center, modal)
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

func (m *Model) renderColumns(y int) string {
	n := len(m.info.Columns)
	if n == 0 {
		return ""
	}
	m.cardHits = m.cardHits[:0]
	m.columnHits = m.columnHits[:0]
	width := (m.width - (n - 1)) / n
	if width < 16 {
		width = 16
	}
	views := make([]string, 0, n)
	x := 0
	maxH := 0
	for i, c := range m.info.Columns {
		view := m.renderColumn(i, c, width, x, y)
		views = append(views, view)
		if h := lipgloss.Height(view); h > maxH {
			maxH = h
		}
		x += lipgloss.Width(view)
	}
	// Make the whole lane below the header a drop target, including empty
	// space below the last card.
	laneH := max(maxH, m.height-y-2)
	for i := range m.columnHits {
		if m.columnHits[i].rect.h < laneH {
			m.columnHits[i].rect.h = laneH
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, views...)
}

func (m *Model) renderColumn(idx int, c *pb.Column, width, x, y int) string {
	tasks := m.tasksFor(c.Name)
	active := idx == m.col
	border := lipgloss.RoundedBorder()
	color := "8"
	if active {
		color = "6"
	}
	if m.drag != nil && m.drag.moved && idx == m.drag.hoverCol {
		border = lipgloss.ThickBorder()
		color = "2"
	}
	header := lipgloss.NewStyle().
		Border(border).
		BorderForeground(lipgloss.Color(color)).
		Width(width-2).
		Padding(0, 1).
		Bold(true).
		Render(truncate(fmt.Sprintf("%s (%d)", c.Name, len(tasks)), width-4))

	var cards []string
	cardY := y + 3
	for i, t := range tasks {
		card := m.renderCard(t, i == m.row && active, width)
		h := lipgloss.Height(card)
		m.cardHits = append(m.cardHits, cardHit{
			taskID: t.Id, taskKey: t.Key, col: idx, row: i,
			rect: hitRect{x: x, y: cardY, w: width, h: h},
		})
		cards = append(cards, card)
		cardY += h + 1
	}
	var body string
	if len(cards) == 0 {
		body = lipgloss.NewStyle().Faint(true).Width(width).Padding(0, 1).Render("empty")
	} else {
		body = strings.Join(cards, "\n\n")
	}
	view := header + "\n" + body
	m.columnHits = append(m.columnHits, columnHit{
		col: idx, rect: hitRect{x: x, y: y, w: width, h: lipgloss.Height(view)},
	})
	return view + " "
}

func (m *Model) renderCard(t *pb.Task, selected bool, width int) string {
	inner := max(1, width-4)
	title := strings.Join(strings.Fields(t.Title), " ")
	title = truncate(title, max(1, inner-len([]rune(t.Key))-1))
	metaText := sdk.PriorityLabel(t.Priority)
	if len(t.Labels) > 0 {
		metaText += " " + strings.Join(t.Labels, ",")
	}
	if t.Assignee != "" {
		metaText += " @" + t.Assignee
	}
	metaText = truncate(metaText, inner)
	description := strings.Join(strings.Fields(t.Description), " ")
	description = truncate(description, inner)

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
	line := lipgloss.NewStyle().Faint(true).Render(t.Key) + " " +
		lipgloss.NewStyle().Bold(true).Render(title) + "\n" +
		lipgloss.NewStyle().Foreground(lipgloss.Color(prColor)).Render(metaText)
	if description != "" {
		line += "\n" + lipgloss.NewStyle().Faint(true).Render(description)
	}

	borderColor := "8"
	if selected {
		borderColor = "6"
	}
	if m.drag != nil && m.drag.taskID == t.Id {
		borderColor = "3"
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(width-2).
		Padding(0, 1)
	if m.drag != nil && m.drag.taskID == t.Id {
		style = style.Background(lipgloss.AdaptiveColor{Light: "229", Dark: "58"})
	} else if selected {
		style = style.Background(lipgloss.AdaptiveColor{Light: "254", Dark: "237"})
	}
	return style.Render(line)
}

func (m *Model) helpText() string {
	nav := []string{"←/→ column", "↑/↓ card", "shift+←/→ move"}
	if m.info != nil && m.info.Capabilities.GetOrdering() {
		nav = append(nav, "shift+↑/↓ reorder")
	}
	actions := []string{
		"enter open", "n new", "f filter", "r refresh", "q quit",
		"drag cards with mouse",
	}
	return strings.Join(nav, "  ") + "\n" + strings.Join(actions, "  ")
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
	sortTasks(out)
	return out
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
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
