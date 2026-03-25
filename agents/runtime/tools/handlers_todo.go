package tools

import "fmt"

func todoSetHandler(todo *TodoStore) Handler {
	return func(arguments string) string {
		tasks, currentID, err := parseTodoSetArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}

		version, err := todo.Set(tasks, currentID)
		if err != nil {
			return "invalid todo: " + err.Error()
		}

		doneCount := 0
		for _, task := range tasks {
			if task.Done {
				doneCount++
			}
		}
		return fmt.Sprintf("todo updated v%d: %d total, %d done\n%s", version, len(tasks), doneCount, todo.RenderForUser())
	}
}
