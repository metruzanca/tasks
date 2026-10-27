package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/metruzanca/tasks/pkg/sdk"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
	"github.com/metruzanca/tasks/plugins/sqlite"
)

func newTestModel(t *testing.T) (*Model, *sqlite.Backend, *pb.Task) {
	t.Helper()
	b, err := sqlite.New(sqlite.Config{
		Path: filepath.Join(t.TempDir(), "tasks.db"),
		Me:   "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	ctx := context.Background()
	task, err := b.CreateTask(ctx, &pb.CreateTaskRequest{Title: "Draggable card", Status: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := b.Describe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := b.ListTasks(ctx, &pb.TaskFilter{IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{ctx: ctx, backend: b, info: info, tasks: tasks, width: 140, height: 40, moves: map[string]*pendingMove{}, showArchived: true}
	m.input = textinput.New()
	return m, b, task
}

func flushPendingMoves(t *testing.T, m *Model) {
	t.Helper()
	for len(m.moves) > 0 {
		cmd := m.flushMoves()
		if cmd == nil {
			t.Fatalf("pending moves without a command: %+v", m.moves)
		}
		runMoveCmd(t, m, cmd())
	}
}

func runMoveCmd(t *testing.T, m *Model, msg tea.Msg) {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			if cmd != nil {
				runMoveCmd(t, m, cmd())
			}
		}
		return
	}
	m.Update(msg)
}

func processMoveResult(t *testing.T, m *Model, msg tea.Msg) bool {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		quit := false
		for _, cmd := range batch {
			if cmd != nil && processMoveResult(t, m, cmd()) {
				quit = true
			}
		}
		return quit
	}
	_, next := m.Update(msg)
	if next != nil {
		if _, ok := next().(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}

func TestMouseDragMovesTaskBetweenColumns(t *testing.T) {
	m, b, task := newTestModel(t)
	view := m.View()

	var card cardHit
	for _, h := range m.cardHits {
		if h.taskID == task.Id {
			card = h
			break
		}
	}
	if card.taskID == "" {
		t.Fatal("task card was not rendered")
	}
	lines := strings.Split(view, "\n")
	found := false
	for y := card.rect.y; y < card.rect.y+card.rect.h && y < len(lines); y++ {
		if strings.Contains(lines[y], task.Key) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("card hit at y=%d does not cover the card", card.rect.y)
	}
	var target columnHit
	for _, h := range m.columnHits {
		if m.info.Columns[h.col].Name == "in_progress" {
			target = h
			break
		}
	}
	if target.rect.w == 0 {
		t.Fatal("target column was not rendered")
	}

	pressX, pressY := card.rect.x+1, card.rect.y
	dropX, dropY := target.rect.x+2, target.rect.y+2
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: pressX, Y: pressY})
	if m.drag == nil {
		t.Fatal("press did not start a drag")
	}
	m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft, X: dropX, Y: dropY})
	_, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: dropX, Y: dropY})
	if cmd == nil {
		t.Fatal("release did not produce a move command")
	}
	if got := m.taskByID(task.Id); got == nil || got.Status != "in_progress" {
		t.Fatalf("optimistic status = %+v", got)
	}
	flushPendingMoves(t, m)

	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got.Status)
	}
	if sel := m.selected(); sel == nil || sel.Id != task.Id {
		t.Fatalf("selection did not follow the dragged card: %+v", sel)
	}
	if m.info.Columns[m.col].Name != "in_progress" {
		t.Fatalf("selected column = %q, want in_progress", m.info.Columns[m.col].Name)
	}
}

func TestArrowNavigationAndNoVimKeys(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.col = 0
	m.View()

	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.col != 1 {
		t.Fatalf("right arrow did not move to column 1: %d", m.col)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	if m.col != 1 {
		t.Fatalf("vim key changed the column: %d", m.col)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.col != 0 {
		t.Fatalf("left arrow did not move back to column 0: %d", m.col)
	}
}

func TestMouseClickSelectsCard(t *testing.T) {
	m, _, task := newTestModel(t)
	m.col = 0
	m.row = 0
	m.View()
	var card cardHit
	for _, h := range m.cardHits {
		if h.taskID == task.Id {
			card = h
			break
		}
	}
	if card.taskID == "" {
		t.Fatal("task card was not rendered")
	}
	x, y := card.rect.x+1, card.rect.y
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: y})
	_, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: x, Y: y})
	if cmd != nil {
		t.Fatal("a plain click should not move the task")
	}
	if got := m.selected(); got == nil || got.Id != task.Id {
		t.Fatalf("click did not select the card: %+v", got)
	}
}

func TestShiftArrowMovesCard(t *testing.T) {
	m, b, task := newTestModel(t)
	m.col = 1
	m.row = 0
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	if cmd == nil {
		t.Fatal("shift+right did not produce a move command")
	}
	if got := m.taskByID(task.Id); got == nil || got.Status != "in_progress" {
		t.Fatalf("optimistic status = %+v", got)
	}
	flushPendingMoves(t, m)
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got.Status)
	}
	if sel := m.selected(); sel == nil || sel.Id != task.Id {
		t.Fatalf("selection did not follow the moved card: %+v", sel)
	}
	if m.info.Columns[m.col].Name != "in_progress" {
		t.Fatalf("selected column = %q, want in_progress", m.info.Columns[m.col].Name)
	}
}

func TestReorderMovesSelectionWithCard(t *testing.T) {
	m, b, first := newTestModel(t)
	_, err := b.CreateTask(context.Background(), &pb.CreateTaskRequest{Title: "Second card", Status: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := b.ListTasks(context.Background(), &pb.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	m.tasks = tasks
	m.col = 1
	m.row = 0
	if sel := m.selected(); sel == nil || sel.Id != first.Id {
		t.Fatalf("test setup selected %+v, want first card", sel)
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	if cmd == nil {
		t.Fatal("shift+down did not produce a reorder command")
	}
	if sel := m.selected(); sel == nil || sel.Id != first.Id {
		t.Fatalf("selection did not follow reordered card: %+v", sel)
	}
	if m.row != 1 {
		t.Fatalf("row = %d, want 1 after moving down", m.row)
	}
	flushPendingMoves(t, m)
}

func TestMouseDropAnywhereInColumn(t *testing.T) {
	m, b, task := newTestModel(t)
	m.View()
	var card cardHit
	for _, h := range m.cardHits {
		if h.taskID == task.Id {
			card = h
			break
		}
	}
	if card.taskID == "" {
		t.Fatal("task card was not rendered")
	}
	var target columnHit
	for _, h := range m.columnHits {
		if m.info.Columns[h.col].Name == "in_progress" {
			target = h
			break
		}
	}
	if target.rect.w == 0 {
		t.Fatal("target column was not rendered")
	}
	// Drop well below the target column's header and empty body.
	dropX, dropY := target.rect.x+2, target.rect.y+12
	pressX, pressY := card.rect.x+1, card.rect.y
	m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: pressX, Y: pressY})
	m.Update(tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft, X: dropX, Y: dropY})
	_, cmd := m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: dropX, Y: dropY})
	if cmd == nil {
		t.Fatal("drop below the header did not produce a move command")
	}
	if got := m.taskByID(task.Id); got == nil || got.Status != "in_progress" {
		t.Fatalf("optimistic status = %+v", got)
	}
	flushPendingMoves(t, m)
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got.Status)
	}
}

func TestDoubleClickOpensEditForm(t *testing.T) {
	m, _, task := newTestModel(t)
	m.View()
	var card cardHit
	for _, h := range m.cardHits {
		if h.taskID == task.Id {
			card = h
			break
		}
	}
	if card.taskID == "" {
		t.Fatal("task card was not rendered")
	}
	x, y := card.rect.x+1, card.rect.y+1
	for i := 0; i < 2; i++ {
		m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: y})
		m.Update(tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: x, Y: y})
	}
	if m.mode != modeEdit || m.edit == nil {
		t.Fatalf("double click did not open the edit form: mode=%v", m.mode)
	}
	if m.edit.title.Value() != task.Title {
		t.Fatalf("title field = %q", m.edit.title.Value())
	}
	if m.edit.focus != editTitle {
		t.Fatalf("initial focus = %v, want title", m.edit.focus)
	}
}

func TestEditFormTabAndMouseFocus(t *testing.T) {
	m, _, task := newTestModel(t)
	m.openEdit(task.Id)
	if m.edit == nil {
		t.Fatal("edit form missing")
	}
	if m.edit.focus != editTitle {
		t.Fatalf("focus = %v, want title", m.edit.focus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.edit.focus != editDescription {
		t.Fatalf("tab focus = %v, want description", m.edit.focus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.edit.focus != editTitle {
		t.Fatalf("shift+tab focus = %v, want title", m.edit.focus)
	}
	for i := 0; i < 6; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyTab})
	}
	if m.edit.focus != editSave {
		t.Fatalf("focus = %v, want save", m.edit.focus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.edit.focus != editDelete {
		t.Fatalf("focus = %v, want delete", m.edit.focus)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.edit.focus != editClose {
		t.Fatalf("focus = %v, want close", m.edit.focus)
	}

	m.View()
	labelsHit, ok := m.edit.fieldHits[editLabels]
	if !ok {
		t.Fatal("labels field has no mouse hitbox")
	}
	m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: labelsHit.x + 1, Y: labelsHit.y,
	})
	if m.edit.focus != editLabels {
		t.Fatalf("mouse click focus = %v, want labels", m.edit.focus)
	}
}

func TestEditFormSaveButton(t *testing.T) {
	m, b, task := newTestModel(t)
	m.openEdit(task.Id)
	e := m.edit
	e.title.SetValue("Renamed card")
	e.description.SetValue("Updated description")
	e.labels.SetValue("one, two")
	e.priority.SetValue("high")
	e.assignee.SetValue("tester")
	m.View()
	if e.saveRect.w == 0 {
		t.Fatal("save button has no mouse hitbox")
	}
	_, cmd := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: e.saveRect.x + 1, Y: e.saveRect.y + 1,
	})
	if cmd == nil {
		t.Fatal("save button did not save")
	}
	msg := cmd()
	if lm, ok := msg.(loadedMsg); ok && lm.err != nil {
		t.Fatal(lm.err)
	}
	m.Update(msg)
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Renamed card" || got.Description != "Updated description" {
		t.Fatalf("unexpected task: %+v", got)
	}
	if got.Priority != pb.Priority_PRIORITY_HIGH || got.Assignee != "tester" {
		t.Fatalf("unexpected priority/assignee: %+v", got)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "one" || got.Labels[1] != "two" {
		t.Fatalf("labels = %v", got.Labels)
	}
}

func TestControlsStayAtBottomOfScreen(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.height = 28
	m.width = 120

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("view height = %d, want %d; tail=%q", len(lines), m.height, view[len(view)-80:])
	}
	if !strings.Contains(lines[len(lines)-1], "q quit") {
		t.Fatalf("controls are not on the bottom line: %q", lines[len(lines)-1])
	}
	firstBottom := len(lines) - 1

	m.tasks = nil
	lines = strings.Split(m.View(), "\n")
	if len(lines) != m.height {
		t.Fatalf("view height after removing cards = %d, want %d", len(lines), m.height)
	}
	if !strings.Contains(lines[len(lines)-1], "q quit") {
		t.Fatalf("controls moved after layout changed: %q", lines[len(lines)-1])
	}
	if len(lines)-1 != firstBottom {
		t.Fatalf("controls moved from row %d to %d", firstBottom, len(lines)-1)
	}
}

func TestEnterOpensCombinedTaskScreen(t *testing.T) {
	m, _, task := newTestModel(t)
	m.col = 1
	m.row = 0
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeEdit || m.edit == nil {
		t.Fatalf("enter did not open the task screen: mode=%v", m.mode)
	}
	if m.edit.taskID != task.Id || m.edit.title.Value() != task.Title {
		t.Fatalf("task fields were not populated: %+v", m.edit)
	}
	if m.edit.status.Value() != task.Status || m.edit.initialStatus != task.Status {
		t.Fatalf("status field = %q, want %q", m.edit.status.Value(), task.Status)
	}
	if m.edit.createdAt == "" {
		t.Fatal("created timestamp was not populated")
	}
}

func TestRemovedTaskKeybindsDoNothing(t *testing.T) {
	m, b, task := newTestModel(t)
	m.col = 1
	m.row = 0
	for _, r := range []rune{'e', 'd', 'a'} {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if m.mode != modeNormal {
			t.Fatalf("%q changed the mode to %v", r, m.mode)
		}
		if m.edit != nil {
			t.Fatalf("%q opened the task screen", r)
		}
	}
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.Status {
		t.Fatalf("removed d key still moved the task to %q", got.Status)
	}
}

func TestDeleteButtonConfirmationAndMouse(t *testing.T) {
	m, b, task := newTestModel(t)
	m.openEdit(task.Id)
	m.View()
	e := m.edit
	if e.deleteRect.w == 0 {
		t.Fatal("delete button has no hitbox")
	}
	m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: e.deleteRect.x + 1, Y: e.deleteRect.y + 1,
	})
	if !e.confirmDelete {
		t.Fatal("delete button did not open the confirmation")
	}
	m.View()
	if e.confirmDeleteRect.w == 0 || e.confirmCancelRect.w == 0 {
		t.Fatal("confirmation buttons have no hitboxes")
	}
	// Cancel returns to the form.
	m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: e.confirmCancelRect.x + 1, Y: e.confirmCancelRect.y + 1,
	})
	if e.confirmDelete {
		t.Fatal("cancel did not close the confirmation")
	}
	// Confirm via keyboard.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !e.confirmDelete {
		t.Fatal("delete button did not reopen the confirmation")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirming delete did not produce a command")
	}
	msg := cmd()
	if lm, ok := msg.(loadedMsg); ok && lm.err != nil {
		t.Fatal(lm.err)
	}
	m.Update(msg)
	if _, err := b.GetTask(context.Background(), task.Id); err == nil {
		t.Fatal("task still exists after confirming delete")
	}
	if m.mode != modeNormal || m.edit != nil {
		t.Fatalf("delete did not close the task screen: mode=%v", m.mode)
	}
}

func TestEscapeDiscardsFormChanges(t *testing.T) {
	m, b, task := newTestModel(t)
	m.openEdit(task.Id)
	m.edit.title.SetValue("Unsaved title")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal || m.edit != nil {
		t.Fatalf("escape did not close the task screen: mode=%v", m.mode)
	}
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != task.Title {
		t.Fatalf("escape saved changes: title = %q", got.Title)
	}
}

func TestEnterOnSaveButtonSaves(t *testing.T) {
	m, b, task := newTestModel(t)
	m.openEdit(task.Id)
	m.edit.title.SetValue("Saved with keyboard")
	_ = m.setEditFocus(editSave)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on Save did not produce a save command")
	}
	msg := cmd()
	if lm, ok := msg.(loadedMsg); ok && lm.err != nil {
		t.Fatal(lm.err)
	}
	m.Update(msg)
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Saved with keyboard" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestStatusFieldSavesAndMovesTask(t *testing.T) {
	m, b, task := newTestModel(t)
	m.openEdit(task.Id)
	m.edit.status.SetValue("in_progress")
	_ = m.setEditFocus(editSave)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("save did not produce a command")
	}
	if got := m.taskByID(task.Id); got == nil || got.Status != "in_progress" {
		t.Fatalf("optimistic status = %+v", got)
	}
	flushPendingMoves(t, m)
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got.Status)
	}
}

func TestInvalidStatusKeepsFormOpen(t *testing.T) {
	m, _, task := newTestModel(t)
	m.openEdit(task.Id)
	m.edit.status.SetValue("not-a-column")
	_ = m.setEditFocus(editSave)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("invalid status should not save")
	}
	if m.mode != modeEdit || m.edit == nil || m.edit.err == "" {
		t.Fatalf("form should stay open with an error, mode=%v err=%q", m.mode, m.edit.err)
	}
}

func TestCloseButtonDiscardsChanges(t *testing.T) {
	m, b, task := newTestModel(t)
	m.openEdit(task.Id)
	m.edit.title.SetValue("Not saved")
	m.View()
	e := m.edit
	if e.closeRect.w == 0 {
		t.Fatal("close button has no hitbox")
	}
	m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
		X: e.closeRect.x + 1, Y: e.closeRect.y + 1,
	})
	if m.mode != modeNormal || m.edit != nil {
		t.Fatalf("close button did not close the form: mode=%v", m.mode)
	}
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != task.Title {
		t.Fatalf("close button saved changes: %q", got.Title)
	}
}

func TestDescriptionHasNoLineNumbers(t *testing.T) {
	m, _, task := newTestModel(t)
	m.openEdit(task.Id)
	if m.edit.description.ShowLineNumbers {
		t.Fatal("description still shows line numbers")
	}
	view := m.View()
	if strings.Contains(view, "› ") {
		t.Fatal("field labels still have a focus prefix")
	}
}

type recordingBackend struct {
	sdk.Backend
	mu    sync.Mutex
	moves []*pb.MoveTaskRequest
	err   error
}

func (r *recordingBackend) MoveTask(ctx context.Context, req *pb.MoveTaskRequest) (*pb.Task, error) {
	r.mu.Lock()
	r.moves = append(r.moves, req)
	err := r.err
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return r.Backend.MoveTask(ctx, req)
}

func (r *recordingBackend) recorded() []*pb.MoveTaskRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*pb.MoveTaskRequest(nil), r.moves...)
}

func TestRapidMovesCoalesceToFinalStatus(t *testing.T) {
	m, b, task := newTestModel(t)
	rec := &recordingBackend{Backend: b}
	m.backend = rec
	m.col = 1
	m.row = 0

	m.queueMove(&pb.MoveTaskRequest{Id: task.Id, Status: "in_progress"}, true, "Moved")
	m.queueMove(&pb.MoveTaskRequest{Id: task.Id, Status: "done"}, true, "Moved")
	flushPendingMoves(t, m)

	moves := rec.recorded()
	if len(moves) != 1 {
		t.Fatalf("sent %d moves, want 1: %+v", len(moves), moves)
	}
	if moves[0].Status != "done" {
		t.Fatalf("sent status = %q, want done", moves[0].Status)
	}
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("backend status = %q, want done", got.Status)
	}
}

func TestMoveFailureRollsBack(t *testing.T) {
	m, b, task := newTestModel(t)
	rec := &recordingBackend{Backend: b, err: errors.New("boom")}
	m.backend = rec
	m.col = 1
	m.row = 0

	m.queueMove(&pb.MoveTaskRequest{Id: task.Id, Status: "in_progress"}, true, "Moved")
	if got := m.taskByID(task.Id); got == nil || got.Status != "in_progress" {
		t.Fatalf("optimistic status = %+v", got)
	}
	flushPendingMoves(t, m)

	if m.err == nil {
		t.Fatal("expected an error")
	}
	if got := m.taskByID(task.Id); got == nil || got.Status != "todo" {
		t.Fatalf("rolled back status = %+v", got)
	}
	if m.info.Columns[m.col].Name != "todo" {
		t.Fatalf("selection column = %q, want todo", m.info.Columns[m.col].Name)
	}
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "todo" {
		t.Fatalf("backend status = %q, want todo", got.Status)
	}
}

func TestQuitWithoutPendingMoves(t *testing.T) {
	m, _, _ := newTestModel(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("quit produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("quit did not return a quit message")
	}
}

func TestQuitFlushesPendingMoves(t *testing.T) {
	m, b, task := newTestModel(t)
	m.col = 1
	m.row = 0
	m.queueMove(&pb.MoveTaskRequest{Id: task.Id, Status: "in_progress"}, true, "Moved")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("quit did not flush pending moves")
	}
	if !m.quitting {
		t.Fatal("quit did not enter the saving state")
	}
	if !strings.Contains(m.View(), "Saving changes...") {
		t.Fatal("saving status is not shown")
	}
	if !processMoveResult(t, m, cmd()) {
		t.Fatal("program did not quit after the move completed")
	}
	got, err := b.GetTask(context.Background(), task.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", got.Status)
	}
}

func TestQuitWaitsForInflightMove(t *testing.T) {
	m, _, task := newTestModel(t)
	m.col = 1
	m.row = 0
	m.queueMove(&pb.MoveTaskRequest{Id: task.Id, Status: "in_progress"}, true, "Moved")

	state := m.moves[task.Id]
	state.sent = state.desired
	state.sentPosition = state.desiredPosition
	state.desired = nil
	state.inFlight = true

	_, quitCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if quitCmd != nil {
		t.Fatal("quit should wait for the in-flight move")
	}
	if !m.quitting {
		t.Fatal("quit did not enter the saving state")
	}
	_, next := m.Update(moveResultMsg{taskID: task.Id, request: state.sent})
	if next == nil {
		t.Fatal("completed move did not quit")
	}
	if _, ok := next().(tea.QuitMsg); !ok {
		t.Fatal("completed move did not return a quit message")
	}
}

func TestQuitWaitsForAction(t *testing.T) {
	m, _, _ := newTestModel(t)
	cmd := m.action(func() error { return nil }, "Updated")
	if cmd == nil || m.ops != 1 {
		t.Fatalf("action did not start: cmd=%v ops=%d", cmd != nil, m.ops)
	}
	_, quitCmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if quitCmd != nil {
		t.Fatal("quit should wait for the in-flight action")
	}
	if !m.quitting {
		t.Fatal("quit did not enter the saving state")
	}
	_, next := m.Update(cmd())
	if next == nil {
		t.Fatal("completed action did not quit")
	}
	if _, ok := next().(tea.QuitMsg); !ok {
		t.Fatal("completed action did not return a quit message")
	}
}

func TestStatusMessageOnlyAfterSync(t *testing.T) {
	m, _, task := newTestModel(t)
	m.col = 1
	m.row = 0
	message := "Moved " + task.Key + " to in_progress"
	m.queueMove(&pb.MoveTaskRequest{Id: task.Id, Status: "in_progress"}, true, message)

	if m.message != "" {
		t.Fatalf("message changed before sync: %q", m.message)
	}
	if !strings.Contains(m.View(), "Syncing...") {
		t.Fatal("syncing status is not shown")
	}

	flushPendingMoves(t, m)
	want := "Synced: " + message
	if m.message != want {
		t.Fatalf("message = %q, want %q", m.message, want)
	}
	view := m.View()
	if !strings.Contains(view, want) {
		t.Fatalf("synced message is not shown: %q", view)
	}
	if strings.Contains(view, "Syncing...") {
		t.Fatal("syncing status is still shown after sync")
	}
}

func TestArchivedVisibilityAndLabel(t *testing.T) {
	m, b, task := newTestModel(t)
	if err := b.DeleteTask(context.Background(), task.Id, false); err != nil {
		t.Fatal(err)
	}
	msg := m.load("")()
	m.Update(msg)

	if !m.showArchived {
		t.Fatal("archived tasks should be shown by default")
	}
	view := m.View()
	if !strings.Contains(view, "[archived]") {
		t.Fatalf("archived label is missing:\n%s", view)
	}
	if !strings.Contains(view, "No active tasks. Showing archived tasks.") {
		t.Fatalf("missing archived-only notice:\n%s", view)
	}
	found := false
	for _, h := range m.cardHits {
		if h.taskID == task.Id {
			found = true
		}
	}
	if !found {
		t.Fatal("archived task card was not rendered")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if m.showArchived {
		t.Fatal("a did not hide archived tasks")
	}
	view = m.View()
	if strings.Contains(view, "[archived]") {
		t.Fatal("archived label is still shown")
	}
	if !strings.Contains(view, "No active tasks. Press a to show archived tasks.") {
		t.Fatalf("missing hidden-archived notice:\n%s", view)
	}
	for _, h := range m.cardHits {
		if h.taskID == task.Id {
			t.Fatal("archived task card is still rendered")
		}
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !m.showArchived {
		t.Fatal("a did not show archived tasks")
	}
	view = m.View()
	if !strings.Contains(view, "[archived]") {
		t.Fatalf("archived label did not return:\n%s", view)
	}
}
