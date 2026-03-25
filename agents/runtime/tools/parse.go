package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

type subAgentSpawnArgs struct {
	TaskSummary string `json:"task_summary"`
	TimeoutSec  int    `json:"timeout_sec"`
	Retries     int    `json:"retries"`
}

type subAgentWaitArgs struct {
	AgentIDs []string `json:"agent_ids"`
}

type skillNameArgs struct {
	Name string `json:"name"`
}

type backgroundWaitArgs struct {
	TaskID     string `json:"task_id"`
	TimeoutSec int    `json:"timeout_sec"`
}

func parseCommand(arguments string) (string, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", err
	}
	args.Command = strings.TrimSpace(args.Command)
	if args.Command == "" {
		return "", fmt.Errorf("empty command")
	}
	return args.Command, nil
}

func parseBackgroundWaitArgs(arguments string) (backgroundWaitArgs, error) {
	var args backgroundWaitArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return backgroundWaitArgs{}, err
	}
	args.TaskID = strings.TrimSpace(args.TaskID)
	if args.TaskID == "" {
		return backgroundWaitArgs{}, fmt.Errorf("empty task_id")
	}
	if args.TimeoutSec < 0 {
		return backgroundWaitArgs{}, fmt.Errorf("timeout_sec must be >= 0")
	}
	if args.TimeoutSec > 3600 {
		return backgroundWaitArgs{}, fmt.Errorf("timeout_sec too large: max 3600")
	}
	return args, nil
}

func parseReadFileArgs(arguments string) (string, int, error) {
	var args struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", 0, err
	}
	args.Path = strings.TrimSpace(args.Path)
	if args.Path == "" {
		return "", 0, fmt.Errorf("empty path")
	}
	return args.Path, args.Limit, nil
}

func parseWriteFileArgs(arguments string) (string, string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", "", err
	}
	args.Path = strings.TrimSpace(args.Path)
	if args.Path == "" {
		return "", "", fmt.Errorf("empty path")
	}
	return args.Path, args.Content, nil
}

func parseTodoSetArgs(arguments string) ([]TodoTask, string, error) {
	var args struct {
		Tasks     []TodoTask `json:"tasks"`
		CurrentID string     `json:"current_id"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil, "", err
	}

	normalized := make([]TodoTask, 0, len(args.Tasks))
	for _, task := range args.Tasks {
		normalized = append(normalized, TodoTask{
			ID:   strings.TrimSpace(task.ID),
			Text: strings.TrimSpace(task.Text),
			Done: task.Done,
		})
	}
	currentID := strings.TrimSpace(args.CurrentID)
	return normalized, currentID, nil
}

func parseSubAgentSpawnArgs(arguments string) (subAgentSpawnArgs, error) {
	var args subAgentSpawnArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return subAgentSpawnArgs{}, err
	}
	args.TaskSummary = strings.TrimSpace(args.TaskSummary)
	if args.TaskSummary == "" {
		return subAgentSpawnArgs{}, fmt.Errorf("empty task_summary")
	}
	if args.TimeoutSec <= 0 {
		return subAgentSpawnArgs{}, fmt.Errorf("timeout_sec must be positive")
	}
	if args.TimeoutSec > 3600 {
		return subAgentSpawnArgs{}, fmt.Errorf("timeout_sec too large: max 3600")
	}
	if args.Retries < 0 {
		return subAgentSpawnArgs{}, fmt.Errorf("retries must be >= 0")
	}
	return args, nil
}

func parseSubAgentWaitArgs(arguments string) (subAgentWaitArgs, error) {
	if strings.TrimSpace(arguments) == "" {
		return subAgentWaitArgs{}, nil
	}

	var args subAgentWaitArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return subAgentWaitArgs{}, err
	}
	normalized := make([]string, 0, len(args.AgentIDs))
	seen := make(map[string]struct{}, len(args.AgentIDs))
	for _, id := range args.AgentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	args.AgentIDs = normalized
	return args, nil
}

func parseSkillNameArgs(arguments string) (skillNameArgs, error) {
	var args skillNameArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return skillNameArgs{}, err
	}
	args.Name = strings.TrimSpace(args.Name)
	if args.Name == "" {
		return skillNameArgs{}, fmt.Errorf("empty name")
	}
	return args, nil
}
