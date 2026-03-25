package background

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultTimeout = 30 * time.Minute
	maxOutputChars = 50000
)

type TaskStatus string

const (
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
)

type Task struct {
	ID         string
	Command    string
	Status     TaskStatus
	StartedAt  time.Time
	FinishedAt *time.Time
	Output     string
	ErrorText  string
	Notified   bool

	Done chan struct{}
}

type Manager struct {
	mu     sync.RWMutex
	nextID int64
	tasks  map[string]*Task
}

func NewManager() *Manager {
	return &Manager{tasks: make(map[string]*Task)}
}

func (m *Manager) Start(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("empty command")
	}

	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("bg-%d", m.nextID)
	task := &Task{
		ID:        id,
		Command:   command,
		Status:    TaskRunning,
		StartedAt: time.Now().UTC(),
		Done:      make(chan struct{}),
	}
	m.tasks[id] = task
	m.mu.Unlock()

	go m.run(task)
	return id, nil
}

func (m *Manager) run(task *Task) {
	defer close(task.Done)

	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "zsh", "-lc", task.Command)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()

	finishedAt := time.Now().UTC()
	text := strings.TrimSpace(string(out))
	if text == "" {
		text = "(no output)"
	}
	if len(text) > maxOutputChars {
		text = text[:maxOutputChars]
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	stored, ok := m.tasks[task.ID]
	if !ok {
		return
	}
	stored.FinishedAt = &finishedAt
	stored.Output = text
	if ctx.Err() == context.DeadlineExceeded {
		stored.Status = TaskFailed
		stored.ErrorText = "command timeout (30m)"
		return
	}
	if err != nil {
		stored.Status = TaskFailed
		stored.ErrorText = strings.TrimSpace(err.Error())
		return
	}
	stored.Status = TaskSucceeded
}

func (m *Manager) Wait(id string, timeout time.Duration) (Task, bool, error) {
	task := m.getTask(id)
	if task == nil {
		return Task{}, false, fmt.Errorf("task not found: %s", id)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	select {
	case <-task.Done:
		snap, _, err := m.Snapshot(id)
		return snap, true, err
	case <-time.After(timeout):
		snap, _, err := m.Snapshot(id)
		return snap, false, err
	}
}

func (m *Manager) getTask(id string) *Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tasks[strings.TrimSpace(id)]
}

func (m *Manager) Snapshot(id string) (Task, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	task, ok := m.tasks[strings.TrimSpace(id)]
	if !ok {
		return Task{}, false, fmt.Errorf("task not found: %s", id)
	}
	cp := cloneTask(task)
	return cp, isFinalStatus(task.Status), nil
}

func (m *Manager) List() []Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.tasks))
	for id := range m.tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]Task, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneTask(m.tasks[id]))
	}
	return out
}

func (m *Manager) DrainNotifications() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]string, 0, len(m.tasks))
	for id := range m.tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]Task, 0)
	for _, id := range ids {
		task := m.tasks[id]
		if !isFinalStatus(task.Status) || task.Notified {
			continue
		}
		task.Notified = true
		out = append(out, cloneTask(task))
	}
	return out
}

func cloneTask(task *Task) Task {
	cp := *task
	cp.Done = nil
	return cp
}

func isFinalStatus(status TaskStatus) bool {
	return status == TaskSucceeded || status == TaskFailed
}
