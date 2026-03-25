package tools

import (
	"fmt"
	"strings"
	"sync"
)

const (
	todoPhaseEmpty     = "empty"
	todoPhaseActive    = "active"
	todoPhaseCompleted = "completed"
)

type TodoTask struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type PersistedTodo struct {
	Tasks     []TodoTask `json:"tasks,omitempty"`
	CurrentID string     `json:"current_id,omitempty"`
	Version   int        `json:"version"`
	Phase     string     `json:"phase,omitempty"`
}

type todoSnapshot struct {
	tasks       []TodoTask
	lastID      string
	lastVersion int
}

type TodoStore struct {
	mu            sync.RWMutex
	tasks         []TodoTask
	currentID     string
	version       int
	phase         string
	lastCompleted *todoSnapshot
}

func NewTodoStore() *TodoStore {
	return &TodoStore{phase: todoPhaseEmpty}
}

func (s *TodoStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = nil
	s.currentID = ""
	s.version = 0
	s.phase = todoPhaseEmpty
}

func (s *TodoStore) Export() PersistedTodo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]TodoTask, len(s.tasks))
	copy(tasks, s.tasks)
	return PersistedTodo{
		Tasks:     tasks,
		CurrentID: s.currentID,
		Version:   s.version,
		Phase:     s.phase,
	}
}

func (s *TodoStore) Import(saved PersistedTodo) error {
	if err := validateTodo(saved.Tasks, saved.CurrentID); err != nil {
		if len(saved.Tasks) > 0 {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.tasks = make([]TodoTask, len(saved.Tasks))
	copy(s.tasks, saved.Tasks)
	s.currentID = strings.TrimSpace(saved.CurrentID)
	s.version = saved.Version
	s.phase = strings.TrimSpace(saved.Phase)
	if s.phase == "" {
		switch {
		case len(s.tasks) == 0:
			s.phase = todoPhaseEmpty
		case isTodoCompletedState(s.tasks, s.currentID):
			s.phase = todoPhaseCompleted
		default:
			s.phase = todoPhaseActive
		}
	}
	return nil
}

func (s *TodoStore) Set(tasks []TodoTask, currentID string) (int, error) {
	if err := validateTodo(tasks, currentID); err != nil {
		return 0, err
	}

	copied := make([]TodoTask, 0, len(tasks))
	for _, task := range tasks {
		copied = append(copied, TodoTask{
			ID:   strings.TrimSpace(task.ID),
			Text: strings.TrimSpace(task.Text),
			Done: task.Done,
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = copied
	s.currentID = strings.TrimSpace(currentID)
	s.version++
	if isTodoCompletedState(s.tasks, s.currentID) {
		s.phase = todoPhaseCompleted
		completedTasks := make([]TodoTask, len(s.tasks))
		copy(completedTasks, s.tasks)
		s.lastCompleted = &todoSnapshot{
			tasks:       completedTasks,
			lastID:      s.currentID,
			lastVersion: s.version,
		}
	} else {
		s.phase = todoPhaseActive
	}
	return s.version, nil
}

func (s *TodoStore) snapshot() ([]TodoTask, string, int, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := make([]TodoTask, 0, len(s.tasks))
	tasks = append(tasks, s.tasks...)
	return tasks, s.currentID, s.version, s.phase
}

func (s *TodoStore) ContextMessage() string {
	tasks, currentID, version, phase := s.snapshot()
	if len(tasks) == 0 {
		return "Current TODO status: (empty). For simple single-turn Q&A, reply directly without TODO. Only call `todo_set` when the task is non-trivial and truly multi-step."
	}
	if phase == todoPhaseCompleted {
		var b strings.Builder
		fmt.Fprintf(&b, "Current TODO status (v%d): COMPLETED.\n", version)
		for _, task := range tasks {
			fmt.Fprintf(&b, "- [x] %s: %s\n", task.ID, task.Text)
		}
		b.WriteString("Current task: (none)\n")
		b.WriteString("The TODO is already complete for this user request. Reply directly to the user now, and do not call `todo_set` again.")
		return b.String()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Current TODO status (v%d):\n", version)
	for _, task := range tasks {
		state := " "
		if task.Done {
			state = "x"
		}
		fmt.Fprintf(&b, "- [%s] %s: %s\n", state, task.ID, task.Text)
	}
	if currentID == "" {
		b.WriteString("Current task: (none)\n")
	} else {
		fmt.Fprintf(&b, "Current task: %s\n", currentID)
	}
	b.WriteString("When progress changes, call `todo_set` with full latest state.")
	return b.String()
}

func (s *TodoStore) RenderForUser() string {
	tasks, currentID, version, _ := s.snapshot()
	if len(tasks) == 0 {
		return "TODO v0: (empty)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "TODO v%d\n", version)
	for _, task := range tasks {
		state := " "
		if task.Done {
			state = "x"
		}
		fmt.Fprintf(&b, "- [%s] %s: %s\n", state, task.ID, task.Text)
	}
	if currentID == "" {
		b.WriteString("Current: (none)")
	} else {
		fmt.Fprintf(&b, "Current: %s", currentID)
	}
	return b.String()
}

func isTodoCompletedState(tasks []TodoTask, currentID string) bool {
	if len(tasks) == 0 || strings.TrimSpace(currentID) != "" {
		return false
	}
	for _, task := range tasks {
		if !task.Done {
			return false
		}
	}
	return true
}

func validateTodo(tasks []TodoTask, currentID string) error {
	if len(tasks) > 50 {
		return fmt.Errorf("too many tasks: max 50")
	}
	trimmedCurrent := strings.TrimSpace(currentID)
	if len(tasks) == 0 && trimmedCurrent != "" {
		return fmt.Errorf("current_id must be empty when tasks is empty")
	}

	seen := make(map[string]struct{}, len(tasks))
	hasCurrent := trimmedCurrent == ""
	for index, task := range tasks {
		id := strings.TrimSpace(task.ID)
		text := strings.TrimSpace(task.Text)
		if id == "" {
			return fmt.Errorf("tasks[%d].id is empty", index)
		}
		if text == "" {
			return fmt.Errorf("tasks[%d].text is empty", index)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate task id: %s", id)
		}
		seen[id] = struct{}{}
		if id == trimmedCurrent {
			hasCurrent = true
			if task.Done {
				return fmt.Errorf("current_id task is marked done: %s", id)
			}
		}
	}
	if !hasCurrent {
		return fmt.Errorf("current_id not found in tasks: %s", trimmedCurrent)
	}
	return nil
}
