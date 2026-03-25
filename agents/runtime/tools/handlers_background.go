package tools

import (
	"fmt"
	"nanocc/agents/background"
	"strings"
	"time"
)

func bashBgHandler(manager *background.Manager) Handler {
	return func(arguments string) string {
		if manager == nil {
			return "error: background manager is not configured"
		}
		command, err := parseCommand(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}
		if err := validateBashCommand(command); err != nil {
			return "blocked: " + err.Error()
		}

		taskID, err := manager.Start(command)
		if err != nil {
			return "error: " + err.Error()
		}
		return fmt.Sprintf("background task started: %s", taskID)
	}
}

func bgWaitHandler(manager *background.Manager) Handler {
	return func(arguments string) string {
		if manager == nil {
			return "error: background manager is not configured"
		}
		args, err := parseBackgroundWaitArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}
		timeout := 30 * time.Second
		if args.TimeoutSec > 0 {
			timeout = time.Duration(args.TimeoutSec) * time.Second
		}

		task, done, err := manager.Wait(args.TaskID, timeout)
		if err != nil {
			return "error: " + err.Error()
		}
		if !done {
			return fmt.Sprintf("background task still running: %s", task.ID)
		}
		return formatBackgroundTask(task)
	}
}

func bgListHandler(manager *background.Manager) Handler {
	return func(arguments string) string {
		if manager == nil {
			return "error: background manager is not configured"
		}
		if strings.TrimSpace(arguments) != "" && strings.TrimSpace(arguments) != "{}" {
			return "invalid args: expected empty object"
		}

		tasks := manager.List()
		if len(tasks) == 0 {
			return "background tasks: none"
		}

		var b strings.Builder
		fmt.Fprintf(&b, "background tasks: %d\n", len(tasks))
		for _, task := range tasks {
			fmt.Fprintf(&b, "- %s [%s] command=%q", task.ID, task.Status, task.Command)
			if !task.StartedAt.IsZero() {
				fmt.Fprintf(&b, " started_at=%s", task.StartedAt.Format(time.RFC3339))
			}
			if task.FinishedAt != nil {
				fmt.Fprintf(&b, " finished_at=%s", task.FinishedAt.Format(time.RFC3339))
			}
			b.WriteString("\n")
		}
		return strings.TrimSpace(b.String())
	}
}

func formatBackgroundTask(task background.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "background task: %s [%s]", task.ID, task.Status)
	if strings.TrimSpace(task.Command) != "" {
		fmt.Fprintf(&b, "\ncommand: %s", task.Command)
	}
	if task.FinishedAt != nil {
		fmt.Fprintf(&b, "\nfinished_at: %s", task.FinishedAt.Format(time.RFC3339))
	}
	if strings.TrimSpace(task.ErrorText) != "" {
		fmt.Fprintf(&b, "\nerror: %s", strings.TrimSpace(task.ErrorText))
	}
	if strings.TrimSpace(task.Output) != "" {
		fmt.Fprintf(&b, "\noutput:\n%s", truncateOutput(strings.TrimSpace(task.Output)))
	}
	return strings.TrimSpace(b.String())
}

func FormatBackgroundNotifications(tasks []background.Task) string {
	if len(tasks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Background task notifications:\n")
	for _, task := range tasks {
		fmt.Fprintf(&b, "- %s [%s] command=%q\n", task.ID, task.Status, task.Command)
		if strings.TrimSpace(task.ErrorText) != "" {
			fmt.Fprintf(&b, "  error: %s\n", strings.TrimSpace(task.ErrorText))
		}
		if strings.TrimSpace(task.Output) != "" {
			fmt.Fprintf(&b, "  output: %s\n", singleLine(truncateOutput(strings.TrimSpace(task.Output))))
		}
	}
	return strings.TrimSpace(b.String())
}

func singleLine(text string) string {
	text = strings.ReplaceAll(text, "\n", " | ")
	return strings.TrimSpace(text)
}
